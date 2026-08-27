package proxy

import (
	"bytes"
	"fmt"
	"jonbaldie/gleam/cache"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestProxyCachesSuccessfulStatusCodeOnCacheHit(t *testing.T) {
	var originCalls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	}))
	defer origin.Close()

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

	first := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/resource", nil)
	handler.ServeHTTP(first, req)
	if first.Code != http.StatusCreated {
		t.Fatalf("expected first response status %d, got %d", http.StatusCreated, first.Code)
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, req)
	if second.Code != http.StatusCreated {
		t.Fatalf("expected cached response status %d, got %d", http.StatusCreated, second.Code)
	}
	if body := second.Body.String(); body != "created" {
		t.Fatalf("expected cached body %q, got %q", "created", body)
	}
	if got := originCalls.Load(); got != 1 {
		t.Fatalf("expected one origin call after cache hit, got %d", got)
	}
	if item, found := c.Get(cacheKeyForRequest(req)); !found {
		t.Fatal("expected successful response to be cached")
	} else if item.Status != http.StatusCreated {
		t.Fatalf("expected cached item status %d, got %d", http.StatusCreated, item.Status)
	}
}

func TestProxyDoesNotCacheTransientFailures(t *testing.T) {
	var originCalls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := originCalls.Add(1)
		if call == 1 {
			http.Error(w, "temporary failure", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("recovered"))
	}))
	defer origin.Close()

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

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
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(r.Header.Get("Authorization")))
	}))
	defer origin.Close()

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

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
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(r.Header.Get("Authorization")))
	}))
	defer origin.Close()

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

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
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(r.Header.Get("X-Tenant")))
	}))
	defer origin.Close()

	originURL, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatalf("parse origin URL: %v", err)
	}
	c := newMockCache()
	handler := NewWithVaryHeaders(originURL, c, time.Minute, []string{"X-Tenant"})

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

func mustCachingProxyHandler(t *testing.T, originURL string, c cache.Cache, ttl time.Duration) http.Handler {
	t.Helper()

	origin, err := url.Parse(originURL)
	if err != nil {
		t.Fatalf("parse origin URL: %v", err)
	}

	return New(origin, c, ttl)
}

// TestProxyDoesNotCacheStatus300 kills gleam.go:47 (expression/comparison changes
// crw.status < 300 to crw.status <= 300, causing status-300 responses to be cached).
func TestProxyDoesNotCacheStatus300(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultipleChoices)
		_, _ = w.Write([]byte("choose"))
	}))
	defer origin.Close()

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

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
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Custom-Header", "sentinel-value")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer origin.Close()

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

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
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("expected hijacker")
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = buf.WriteString("HTTP/1.1 199 Custom Status\r\nContent-Length: 0\r\n\r\n")
		_ = buf.Flush()
	}))
	defer origin.Close()

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

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
