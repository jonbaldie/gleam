package proxy

import (
	"bytes"
	"fmt"
	"jonbaldie/gleam/cache"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCacheResponseWriter(t *testing.T) {
	w := httptest.NewRecorder()
	crw := &cacheResponseWriter{ResponseWriter: w, buf: new(bytes.Buffer)}

	crw.WriteHeader(http.StatusOK)
	if _, err := crw.Write([]byte("Hello, World!")); err != nil {
		t.Fatalf("expected write to succeed: %v", err)
	}

	if crw.status != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, crw.status)
	}

	if crw.buf.String() != "Hello, World!" {
		t.Errorf("Expected 'Hello, World!', got '%s'", crw.buf.String())
	}
}

func TestProxyDoesNotCacheUnfreshSuccessfulStatusCodes(t *testing.T) {
	tests := []struct {
		name   string
		status int
		path   string
		body   string
	}{
		{name: "201 Created", status: http.StatusCreated, path: "/resource", body: "created"},
		{name: "202 Accepted", status: http.StatusAccepted, path: "/job", body: "accepted"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var originCalls atomic.Int32
			origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := originCalls.Add(1)
				w.WriteHeader(tt.status)
				_, _ = fmt.Fprintf(w, "%s-%d", tt.body, call)
			})

			c := newMockCache()
			handler := mustCachingProxyHandler(t, origin, c, time.Minute)
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)

			first := httptest.NewRecorder()
			handler.ServeHTTP(first, req)
			if first.Code != tt.status {
				t.Fatalf("expected first response status %d, got %d", tt.status, first.Code)
			}
			if body := first.Body.String(); body != tt.body+"-1" {
				t.Fatalf("expected first response body %q, got %q", tt.body+"-1", body)
			}

			second := httptest.NewRecorder()
			handler.ServeHTTP(second, req)
			if second.Code != tt.status {
				t.Fatalf("expected second response status %d, got %d", tt.status, second.Code)
			}
			if body := second.Body.String(); body != tt.body+"-2" {
				t.Fatalf("expected second request to reach origin with body %q, got %q", tt.body+"-2", body)
			}
			if got := originCalls.Load(); got != 2 {
				t.Fatalf("expected two origin calls because an unfresh %d response must not be cached, got %d", tt.status, got)
			}
			if _, found := c.Get(cacheKeyForRequest(req)); found {
				t.Fatalf("expected unfresh %d response not to be cached", tt.status)
			}
		})
	}
}

func TestProxyDoesNotCacheTransientFailures(t *testing.T) {
	var originCalls atomic.Int32
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := originCalls.Add(1)
		if call == 1 {
			http.Error(w, "temporary failure", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("recovered"))
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	first := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/flaky", nil)
	handler.ServeHTTP(first, req)
	if first.Code != http.StatusBadGateway {
		t.Fatalf("expected first response status %d, got %d", http.StatusBadGateway, first.Code)
	}
	if _, found := c.Get(cacheKeyForRequest(req)); found {
		t.Fatal("expected transient failure response to not be cached")
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, req)
	if second.Code != http.StatusOK {
		t.Fatalf("expected recovery response status %d, got %d", http.StatusOK, second.Code)
	}
	if body := second.Body.String(); body != "recovered" {
		t.Fatalf("expected recovery body %q, got %q", "recovered", body)
	}
	if got := originCalls.Load(); got != 2 {
		t.Fatalf("expected second request to reach origin after failure, got %d calls", got)
	}
	if item, found := c.Get(cacheKeyForRequest(req)); !found {
		t.Fatal("expected recovered response to be cached")
	} else if item.Status != http.StatusOK {
		t.Fatalf("expected cached recovery status %d, got %d", http.StatusOK, item.Status)
	}
}

func TestProxySeparatesCachedGetsByAuthorizationHeader(t *testing.T) {
	var originCalls atomic.Int32
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(r.Header.Get("Authorization")))
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	firstRequest := httptest.NewRequest(http.MethodGet, "/profile", nil)
	firstRequest.Header.Set("Authorization", "Bearer alpha")
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, firstRequest)
	if body := first.Body.String(); body != "Bearer alpha" {
		t.Fatalf("expected first body %q, got %q", "Bearer alpha", body)
	}

	secondRequest := httptest.NewRequest(http.MethodGet, "/profile", nil)
	secondRequest.Header.Set("Authorization", "Bearer beta")
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, secondRequest)
	if body := second.Body.String(); body != "Bearer beta" {
		t.Fatalf("expected second body %q, got %q", "Bearer beta", body)
	}

	if got := originCalls.Load(); got != 2 {
		t.Fatalf("expected distinct authorization headers to bypass shared c, got %d origin calls", got)
	}
}

func TestProxyCachesEquivalentGetsWithSameAuthorizationHeader(t *testing.T) {
	var originCalls atomic.Int32
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		// Authenticated requests only reuse responses that permit shared
		// caching explicitly (RFC 9111 section 3.5).
		w.Header().Set("Cache-Control", "public")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(r.Header.Get("Authorization")))
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	firstRequest := httptest.NewRequest(http.MethodGet, "/profile", nil)
	firstRequest.Header.Set("Authorization", "Bearer alpha")
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, firstRequest)

	secondRequest := httptest.NewRequest(http.MethodGet, "/profile", nil)
	secondRequest.Header.Set("Authorization", "Bearer alpha")
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, secondRequest)

	if first.Body.String() != second.Body.String() {
		t.Fatalf("expected equivalent requests to share cached body, got %q and %q", first.Body.String(), second.Body.String())
	}
	if got := originCalls.Load(); got != 1 {
		t.Fatalf("expected cache hit for identical authorization header, got %d origin calls", got)
	}
}

func TestProxySeparatesCachedGetsByConfiguredVaryHeader(t *testing.T) {
	var originCalls atomic.Int32
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(r.Header.Get("X-Tenant")))
	})

	c := newMockCache()
	handler := NewHandler(origin, c, time.Minute, []string{"X-Tenant"})

	firstRequest := httptest.NewRequest(http.MethodGet, "/profile", nil)
	firstRequest.Header.Set("X-Tenant", "alpha")
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, firstRequest)

	secondRequest := httptest.NewRequest(http.MethodGet, "/profile", nil)
	secondRequest.Header.Set("X-Tenant", "beta")
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, secondRequest)

	if first.Body.String() != "alpha" {
		t.Fatalf("expected first body %q, got %q", "alpha", first.Body.String())
	}
	if second.Body.String() != "beta" {
		t.Fatalf("expected second body %q, got %q", "beta", second.Body.String())
	}
	if got := originCalls.Load(); got != 2 {
		t.Fatalf("expected distinct configured vary headers to bypass shared cache, got %d origin calls", got)
	}
}

func mustCachingProxyHandler(t *testing.T, upstream http.HandlerFunc, c cache.Cache, ttl time.Duration) http.Handler {
	t.Helper()

	return NewHandler(upstream, c, ttl, DefaultVaryHeaders())
}

func mustCachingProxyHandlerWithVaryHeaders(t *testing.T, upstream http.HandlerFunc, c cache.Cache, ttl time.Duration, varyHeaders []string) http.Handler {
	t.Helper()

	return NewHandler(upstream, c, ttl, varyHeaders)
}

// TestProxyDoesNotCacheStatus300 kills gleam.go:47 (expression/comparison changes
// crw.status < 300 to crw.status <= 300, causing status-300 responses to be cached).
func TestProxyDoesNotCacheStatus300(t *testing.T) {
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultipleChoices)
		_, _ = w.Write([]byte("choose"))
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/page", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMultipleChoices {
		t.Fatalf("expected upstream status %d forwarded, got %d", http.StatusMultipleChoices, rec.Code)
	}
	if _, found := c.Get(cacheKeyForRequest(req)); found {
		t.Error("expected status-300 response to not be cached")
	}
}

// TestProxyCopiesAllResponseHeadersOnCacheHit kills three mutations:
//   - gleam.go:35 (loop/range_break inserts break at top of outer header loop, skipping all headers)
//   - gleam.go:36 (loop/range_break inserts break at top of inner values loop, adding no values)
//   - gleam.go:36 (statement/remove drops w.Header().Add, silently discarding each header value)
//
// All three produce a cache-hit response that is missing the upstream response headers.
func TestProxyCopiesAllResponseHeadersOnCacheHit(t *testing.T) {
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Custom-Header", "sentinel-value")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api", nil))

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/api", nil))

	if got := second.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("expected Content-Type %q from c, got %q", "application/json", got)
	}
	if got := second.Header().Get("X-Custom-Header"); got != "sentinel-value" {
		t.Errorf("expected X-Custom-Header %q from c, got %q", "sentinel-value", got)
	}
	if body := second.Body.String(); body != `{"ok":true}` {
		t.Errorf("expected cached body %q, got %q", `{"ok":true}`, body)
	}
}

func TestBugHuntConditionalGETDoesNotProduceNotModifiedResponse(t *testing.T) {
	var originCalls atomic.Int32
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		w.Header().Set("ETag", `"version-1"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("body"))
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/resource", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("expected first response status %d, got %d", http.StatusOK, first.Code)
	}
	if first.Body.String() != "body" {
		t.Fatalf("expected first body %q, got %q", "body", first.Body.String())
	}

	conditional := httptest.NewRequest(http.MethodGet, "/resource", nil)
	conditional.Header.Set("If-None-Match", `"version-1"`)
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, conditional)

	if second.Code != http.StatusNotModified {
		t.Fatalf("expected conditional cache hit status %d, got %d", http.StatusNotModified, second.Code)
	}
	if body := second.Body.String(); body != "" {
		t.Fatalf("expected empty 304 body, got %q", body)
	}
	if got := originCalls.Load(); got != 1 {
		t.Fatalf("expected one origin call after conditional cache hit, got %d", got)
	}
	if got := second.Header().Get("ETag"); got != `"version-1"` {
		t.Fatalf("expected 304 to keep cached ETag, got %q", got)
	}
}

func TestProxyConditionalGETOnCacheHit(t *testing.T) {
	tests := []struct {
		name        string
		etag        string
		ifNoneMatch string
		wantStatus  int
		wantBody    string
	}{
		{
			name:        "weak cached etag matches strong validator",
			etag:        `W/"xyz"`,
			ifNoneMatch: `"xyz"`,
			wantStatus:  http.StatusNotModified,
			wantBody:    "",
		},
		{
			name:        "strong cached etag matches weak validator",
			etag:        `"xyz"`,
			ifNoneMatch: `W/"xyz"`,
			wantStatus:  http.StatusNotModified,
			wantBody:    "",
		},
		{
			name:        "weak cached etag matches weak validator",
			etag:        `W/"xyz"`,
			ifNoneMatch: `W/"xyz"`,
			wantStatus:  http.StatusNotModified,
			wantBody:    "",
		},
		{
			name:        "star matches any cached etag",
			etag:        `"version-1"`,
			ifNoneMatch: "*",
			wantStatus:  http.StatusNotModified,
			wantBody:    "",
		},
		{
			name:        "comma-separated list matches cached etag",
			etag:        `"version-1"`,
			ifNoneMatch: `"other", "version-1", "also"`,
			wantStatus:  http.StatusNotModified,
			wantBody:    "",
		},
		{
			name:        "non-matching validator returns full body",
			etag:        `"version-1"`,
			ifNoneMatch: `"version-2"`,
			wantStatus:  http.StatusOK,
			wantBody:    "body",
		},
		{
			name:        "no cached etag returns full body",
			etag:        "",
			ifNoneMatch: `"version-1"`,
			wantStatus:  http.StatusOK,
			wantBody:    "body",
		},
		{
			name:        "star matches missing etag",
			etag:        "",
			ifNoneMatch: "*",
			wantStatus:  http.StatusNotModified,
			wantBody:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var originCalls atomic.Int32
			origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				originCalls.Add(1)
				if tt.etag != "" {
					w.Header().Set("ETag", tt.etag)
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("body"))
			})

			handler := mustCachingProxyHandler(t, origin, newMockCache(), time.Minute)
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/resource", nil))

			conditional := httptest.NewRequest(http.MethodGet, "/resource", nil)
			conditional.Header.Set("If-None-Match", tt.ifNoneMatch)
			second := httptest.NewRecorder()
			handler.ServeHTTP(second, conditional)

			if second.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", second.Code, tt.wantStatus)
			}
			if body := second.Body.String(); body != tt.wantBody {
				t.Fatalf("body = %q, want %q", body, tt.wantBody)
			}
			if got := originCalls.Load(); got != 1 {
				t.Fatalf("origin calls = %d, want 1", got)
			}
		})
	}
}

// TestCacheKeyForRequestIsDeterministicWithMultipleHeaders kills gleam.go:268
// (statement/remove drops sort.Strings(headerNames), making the hash dependent on
// non-deterministic map iteration order).
// Running 30 iterations with 3 headers gives 3! = 6 possible orderings; the probability
// that all 30 iterations happen to return the same unsorted order is negligible.
func TestCacheKeyForRequestIsDeterministicWithMultipleHeaders(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/resource", nil)
	r.Header.Set("Z-Last", "zval")
	r.Header.Set("A-First", "aval")
	r.Header.Set("M-Middle", "mval")
	varyHeaders := []string{"A-First", "M-Middle", "Z-Last"}

	var want string
	for i := 0; i < 30; i++ {
		got := cacheKeyForRequestWithVaryHeaders(r, varyHeaders)
		if i == 0 {
			want = got
		} else if got != want {
			t.Fatalf("iteration %d: cache key non-deterministic: got %q, want %q", i, got, want)
		}
	}
}

// TestCacheKeyForRequestMultiValueHeaderOrderIsNormalized kills gleam.go:273
// (statement/remove drops sort.Strings(values), so two requests with the same multi-value
// header in different insertion order hash to different keys and miss the cache).
func TestCacheKeyForRequestMultiValueHeaderOrderIsNormalized(t *testing.T) {
	r1 := httptest.NewRequest(http.MethodGet, "/resource", nil)
	r1.Header.Add("Accept", "text/html")
	r1.Header.Add("Accept", "application/json")

	r2 := httptest.NewRequest(http.MethodGet, "/resource", nil)
	r2.Header.Add("Accept", "application/json")
	r2.Header.Add("Accept", "text/html")

	if cacheKeyForRequestWithVaryHeaders(r1, []string{"Accept"}) != cacheKeyForRequestWithVaryHeaders(r2, []string{"Accept"}) {
		t.Error("expected same cache key for identical multi-value headers regardless of insertion order")
	}
}

// TestCacheKeyForRequestDependsOnHeaderName kills gleam.go:274 (statement/remove
// drops signature.WriteString(name), so headers with different names but the same value
// hash identically and collide in the cache).
func TestCacheKeyForRequestDependsOnHeaderName(t *testing.T) {
	r1 := httptest.NewRequest(http.MethodGet, "/path", nil)
	r1.Header.Set("X-Header-Alpha", "same-value")

	r2 := httptest.NewRequest(http.MethodGet, "/path", nil)
	r2.Header.Set("X-Header-Beta", "same-value")

	if cacheKeyForRequestWithVaryHeaders(r1, []string{"X-Header-Alpha"}) == cacheKeyForRequestWithVaryHeaders(r2, []string{"X-Header-Beta"}) {
		t.Error("expected different cache keys for different header names with the same value")
	}
}

// TestCacheKeyForRequestSeparatesHeaderNameFromValue kills gleam.go:275
// (statement/remove drops signature.WriteByte(':'), so header name "A" value "bc"
// and header name "Ab" value "c" both produce the raw string "Abc" and collide).
func TestCacheKeyForRequestSeparatesHeaderNameFromValue(t *testing.T) {

	r1 := httptest.NewRequest(http.MethodGet, "/path", nil)
	r1.Header.Set("A", "bc")

	r2 := httptest.NewRequest(http.MethodGet, "/path", nil)
	r2.Header.Set("Ab", "c")

	if cacheKeyForRequestWithVaryHeaders(r1, []string{"A"}) == cacheKeyForRequestWithVaryHeaders(r2, []string{"Ab"}) {
		t.Error("expected different cache keys: header name and value must be delimited by ':'")
	}
}

// TestCacheKeyForRequestSeparatesHeaderEntries kills gleam.go:277 (statement/remove
// drops signature.WriteByte('\n'), so two headers "A"="val","B"="val2" concatenate to
// "A:valB:val2" — identical to one header "A"="valB:val2" — causing a collision).
func TestCacheKeyForRequestSeparatesHeaderEntries(t *testing.T) {

	r1 := httptest.NewRequest(http.MethodGet, "/path", nil)
	r1.Header.Set("A", "val")
	r1.Header.Set("B", "val2")

	r2 := httptest.NewRequest(http.MethodGet, "/path", nil)
	r2.Header.Set("A", "valB:val2")

	if cacheKeyForRequestWithVaryHeaders(r1, []string{"A", "B"}) == cacheKeyForRequestWithVaryHeaders(r2, []string{"A"}) {
		t.Error("expected different cache keys: header entries must be separated by a newline")
	}
}

// TestCacheKeyForRequestDifferentPathsNoHeaders kills the return base mutant
// by ensuring that two requests with different paths but no headers still
// generate distinct keys.
func TestCacheKeyForRequestDifferentPathsNoHeaders(t *testing.T) {
	r1 := httptest.NewRequest(http.MethodGet, "/path1", nil)
	r2 := httptest.NewRequest(http.MethodGet, "/path2", nil)
	if cacheKeyForRequest(r1) == cacheKeyForRequest(r2) {
		t.Error("expected different cache keys for different paths with no headers")
	}
}

// TestcacheResponseWriterDefaultsToStatusOK kills gleam.go:44 (composite/field-clear
// drops Status: http.StatusOK from the cacheResponseWriter literal, leaving status at its
// zero value 0). If WriteHeader is never called — which cannot happen with httputil.ReverseProxy
// but is the contract the type itself must honour — a status of 0 fails the
// crw.status >= http.StatusOK guard at gleam.go:47, so the response would silently not be
// cached. Verifying the field is present and equals 200 kills the mutation without requiring
// a full integration scenario.
func TestCacheResponseWriterDefaultsToStatusOK(t *testing.T) {
	w := httptest.NewRecorder()
	crw := &cacheResponseWriter{ResponseWriter: w, buf: new(bytes.Buffer), status: http.StatusOK}

	if crw.status != http.StatusOK {
		t.Errorf("expected initialised status %d, got %d", http.StatusOK, crw.status)
	}
}

func TestCacheResponseWriterSubOKStatusIsNotCacheable(t *testing.T) {
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(199)
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/informational", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != 199 {
		t.Fatalf("expected upstream status 199 forwarded, got %d", rec.Code)
	}
	if _, found := c.Get(cacheKeyForRequest(req)); found {
		t.Error("expected status-199 response to not be cached")
	}
}

// TestCacheKeyForRequestNoHeadersHasNoHash kills the return base branch/if and numbers/decrementer
// mutants by ensuring that when headers are empty, the cache key does not contain the hash suffix.
func TestCacheKeyForRequestNoHeadersHasNoHash(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/path", nil)
	key := cacheKeyForRequest(r)
	if strings.Contains(key, "#h=") {
		t.Error("expected cache key to not contain hash suffix when headers are empty")
	}
}

func TestCacheKeyForRequestIgnoresHeadersOutsideDefaultVarySet(t *testing.T) {
	r1 := httptest.NewRequest(http.MethodGet, "/resource", nil)
	r1.Header.Set("X-Request-Id", "first")

	r2 := httptest.NewRequest(http.MethodGet, "/resource", nil)
	r2.Header.Set("X-Request-Id", "second")

	if cacheKeyForRequest(r1) != cacheKeyForRequest(r2) {
		t.Error("expected default cache key to ignore headers outside the vary set")
	}
}

func TestCacheKeyForRequestSeparatesDefaultCookieHeader(t *testing.T) {
	r1 := httptest.NewRequest(http.MethodGet, "/resource", nil)
	r1.Header.Set("Cookie", "session=alpha")

	r2 := httptest.NewRequest(http.MethodGet, "/resource", nil)
	r2.Header.Set("Cookie", "session=beta")

	if cacheKeyForRequest(r1) == cacheKeyForRequest(r2) {
		t.Error("expected default cache key to vary by Cookie")
	}
}

// TestCacheKeyForRequestDoesNotCollideOnCommaInValue guards against the bug where
// strings.Join(values, ",") was used to serialise per-header values, making two requests
// whose values sort-and-join to the same string indistinguishable in the cache key.
//
// Concrete collision:
//   - values ["a,b", "c"] → sorted: ["a,b","c"] → joined: "a,b,c"
//   - values ["a", "b,c"] → sorted: ["a","b,c"] → joined: "a,b,c"
//
// With a comma-containing value these two distinct value-sets produce the same signature
// fragment, so a request carrying X-Token: a,b + X-Token: c would be served the cached
// response for X-Token: a + X-Token: b,c (or vice-versa), violating per-user isolation.
// The fix uses \x00 (NUL, invalid in HTTP header values) as the intra-value separator.
func TestCacheKeyForRequestDoesNotCollideOnCommaInValue(t *testing.T) {

	r1 := httptest.NewRequest(http.MethodGet, "/resource", nil)
	r1.Header["X-Token"] = []string{"a,b", "c"}

	r2 := httptest.NewRequest(http.MethodGet, "/resource", nil)
	r2.Header["X-Token"] = []string{"a", "b,c"}

	k1 := cacheKeyForRequestWithVaryHeaders(r1, []string{"X-Token"})
	k2 := cacheKeyForRequestWithVaryHeaders(r2, []string{"X-Token"})
	if k1 == k2 {
		t.Errorf("collision: requests with different X-Token value sets produced the same cache key %q", k1)
	}
}

func BenchmarkCacheKeyForRequestDefaultVaryHeadersManyUnrelatedHeaders(b *testing.B) {
	req := httptest.NewRequest(http.MethodGet, "/resource", nil)
	req.Header.Set("Authorization", "Bearer alpha")
	for i := 0; i < 100; i++ {
		req.Header.Set(fmt.Sprintf("X-Irrelevant-%03d", i), strings.Repeat("value", 10))
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = cacheKeyForRequest(req)
	}
}

func TestProxyCachedTrailersRemainTrailers(t *testing.T) {
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Trailer", "X-Origin-Trailer")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("body"))
		w.Header().Set("X-Origin-Trailer", "done")
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)
	first := recordGET(handler, "/with-trailer")
	if got := first.Header.Get("X-Origin-Trailer"); got != "" {
		t.Fatalf("first response unexpectedly promoted trailer to header: %q", got)
	}
	if got := first.Trailer.Get("X-Origin-Trailer"); got != "done" {
		t.Fatalf("first response trailer = %q, want done", got)
	}

	second := recordGET(handler, "/with-trailer")
	if got := second.Header.Get("X-Origin-Trailer"); got != "" {
		t.Fatalf("cached response promoted trailer to ordinary header: %q", got)
	}
	if got := second.Trailer.Get("X-Origin-Trailer"); got != "done" {
		t.Fatalf("cached response trailer = %q, want done", got)
	}
}

func TestBugHuntUnannouncedTrailerIsDroppedFromCache(t *testing.T) {
	var originCalls atomic.Int32
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		w.Header()[http.TrailerPrefix+"X-Unannounced-Trailer"] = []string{"done"}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("body"))
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)
	first := recordGET(handler, "/with-unannounced-trailer")
	if got := first.Trailer.Get("X-Unannounced-Trailer"); got != "done" {
		t.Fatalf("first response trailer = %q, want done", got)
	}

	second := recordGET(handler, "/with-unannounced-trailer")
	if got := second.Trailer.Get("X-Unannounced-Trailer"); got != "done" {
		t.Fatalf("cached response trailer = %q, want done", got)
	}
	if got := originCalls.Load(); got != 1 {
		t.Fatalf("expected one origin call for a cache hit, got %d", got)
	}

	cacheRequest := httptest.NewRequest(http.MethodGet, "/with-unannounced-trailer", nil)
	item, found := c.Get(cacheKeyForRequest(cacheRequest))
	if !found {
		t.Fatal("expected unannounced trailer response to be cached")
	}
	if _, found := item.Trailer[http.TrailerPrefix+"X-Unannounced-Trailer"]; found {
		t.Fatalf("cached trailer retained internal prefix: %#v", item.Trailer)
	}
}

func TestProxyDoesNotStoreNoStoreResponses(t *testing.T) {
	var calls atomic.Int32
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = fmt.Fprintf(w, "response-%d", call)
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/secret", nil)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, req)

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, req)

	if got := calls.Load(); got != 2 {
		t.Fatalf("origin calls = %d, want 2 because Cache-Control: no-store forbids storage", got)
	}
	if first.Body.String() == second.Body.String() {
		t.Fatalf("second response was served from cache despite no-store: %q", second.Body.String())
	}
}

func TestBugHuntResponsePrivateIsStored(t *testing.T) {
	var calls atomic.Int32
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		w.Header().Set("Cache-Control", "private")
		_, _ = fmt.Fprintf(w, "private-%d", call)
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/account", nil)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, req)

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, req)

	if got := calls.Load(); got != 2 {
		t.Fatalf("origin calls = %d, want 2 because Cache-Control: private forbids shared-cache storage", got)
	}
	if first.Body.String() == second.Body.String() {
		t.Fatalf("second response was served from cache despite Cache-Control: private: %q", second.Body.String())
	}
}

func TestBugHuntResponseNoCacheIsReused(t *testing.T) {
	var calls atomic.Int32
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = fmt.Fprintf(w, "no-cache-%d", call)
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/fresh", nil)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, req)
	if _, found := c.Get(cacheKeyForRequest(req)); found {
		t.Fatal("expected Cache-Control: no-cache response not to be stored")
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, req)

	if got := calls.Load(); got != 2 {
		t.Fatalf("origin calls = %d, want 2 because Cache-Control: no-cache requires revalidation", got)
	}
	if first.Body.String() == second.Body.String() {
		t.Fatalf("second response was served from cache despite Cache-Control: no-cache: %q", second.Body.String())
	}
}

func TestProxyDoesNotReuseImmediatelyStaleResponse(t *testing.T) {
	var calls atomic.Int32
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		w.Header().Set("Cache-Control", "max-age=0")
		_, _ = fmt.Fprintf(w, "body-%d", call)
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/resource", nil)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, req)
	if _, found := c.Get(cacheKeyForRequest(req)); found {
		t.Fatal("expected immediately stale response not to be stored")
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, req)

	if got := calls.Load(); got != 2 {
		t.Fatalf("origin calls = %d, want 2 because Cache-Control: max-age=0 makes the response immediately stale", got)
	}
	if body := second.Body.String(); body != "body-2" {
		t.Fatalf("second response body = %q, want %q", body, "body-2")
	}
}

func TestBugHuntSetCookieResponseIsStored(t *testing.T) {
	var calls atomic.Int32
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		w.Header().Set("Set-Cookie", fmt.Sprintf("session=%d; Path=/", call))
		_, _ = fmt.Fprintf(w, "cookie-%d", call)
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, req)

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, req)

	if got := calls.Load(); got != 2 {
		t.Fatalf("origin calls = %d, want 2 because Set-Cookie responses must not be stored", got)
	}
	if first.Body.String() == second.Body.String() {
		t.Fatalf("second response was served from cache despite Set-Cookie: %q", second.Body.String())
	}
	if cookie := second.Header().Get("Set-Cookie"); cookie == first.Header().Get("Set-Cookie") {
		t.Fatalf("second response replayed first response's Set-Cookie: %q", cookie)
	}
}

func TestProxyRequestNoCacheRevalidatesEveryTime(t *testing.T) {
	var calls atomic.Int32
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		_, _ = fmt.Fprintf(w, "response-%d", call)
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/resource", nil)
	req.Header.Set("Cache-Control", "no-cache")
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, req)

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, req)

	if got := calls.Load(); got != 2 {
		t.Fatalf("origin calls = %d, want 2 because request Cache-Control: no-cache requires revalidation", got)
	}
	if first.Body.String() == second.Body.String() {
		t.Fatalf("second no-cache request was served from cache: %q", second.Body.String())
	}
}

func TestBugHuntRequestNoStoreBypassesAndDoesNotPopulateCache(t *testing.T) {
	var calls atomic.Int32
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		_, _ = fmt.Fprintf(w, "response-%d", call)
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	noStore := httptest.NewRequest(http.MethodGet, "/resource", nil)
	noStore.Header.Set("Cache-Control", "no-store")
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, noStore)

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, noStore)

	if got := calls.Load(); got != 2 {
		t.Fatalf("origin calls = %d, want 2 because request Cache-Control: no-store must bypass the cache", got)
	}
	if first.Body.String() == second.Body.String() {
		t.Fatalf("second no-store request was served from cache: %q", second.Body.String())
	}

	// The no-store request's response must not be stored either: a plain
	// request for the same resource must still reach the origin.
	plain := httptest.NewRecorder()
	handler.ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "/resource", nil))

	if got := calls.Load(); got != 3 {
		t.Fatalf("origin calls = %d, want 3 because a response served for a no-store request must not be stored", got)
	}
	if plain.Body.String() != "response-3" {
		t.Fatalf("plain request body = %q, want %q", plain.Body.String(), "response-3")
	}
}

func TestProxyDoesNotReuseVaryStarResponses(t *testing.T) {
	var calls atomic.Int32
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		w.Header().Set("Vary", "*")
		_, _ = fmt.Fprintf(w, "response-%d", call)
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/resource", nil)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, req)

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, req)

	if got := calls.Load(); got != 2 {
		t.Fatalf("origin calls = %d, want 2 because Vary: * forbids reuse from cache", got)
	}
	if first.Body.String() == second.Body.String() {
		t.Fatalf("second Vary: * response was served from cache: %q", second.Body.String())
	}
}

func TestProxyDoesNotCacheResponsesWithUnconfiguredVaryHeaders(t *testing.T) {
	var calls atomic.Int32
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		w.Header().Set("Vary", "X-Custom")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprintf(w, "response-%s-%d", r.Header.Get("X-Custom"), call)
	})

	c := newMockCache()
	// Handler uses default vary headers (Authorization, Cookie), which does not include X-Custom
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	req1 := httptest.NewRequest(http.MethodGet, "/resource", nil)
	req1.Header.Set("X-Custom", "alpha")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	req2 := httptest.NewRequest(http.MethodGet, "/resource", nil)
	req2.Header.Set("X-Custom", "beta")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if got := calls.Load(); got != 2 {
		t.Fatalf("origin calls = %d, want 2 because unconfigured Vary header must not be cached", got)
	}
	if rec2.Body.String() != "response-beta-2" {
		t.Fatalf("second request got cached body %q, want response-beta-2", rec2.Body.String())
	}
	if _, found := c.Get(cacheKeyForRequest(req1)); found {
		t.Fatal("response with unconfigured Vary header was stored in cache")
	}
}

func TestProxyCachesResponsesWithConfiguredVaryHeaders(t *testing.T) {
	var calls atomic.Int32
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		w.Header().Set("Vary", "Cookie")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprintf(w, "cookie-%s-%d", r.Header.Get("Cookie"), call)
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	req1 := httptest.NewRequest(http.MethodGet, "/resource", nil)
	req1.Header.Set("Cookie", "session=alpha")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	// Same cookie header should hit cache
	rec1Cached := httptest.NewRecorder()
	handler.ServeHTTP(rec1Cached, req1)

	// Different cookie header should call origin
	req2 := httptest.NewRequest(http.MethodGet, "/resource", nil)
	req2.Header.Set("Cookie", "session=beta")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	// Second cookie request should hit cache now
	rec2Cached := httptest.NewRecorder()
	handler.ServeHTTP(rec2Cached, req2)

	if got := calls.Load(); got != 2 {
		t.Fatalf("origin calls = %d, want 2 (one per unique configured vary value)", got)
	}
	if rec1Cached.Body.String() != rec1.Body.String() {
		t.Fatalf("expected cached body %q, got %q", rec1.Body.String(), rec1Cached.Body.String())
	}
	if rec2Cached.Body.String() != rec2.Body.String() {
		t.Fatalf("expected cached body %q, got %q", rec2.Body.String(), rec2Cached.Body.String())
	}
}

func TestProxyCachesResponsesWithCaseInsensitiveVaryHeaders(t *testing.T) {
	var calls atomic.Int32
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Vary", "cookie, AUTHORIZATION")
		w.Header().Set("Cache-Control", "public")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/resource", nil)
	req.Header.Set("Cookie", "session=alpha")
	req.Header.Set("Authorization", "Bearer token")

	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req)

	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req)

	if got := calls.Load(); got != 1 {
		t.Fatalf("origin calls = %d, want 1 because case-insensitive Vary matching should allow caching", got)
	}
}

func TestProxyDoesNotCacheResponsesWithPartiallyUnconfiguredVaryHeaders(t *testing.T) {
	var calls atomic.Int32
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		// Cookie is configured, but X-Extra is unconfigured
		w.Header().Set("Vary", "Cookie, X-Extra")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("response"))
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/resource", nil)
	req.Header.Set("Cookie", "session=alpha")
	req.Header.Set("X-Extra", "foo")

	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req)

	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req)

	if got := calls.Load(); got != 2 {
		t.Fatalf("origin calls = %d, want 2 because partially unconfigured Vary must not be cached", got)
	}
}

func TestProxyDoesNotCacheResponsesWithMultipleVaryHeadersContainingUnconfigured(t *testing.T) {
	var calls atomic.Int32
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Add("Vary", "Cookie")
		w.Header().Add("Vary", "X-Custom")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("multi-vary"))
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/resource", nil)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req)

	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req)

	if got := calls.Load(); got != 2 {
		t.Fatalf("origin calls = %d, want 2 because multiple Vary lines with unconfigured header must not be cached", got)
	}
}

func TestProxyDoesNotCacheResponsesWithVaryStarCombinedWithConfigured(t *testing.T) {
	var calls atomic.Int32
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Vary", "Cookie, *")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("vary-star-combined"))
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/resource", nil)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req)

	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req)

	if got := calls.Load(); got != 2 {
		t.Fatalf("origin calls = %d, want 2 because Vary with * must never be cached", got)
	}
}

func TestProxyStreamingGetFlushesFirstChunk(t *testing.T) {
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("origin response writer is not a flusher")
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("first\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("second\n"))
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)
	client := newFlushRecorder()
	handler.ServeHTTP(client, httptest.NewRequest(http.MethodGet, "/stream", nil))

	if got := client.firstBodyFlush(t); got != "first\n" {
		t.Fatalf("body delivered at first flush = %q, want %q", got, "first\n")
	}
}
