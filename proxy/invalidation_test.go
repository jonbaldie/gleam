package proxy

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestProxySuccessfulPOSTInvalidatesCachedGET reproduces issue #58: a
// successful POST to a URI must invalidate stored GET responses for that URI
// (RFC 9111 section 4.4), so a subsequent GET reaches the origin instead of
// serving data from before the write.
func TestProxySuccessfulPOSTInvalidatesCachedGET(t *testing.T) {
	var getCalls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if getCalls.Add(1) == 1 {
				_, _ = w.Write([]byte("old"))
				return
			}
			_, _ = w.Write([]byte("new"))
		case http.MethodPost:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected origin request %s %s", r.Method, r.URL)
		}
	}))
	defer origin.Close()

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

	first := httptest.NewRequest(http.MethodGet, "/resource", nil)
	firstResp := httptest.NewRecorder()
	handler.ServeHTTP(firstResp, first)
	if body := firstResp.Body.String(); body != "old" {
		t.Fatalf("expected first GET body %q, got %q", "old", body)
	}

	post := httptest.NewRequest(http.MethodPost, "/resource", nil)
	postResp := httptest.NewRecorder()
	handler.ServeHTTP(postResp, post)
	if postResp.Code != http.StatusNoContent {
		t.Fatalf("expected POST status %d, got %d", http.StatusNoContent, postResp.Code)
	}

	second := httptest.NewRequest(http.MethodGet, "/resource", nil)
	secondResp := httptest.NewRecorder()
	handler.ServeHTTP(secondResp, second)
	if body := secondResp.Body.String(); body != "new" {
		t.Fatalf("expected final GET to fetch %q from origin after successful POST, got stale %q", "new", body)
	}
	if got := getCalls.Load(); got != 2 {
		t.Fatalf("expected two origin GET calls after invalidation, got %d", got)
	}
}

// A cache key is the host+URI base plus an optional hash suffix encoding the
// configured vary headers the request carried, so invalidation must remove
// every variant for the URI, not just the incoming request's own key.
func TestProxySuccessfulPOSTInvalidatesAllVaryVariants(t *testing.T) {
	var getCalls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		getCalls.Add(1)
		_, _ = w.Write([]byte("state-" + r.Header.Get("Authorization")))
	}))
	defer origin.Close()

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

	for _, authorization := range []string{"Bearer alpha", "Bearer beta"} {
		req := httptest.NewRequest(http.MethodGet, "/resource", nil)
		req.Header.Set("Authorization", authorization)
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if _, found := c.Get(cacheKeyForRequest(req)); !found {
			t.Fatalf("expected %q response to be cached", authorization)
		}
	}

	post := httptest.NewRequest(http.MethodPost, "/resource", nil)
	postResp := httptest.NewRecorder()
	handler.ServeHTTP(postResp, post)
	if postResp.Code != http.StatusNoContent {
		t.Fatalf("expected POST status %d, got %d", http.StatusNoContent, postResp.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/resource", nil)
	req.Header.Set("Authorization", "Bearer alpha")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if body := resp.Body.String(); body != "state-Bearer alpha" {
		t.Fatalf("expected origin refetch after invalidating all variants, got %q", body)
	}
	if got := getCalls.Load(); got != 3 {
		t.Fatalf("expected three origin GET calls (two cached, one refetch), got %d", got)
	}
}

// Non-error responses are 2xx or 3xx (RFC 9111 section 4.4); 4xx and 5xx
// responses leave stored responses for the target URI intact.
func TestProxyFailedPOSTKeepsCachedGET(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusInternalServerError, http.StatusBadGateway} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var getCalls atomic.Int32
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					getCalls.Add(1)
					_, _ = w.Write([]byte("old"))
				case http.MethodPost:
					w.WriteHeader(status)
				default:
					t.Errorf("unexpected origin request %s %s", r.Method, r.URL)
				}
			}))
			defer origin.Close()

			c := newMockCache()
			handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

			get := httptest.NewRequest(http.MethodGet, "/resource", nil)
			first := httptest.NewRecorder()
			handler.ServeHTTP(first, get)

			post := httptest.NewRequest(http.MethodPost, "/resource", nil)
			postResp := httptest.NewRecorder()
			handler.ServeHTTP(postResp, post)
			if postResp.Code != status {
				t.Fatalf("expected POST status %d, got %d", status, postResp.Code)
			}
			if _, found := c.Get(cacheKeyForRequest(get)); !found {
				t.Fatalf("status %d must not invalidate the cached entry", status)
			}

			second := httptest.NewRecorder()
			handler.ServeHTTP(second, get)
			if body := second.Body.String(); body != "old" {
				t.Fatalf("expected cached body %q after failed POST, got %q", "old", body)
			}
			if got := getCalls.Load(); got != 1 {
				t.Fatalf("expected one origin GET call (second GET served from cache), got %d", got)
			}
		})
	}
}

func TestProxyRedirectPOSTInvalidatesCachedGET(t *testing.T) {
	var getCalls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			http.Redirect(w, r, "/elsewhere", http.StatusSeeOther)
			return
		}
		if getCalls.Add(1) == 1 {
			_, _ = w.Write([]byte("old"))
			return
		}
		_, _ = w.Write([]byte("new"))
	}))
	defer origin.Close()

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/resource", nil))
	if body := first.Body.String(); body != "old" {
		t.Fatalf("expected first GET body %q, got %q", "old", body)
	}

	postResp := httptest.NewRecorder()
	handler.ServeHTTP(postResp, httptest.NewRequest(http.MethodPost, "/resource", nil))
	if postResp.Code != http.StatusSeeOther {
		t.Fatalf("expected POST status %d, got %d", http.StatusSeeOther, postResp.Code)
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/resource", nil))
	if body := second.Body.String(); body != "new" {
		t.Fatalf("expected 3xx POST response to invalidate cached GET, got stale %q", body)
	}
}

func TestProxySuccessfulUnsafeMethodsInvalidateCachedGET(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			var getCalls atomic.Int32
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					if getCalls.Add(1) == 1 {
						_, _ = w.Write([]byte("old"))
						return
					}
					_, _ = w.Write([]byte("new"))
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer origin.Close()

			c := newMockCache()
			handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

			get := httptest.NewRequest(http.MethodGet, "/resource", nil)
			first := httptest.NewRecorder()
			handler.ServeHTTP(first, get)
			if body := first.Body.String(); body != "old" {
				t.Fatalf("expected first GET body %q, got %q", "old", body)
			}

			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, httptest.NewRequest(method, "/resource", nil))
			if resp.Code != http.StatusNoContent {
				t.Fatalf("expected %s status %d, got %d", method, http.StatusNoContent, resp.Code)
			}

			second := httptest.NewRecorder()
			handler.ServeHTTP(second, get)
			if body := second.Body.String(); body != "new" {
				t.Fatalf("expected successful %s to invalidate cached GET, got stale %q", method, body)
			}
		})
	}
}

// Safe methods do not change origin state, so a 2xx to GET/HEAD/OPTIONS/TRACE
// must leave stored responses for the URI intact (RFC 9111 section 4.4).
func TestProxySafeMethodsDoNotInvalidateCachedGET(t *testing.T) {
	var getCalls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			getCalls.Add(1)
			_, _ = w.Write([]byte("old"))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer origin.Close()

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

	get := httptest.NewRequest(http.MethodGet, "/resource", nil)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, get)

	for _, method := range []string{http.MethodHead, http.MethodOptions, http.MethodTrace} {
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, httptest.NewRequest(method, "/resource", nil))
		if resp.Code != http.StatusOK {
			t.Fatalf("expected %s status %d, got %d", method, http.StatusOK, resp.Code)
		}
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, get)
	if body := second.Body.String(); body != "old" {
		t.Fatalf("expected safe 2xx responses to keep cached body %q, got %q", "old", body)
	}
	if got := getCalls.Load(); got != 1 {
		t.Fatalf("expected safe methods not to invalidate, got %d origin GET calls", got)
	}
}

// The invalidation wrapper must not introduce buffering on the non-GET path:
// the origin's first POST response chunk must reach the client before the
// origin has finished writing the response.
func TestProxyPOSTStreamsResponseWithoutBuffering(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("first\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte("second"))
	}))
	defer origin.Close()

	handler := mustCachingProxyHandler(t, origin.URL, newMockCache(), time.Minute)
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	resp, err := http.Post(proxy.URL+"/resource", "text/plain", strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	lineCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(resp.Body).ReadString('\n')
		if err != nil {
			errCh <- err
			return
		}
		lineCh <- line
	}()

	select {
	case line := <-lineCh:
		if line != "first\n" {
			t.Fatalf("expected first streamed line %q, got %q", "first\n", line)
		}
	case err := <-errCh:
		t.Fatalf("stream read failed: %v", err)
	case <-time.After(time.Second):
		t.Fatal("first POST response chunk was not streamed before the origin finished")
	}
}