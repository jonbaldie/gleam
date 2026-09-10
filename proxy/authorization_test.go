package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestBugHuntAuthorizedResponseReusedWithoutPermission reproduces
// jonbaldie/gleam#67: a shared cache must not reuse a stored response for a
// request carrying Authorization unless the response explicitly permits it
// (RFC 9111 section 3.5).
func TestBugHuntAuthorizedResponseReusedWithoutPermission(t *testing.T) {
	var calls int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "response-%d", n)
	}))
	defer origin.Close()

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

	for i := 1; i <= 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/resource", nil)
		req.Header.Set("Authorization", "Bearer token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if got, want := rec.Body.String(), fmt.Sprintf("response-%d", i); got != want {
			t.Fatalf("request %d: expected body %q, got %q", i, want, got)
		}
	}
	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Fatalf("expected the origin to be called twice, got %d", got)
	}
}

// An explicit shared-cache permission directive restores reuse for
// authenticated requests.
func TestProxyReusesAuthorizedResponseWithSharedCachePermission(t *testing.T) {
	for _, directive := range []string{"public", "must-revalidate", "s-maxage=60"} {
		t.Run(directive, func(t *testing.T) {
			var calls int64
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := atomic.AddInt64(&calls, 1)
				w.Header().Set("Cache-Control", directive)
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprintf(w, "response-%d", n)
			}))
			defer origin.Close()

			c := newMockCache()
			handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

			for i := 1; i <= 2; i++ {
				req := httptest.NewRequest(http.MethodGet, "/resource", nil)
				req.Header.Set("Authorization", "Bearer token")
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if got, want := rec.Body.String(), "response-1"; got != want {
					t.Fatalf("request %d: expected body %q, got %q", i, want, got)
				}
			}
			if got := atomic.LoadInt64(&calls); got != 1 {
				t.Fatalf("expected the origin to be called once, got %d", got)
			}
		})
	}
}

// A stored response without a permission directive stays reusable for
// unauthenticated requests.
func TestProxyReusesUnauthenticatedResponseWithoutPermission(t *testing.T) {
	var calls int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "response-%d", n)
	}))
	defer origin.Close()

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

	for i := 1; i <= 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/resource", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if got, want := rec.Body.String(), "response-1"; got != want {
			t.Fatalf("request %d: expected body %q, got %q", i, want, got)
		}
	}
	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Fatalf("expected the origin to be called once, got %d", got)
	}
}

// A cached entry stored for an unauthenticated request must not be handed to
// an authenticated one, even where the deployment's vary headers leave
// Authorization out of the cache key.
func TestProxyDoesNotServeStoredEntryToAuthorizedRequestWithoutPermission(t *testing.T) {
	var calls int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "response-%d", n)
	}))
	defer origin.Close()

	c := newMockCache()
	handler := mustCachingProxyHandlerWithVaryHeaders(t, origin.URL, c, time.Minute, []string{"Cookie"})

	plain := httptest.NewRecorder()
	handler.ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "/resource", nil))
	if got, want := plain.Body.String(), "response-1"; got != want {
		t.Fatalf("expected body %q, got %q", want, got)
	}

	authorized := httptest.NewRequest(http.MethodGet, "/resource", nil)
	authorized.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authorized)
	if got, want := rec.Body.String(), "response-2"; got != want {
		t.Fatalf("expected authorized request to reach the origin, got body %q", got)
	}
	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Fatalf("expected the origin to be called twice, got %d", got)
	}
}
