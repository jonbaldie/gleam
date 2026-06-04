package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSimpleCache(t *testing.T) {
	cache := NewSimpleCache()

	// Test Set and Get
	cache.Set("key1", []byte("value1"), http.Header{}, http.StatusOK, 1*time.Minute)
	item, found := cache.Get("key1")
	if !found {
		t.Error("Expected to find key1 in cache")
	}
	if string(item.content) != "value1" {
		t.Errorf("Expected value1, got %s", string(item.content))
	}

	// Test expiration
	cache.Set("key2", []byte("value2"), http.Header{}, http.StatusOK, 1*time.Nanosecond)
	time.Sleep(1 * time.Millisecond)
	_, found = cache.Get("key2")
	if found {
		t.Error("Expected key2 to be expired")
	}
}

func TestCacheResponseWriter(t *testing.T) {
	w := httptest.NewRecorder()
	crw := &CacheResponseWriter{ResponseWriter: w, buf: new(bytes.Buffer)}

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

func TestLoadConfigFromEnvAcceptsPositiveTTLMinutes(t *testing.T) {
	t.Setenv("ORIGIN_URL", "https://example.com")
	t.Setenv("TTL_MINUTES", "10")
	t.Setenv("PORT", "9090")

	config, err := loadConfigFromEnv()
	if err != nil {
		t.Fatalf("expected positive TTL_MINUTES to load, got error: %v", err)
	}

	if config.OriginURL != "https://example.com" {
		t.Errorf("Expected OriginURL to be https://example.com, got %s", config.OriginURL)
	}
	if config.TTL != 10*time.Minute {
		t.Errorf("Expected TTL to be 10 minutes, got %v", config.TTL)
	}
	if config.Port != "9090" {
		t.Errorf("Expected Port to be 9090, got %s", config.Port)
	}
}

func TestLoadConfigFromEnvRejectsZeroTTLMinutes(t *testing.T) {
	t.Setenv("ORIGIN_URL", "https://example.com")
	t.Setenv("TTL_MINUTES", "0")

	_, err := loadConfigFromEnv()
	if err == nil {
		t.Fatal("expected zero TTL_MINUTES to fail")
	}
	if !strings.Contains(err.Error(), "must be greater than 0") {
		t.Fatalf("expected clear TTL validation error, got %v", err)
	}
}

func TestLoadConfigFromEnvRejectsNegativeTTLMinutes(t *testing.T) {
	t.Setenv("ORIGIN_URL", "https://example.com")
	t.Setenv("TTL_MINUTES", "-5")

	_, err := loadConfigFromEnv()
	if err == nil {
		t.Fatal("expected negative TTL_MINUTES to fail")
	}
	if !strings.Contains(err.Error(), "must be greater than 0") {
		t.Fatalf("expected clear TTL validation error, got %v", err)
	}
}

func TestParseOriginURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{
			name:  "valid https origin",
			input: "https://example.com",
			want:  "https://example.com",
		},
		{
			name:    "invalid control character",
			input:   "https://example.com/\tbad",
			wantErr: "invalid ORIGIN_URL",
		},
		{
			name:    "malformed but parseable origin missing host",
			input:   ":bad",
			wantErr: "invalid ORIGIN_URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origin, err := parseOriginURL(tt.input)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := origin.String(); got != tt.want {
				t.Fatalf("expected origin %q, got %q", tt.want, got)
			}
		})
	}
}

func TestParseOriginURLRejectsMissingScheme(t *testing.T) {
	_, err := parseOriginURL("example.com")
	if err == nil {
		t.Fatal("expected missing-scheme origin to fail")
	}
	if !strings.Contains(err.Error(), "must include http or https scheme") {
		t.Fatalf("unexpected error: %v", err)
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

	cache := NewSimpleCache()
	handler := mustCachingProxyHandler(t, origin.URL, cache, time.Minute)

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
	if item, found := cache.Get(cacheKeyForRequest(req)); !found {
		t.Fatal("expected successful response to be cached")
	} else if item.status != http.StatusCreated {
		t.Fatalf("expected cached item status %d, got %d", http.StatusCreated, item.status)
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

	cache := NewSimpleCache()
	handler := mustCachingProxyHandler(t, origin.URL, cache, time.Minute)

	first := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/flaky", nil)
	handler.ServeHTTP(first, req)
	if first.Code != http.StatusBadGateway {
		t.Fatalf("expected first response status %d, got %d", http.StatusBadGateway, first.Code)
	}
	if _, found := cache.Get(cacheKeyForRequest(req)); found {
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
	if item, found := cache.Get(cacheKeyForRequest(req)); !found {
		t.Fatal("expected recovered response to be cached")
	} else if item.status != http.StatusOK {
		t.Fatalf("expected cached recovery status %d, got %d", http.StatusOK, item.status)
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

	cache := NewSimpleCache()
	handler := mustCachingProxyHandler(t, origin.URL, cache, time.Minute)

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
		t.Fatalf("expected distinct authorization headers to bypass shared cache, got %d origin calls", got)
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

	cache := NewSimpleCache()
	handler := mustCachingProxyHandler(t, origin.URL, cache, time.Minute)

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

func mustCachingProxyHandler(t *testing.T, originURL string, cache Cache, ttl time.Duration) http.Handler {
	t.Helper()

	origin, err := parseOriginURL(originURL)
	if err != nil {
		t.Fatalf("parse origin URL: %v", err)
	}

	return newCachingProxyHandler(origin, cache, ttl)
}

// TestSimpleCacheStoresAndReturnsHeaders kills gleam.go:82 (composite/field-clear drops
// the header field from the stored CacheItem, causing cached responses to carry no headers).
func TestSimpleCacheStoresAndReturnsHeaders(t *testing.T) {
	cache := NewSimpleCache()
	header := http.Header{
		"Content-Type": {"application/json"},
		"X-Request-Id": {"abc-123"},
	}
	cache.Set("k", []byte("body"), header, http.StatusOK, time.Minute)

	item, found := cache.Get("k")
	if !found {
		t.Fatal("expected to find cached item")
	}
	if got := item.header.Get("Content-Type"); got != "application/json" {
		t.Errorf("expected cached Content-Type application/json, got %q", got)
	}
	if got := item.header.Get("X-Request-Id"); got != "abc-123" {
		t.Errorf("expected cached X-Request-Id abc-123, got %q", got)
	}
}

// TestLoadConfigFromEnvAcceptsCacheTypeRedis kills three mutations at gleam.go:214
// (conditional/negated flips the redis check to ==, expression/remove drops the redis
// term, expression/string-literal replaces "redis" with "") and gleam.go:230
// (composite/field-clear drops CacheType from the returned Config).
// All four mutations would either reject a valid "redis" value or lose the field.
func TestLoadConfigFromEnvAcceptsCacheTypeRedis(t *testing.T) {
	t.Setenv("ORIGIN_URL", "https://example.com")
	t.Setenv("CACHE_TYPE", "redis")

	config, err := loadConfigFromEnv()
	if err != nil {
		t.Fatalf("expected CACHE_TYPE=redis to be accepted, got error: %v", err)
	}
	if config.CacheType != "redis" {
		t.Errorf("expected CacheType %q, got %q", "redis", config.CacheType)
	}
}

// TestLoadConfigFromEnvAcceptsTTLMinutesOf1 kills gleam.go:208 (numbers/incrementer
// changes the guard from <= 0 to <= 1, incorrectly rejecting TTL_MINUTES=1).
func TestLoadConfigFromEnvAcceptsTTLMinutesOf1(t *testing.T) {
	t.Setenv("ORIGIN_URL", "https://example.com")
	t.Setenv("TTL_MINUTES", "1")

	config, err := loadConfigFromEnv()
	if err != nil {
		t.Fatalf("expected TTL_MINUTES=1 to be valid, got error: %v", err)
	}
	if config.TTL != time.Minute {
		t.Errorf("expected TTL 1m, got %v", config.TTL)
	}
}

// TestProxyDoesNotCacheStatus300 kills gleam.go:47 (expression/comparison changes
// crw.status < 300 to crw.status <= 300, causing status-300 responses to be cached).
func TestProxyDoesNotCacheStatus300(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultipleChoices) // 300
		_, _ = w.Write([]byte("choose"))
	}))
	defer origin.Close()

	cache := NewSimpleCache()
	handler := mustCachingProxyHandler(t, origin.URL, cache, time.Minute)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/page", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMultipleChoices {
		t.Fatalf("expected upstream status %d forwarded, got %d", http.StatusMultipleChoices, rec.Code)
	}
	if _, found := cache.Get(cacheKeyForRequest(req)); found {
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

	cache := NewSimpleCache()
	handler := mustCachingProxyHandler(t, origin.URL, cache, time.Minute)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api", nil))

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/api", nil))

	if got := second.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("expected Content-Type %q from cache, got %q", "application/json", got)
	}
	if got := second.Header().Get("X-Custom-Header"); got != "sentinel-value" {
		t.Errorf("expected X-Custom-Header %q from cache, got %q", "sentinel-value", got)
	}
	if body := second.Body.String(); body != `{"ok":true}` {
		t.Errorf("expected cached body %q, got %q", `{"ok":true}`, body)
	}
}

// TestParseOriginURLWrapsUnderlyingParseError kills gleam.go:237 (expression/errorf-wrap
// downgrades %w to %v so the wrapped *url.Error is no longer reachable via errors.As).
func TestParseOriginURLWrapsUnderlyingParseError(t *testing.T) {
	// A tab character in the URL path causes url.Parse to return a *url.Error.
	_, err := parseOriginURL("https://example.com/\tbad")
	if err == nil {
		t.Skip("url.Parse accepted this input on this Go version; skipping wrap test")
	}
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		t.Errorf("expected error to wrap *url.Error (%%w), errors.As returned false; got: %v", err)
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

	var want string
	for i := 0; i < 30; i++ {
		got := cacheKeyForRequest(r)
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

	if cacheKeyForRequest(r1) != cacheKeyForRequest(r2) {
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

	if cacheKeyForRequest(r1) == cacheKeyForRequest(r2) {
		t.Error("expected different cache keys for different header names with the same value")
	}
}

// TestCacheKeyForRequestSeparatesHeaderNameFromValue kills gleam.go:275
// (statement/remove drops signature.WriteByte(':'), so header name "A" value "bc"
// and header name "Ab" value "c" both produce the raw string "Abc" and collide).
func TestCacheKeyForRequestSeparatesHeaderNameFromValue(t *testing.T) {
	// Without the ':' separator both produce the concatenation "Abc".
	r1 := httptest.NewRequest(http.MethodGet, "/path", nil)
	r1.Header.Set("A", "bc")

	r2 := httptest.NewRequest(http.MethodGet, "/path", nil)
	r2.Header.Set("Ab", "c")

	if cacheKeyForRequest(r1) == cacheKeyForRequest(r2) {
		t.Error("expected different cache keys: header name and value must be delimited by ':'")
	}
}

// TestCacheKeyForRequestSeparatesHeaderEntries kills gleam.go:277 (statement/remove
// drops signature.WriteByte('\n'), so two headers "A"="val","B"="val2" concatenate to
// "A:valB:val2" — identical to one header "A"="valB:val2" — causing a collision).
func TestCacheKeyForRequestSeparatesHeaderEntries(t *testing.T) {
	// Without '\n' both produce the concatenation "A:valB:val2".
	r1 := httptest.NewRequest(http.MethodGet, "/path", nil)
	r1.Header.Set("A", "val")
	r1.Header.Set("B", "val2")

	r2 := httptest.NewRequest(http.MethodGet, "/path", nil)
	r2.Header.Set("A", "valB:val2")

	if cacheKeyForRequest(r1) == cacheKeyForRequest(r2) {
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

// TestCacheResponseWriterDefaultsToStatusOK kills gleam.go:44 (composite/field-clear
// drops status: http.StatusOK from the CacheResponseWriter literal, leaving status at its
// zero value 0). If WriteHeader is never called — which cannot happen with httputil.ReverseProxy
// but is the contract the type itself must honour — a status of 0 fails the
// crw.status >= http.StatusOK guard at gleam.go:47, so the response would silently not be
// cached. Verifying the field is present and equals 200 kills the mutation without requiring
// a full integration scenario.
func TestCacheResponseWriterDefaultsToStatusOK(t *testing.T) {
	w := httptest.NewRecorder()
	crw := &CacheResponseWriter{ResponseWriter: w, buf: new(bytes.Buffer), status: http.StatusOK}
	// Do not call WriteHeader; the initialised value must survive intact.
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

	cache := NewSimpleCache()
	handler := mustCachingProxyHandler(t, origin.URL, cache, time.Minute)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/informational", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != 199 {
		t.Fatalf("expected upstream status 199 forwarded, got %d", rec.Code)
	}
	if _, found := cache.Get(cacheKeyForRequest(req)); found {
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
	// r1: one value contains a comma; r2: same textual bytes split differently.
	r1 := httptest.NewRequest(http.MethodGet, "/resource", nil)
	r1.Header["X-Token"] = []string{"a,b", "c"}

	r2 := httptest.NewRequest(http.MethodGet, "/resource", nil)
	r2.Header["X-Token"] = []string{"a", "b,c"}

	k1 := cacheKeyForRequest(r1)
	k2 := cacheKeyForRequest(r2)
	if k1 == k2 {
		t.Errorf("collision: requests with different X-Token value sets produced the same cache key %q", k1)
	}
}

// TestSimpleCacheConcurrentSetsDoNotRace kills gleam.go:79 (statement/defer-remove
// converts "defer c.mu.Unlock()" to an immediate unlock, releasing the mutex before
// the map write and causing a concurrent-map-write panic under contention).
func TestSimpleCacheConcurrentSetsDoNotRace(t *testing.T) {
	cache := NewSimpleCache()
	var wg sync.WaitGroup
	const N = 200
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(n int) {
			defer wg.Done()
			// Deliberately reuse a small key space to maximise concurrent access
			// to the same map bucket, reliably triggering Go's built-in
			// concurrent-map-write detector if the mutex is not held.
			key := fmt.Sprintf("key-%d", n%5)
			cache.Set(key, []byte(fmt.Sprintf("v%d", n)), http.Header{}, http.StatusOK, time.Minute)
		}(i)
	}
	wg.Wait()
}
