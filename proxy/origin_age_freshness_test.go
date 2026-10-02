package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestBugHuntOriginAgeIgnoredForFreshness reproduces jonbaldie/gleam#66: a
// response that arrives already at the end of its freshness lifetime — max-age
// fully consumed by the origin's Age — must not be reused without validation.
func TestBugHuntOriginAgeIgnoredForFreshness(t *testing.T) {
	var calls int64
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		w.Header().Set("Cache-Control", "max-age=60")
		w.Header().Set("Age", "60")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "response-%d", n)
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/resource", nil)
	for i := 1; i <= 2; i++ {
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

// TestBugHuntHeuristicCacheIgnoresOriginAge reproduces jonbaldie/gleam#88: a
// heuristically cacheable response (no Cache-Control/Expires) that already
// carries an Age exceeding the configured TTL is already stale on arrival and
// must not be served from cache without revalidation.
func TestBugHuntHeuristicCacheIgnoresOriginAge(t *testing.T) {
	var calls int64
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		w.Header().Set("Age", "600")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "response-%d", n)
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/resource", nil)
	for i := 1; i <= 2; i++ {
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
