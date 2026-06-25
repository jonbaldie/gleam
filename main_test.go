package main

import (
	"errors"
	"fmt"
	"jonbaldie/gleam/cache"
	"jonbaldie/gleam/codec"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSimpleCache(t *testing.T) {
	c := NewSimpleCache()

	// Test Set and Get
	c.Set("key1", cache.CacheItem{Content: []byte("value1"), Header: http.Header{}, Status: http.StatusOK}, 1*time.Minute)
	item, found := c.Get("key1")
	if !found {
		t.Error("Expected to find key1 in cache")
	}
	if string(item.Content) != "value1" {
		t.Errorf("Expected value1, got %s", string(item.Content))
	}

	// Test expiration
	c.Set("key2", cache.CacheItem{Content: []byte("value2"), Header: http.Header{}, Status: http.StatusOK}, 1*time.Nanosecond)
	time.Sleep(1 * time.Millisecond)
	_, found = c.Get("key2")
	if found {
		t.Error("Expected key2 to be expired")
	}
}

func TestLoadConfigFromEnvDefaults(t *testing.T) {
	t.Setenv("ORIGIN_URL", "")
	t.Setenv("TTL_MINUTES", "")
	t.Setenv("PORT", "")
	t.Setenv("REDIS_URL", "")
	t.Setenv("CACHE_TYPE", "")

	config, err := loadConfigFromEnv()
	if err != nil {
		t.Fatalf("expected defaults to load, got error: %v", err)
	}

	if config.OriginURL != "https://httpbin.org" {
		t.Errorf("Expected default OriginURL https://httpbin.org, got %s", config.OriginURL)
	}
	if config.Origin.String() != "https://httpbin.org" {
		t.Errorf("Expected default Origin https://httpbin.org, got %s", config.Origin.String())
	}
	if config.TTL != 5*time.Minute {
		t.Errorf("Expected default TTL 5m, got %v", config.TTL)
	}
	if config.Port != "8080" {
		t.Errorf("Expected default Port 8080, got %s", config.Port)
	}
	if config.RedisURL != "redis://localhost:6379/0" {
		t.Errorf("Expected default RedisURL redis://localhost:6379/0, got %s", config.RedisURL)
	}
	if config.CacheType != "memory" {
		t.Errorf("Expected default CacheType memory, got %s", config.CacheType)
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

// TestSimpleCacheStoresAndReturnsHeaders kills gleam.go:82 (composite/field-clear drops
// the header field from the stored cache.CacheItem, causing cached responses to carry no headers).
func TestSimpleCacheStoresAndReturnsHeaders(t *testing.T) {
	c := NewSimpleCache()
	header := http.Header{
		"Content-Type": {"application/json"},
		"X-Request-Id": {"abc-123"},
	}
	c.Set("k", cache.CacheItem{Content: []byte("body"), Header: header, Status: http.StatusOK}, time.Minute)

	item, found := c.Get("k")
	if !found {
		t.Fatal("expected to find cached item")
	}
	if got := item.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("expected cached Content-Type application/json, got %q", got)
	}
	if got := item.Header.Get("X-Request-Id"); got != "abc-123" {
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

// 300

// TestProxyCopiesAllResponseHeadersOnCacheHit kills three mutations:
//   - gleam.go:35 (loop/range_break inserts break at top of outer header loop, skipping all headers)
//   - gleam.go:36 (loop/range_break inserts break at top of inner values loop, adding no values)
//   - gleam.go:36 (statement/remove drops w.Header().Add, silently discarding each header value)
//
// All three produce a cache-hit response that is missing the upstream response headers.

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

// TestCacheKeyForRequestMultiValueHeaderOrderIsNormalized kills gleam.go:273
// (statement/remove drops sort.Strings(values), so two requests with the same multi-value
// header in different insertion order hash to different keys and miss the cache).

// TestCacheKeyForRequestDependsOnHeaderName kills gleam.go:274 (statement/remove
// drops signature.WriteString(name), so headers with different names but the same value
// hash identically and collide in the cache).

// TestCacheKeyForRequestSeparatesHeaderNameFromValue kills gleam.go:275
// (statement/remove drops signature.WriteByte(':'), so header name "A" value "bc"
// and header name "Ab" value "c" both produce the raw string "Abc" and collide).

// Without the ':' separator both produce the concatenation "Abc".

// TestCacheKeyForRequestSeparatesHeaderEntries kills gleam.go:277 (statement/remove
// drops signature.WriteByte('\n'), so two headers "A"="val","B"="val2" concatenate to
// "A:valB:val2" — identical to one header "A"="valB:val2" — causing a collision).

// Without '\n' both produce the concatenation "A:valB:val2".

// TestCacheKeyForRequestDifferentPathsNoHeaders kills the return base mutant
// by ensuring that two requests with different paths but no headers still
// generate distinct keys.

// TestCacheResponseWriterDefaultsToStatusOK kills gleam.go:44 (composite/field-clear
// drops Status: http.StatusOK from the CacheResponseWriter literal, leaving status at its
// zero value 0). If WriteHeader is never called — which cannot happen with httputil.ReverseProxy
// but is the contract the type itself must honour — a status of 0 fails the
// crw.status >= http.StatusOK guard at gleam.go:47, so the response would silently not be
// cached. Verifying the field is present and equals 200 kills the mutation without requiring
// a full integration scenario.

// Do not call WriteHeader; the initialised value must survive intact.

// TestCacheKeyForRequestNoHeadersHasNoHash kills the return base branch/if and numbers/decrementer
// mutants by ensuring that when headers are empty, the cache key does not contain the hash suffix.

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

// r1: one value contains a comma; r2: same textual bytes split differently.

// TestSimpleCacheConcurrentSetsDoNotRace kills gleam.go:79 (statement/defer-remove
// converts "defer c.mu.Unlock()" to an immediate unlock, releasing the mutex before
// the map write and causing a concurrent-map-write panic under contention).
func TestSimpleCacheConcurrentSetsDoNotRace(t *testing.T) {
	c := NewSimpleCache()
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
			c.Set(key, cache.CacheItem{Content: []byte(fmt.Sprintf("v%d", n)), Header: http.Header{}, Status: http.StatusOK}, time.Minute)
		}(i)
	}
	wg.Wait()
}

// TestNewRedisCache_ValidURL verifies that NewRedisCache accurately parses the REDIS_URL
// and configures the go-redis v8 client properly according to the documented format.
func TestNewRedisCache_ValidURL(t *testing.T) {
	redisURL := "redis://myuser:mypassword@myhost:1234/5"
	c := NewRedisCache(redisURL, &codec.BinaryCodec{})

	opts := c.client.Options()
	if opts.Addr != "myhost:1234" {
		t.Errorf("expected Addr %q, got %q", "myhost:1234", opts.Addr)
	}
	if opts.Username != "myuser" {
		t.Errorf("expected Username %q, got %q", "myuser", opts.Username)
	}
	if opts.Password != "mypassword" {
		t.Errorf("expected Password %q, got %q", "mypassword", opts.Password)
	}
	if opts.DB != 5 {
		t.Errorf("expected DB %d, got %d", 5, opts.DB)
	}
}

// TestNewRedisCache_DefaultURL verifies that NewRedisCache accurately parses the default
// REDIS_URL documented in the README.
func TestNewRedisCache_DefaultURL(t *testing.T) {
	redisURL := "redis://localhost:6379/0"
	c := NewRedisCache(redisURL, &codec.BinaryCodec{})

	opts := c.client.Options()
	if opts.Addr != "localhost:6379" {
		t.Errorf("expected Addr %q, got %q", "localhost:6379", opts.Addr)
	}
	if opts.Username != "" {
		t.Errorf("expected empty Username, got %q", opts.Username)
	}
	if opts.Password != "" {
		t.Errorf("expected empty Password, got %q", opts.Password)
	}
	if opts.DB != 0 {
		t.Errorf("expected DB %d, got %d", 0, opts.DB)
	}
}
