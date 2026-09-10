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

	c.Set("key1", cache.CacheItem{Content: []byte("value1"), Header: http.Header{}, Status: http.StatusOK}, time.Minute)
	item, found := c.Get("key1")
	if !found {
		t.Error("Expected to find key1 in cache")
	}
	if string(item.Content) != "value1" {
		t.Errorf("Expected value1, got %s", string(item.Content))
	}

	c.Set("key2", cache.CacheItem{Content: []byte("value2"), Header: http.Header{}, Status: http.StatusOK}, time.Nanosecond)
	time.Sleep(time.Millisecond)
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
	if got, want := strings.Join(config.VaryHeaders, ","), "Authorization,Cookie"; got != want {
		t.Fatalf("expected default vary headers %q, got %q", want, got)
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

func TestLoadConfigFromEnvRejectsOverflowingTTLMinutes(t *testing.T) {
	t.Setenv("ORIGIN_URL", "https://example.com")
	t.Setenv("TTL_MINUTES", "153722868")

	_, err := loadConfigFromEnv()
	if err == nil {
		t.Fatal("expected overflowing positive TTL_MINUTES to fail")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected overflow error for TTL_MINUTES, got %v", err)
	}
}

func TestLoadConfigFromEnvKeepsLargePositiveTTLOverflowFree(t *testing.T) {
	t.Setenv("ORIGIN_URL", "https://example.com")
	t.Setenv("TTL_MINUTES", "153722867")

	config, err := loadConfigFromEnv()
	if err != nil {
		t.Fatalf("expected largest representable TTL_MINUTES to load, got error: %v", err)
	}
	if config.TTL <= 0 {
		t.Fatalf("expected positive TTL, got %v", config.TTL)
	}
	if want := time.Duration(153722867) * time.Minute; config.TTL != want {
		t.Fatalf("expected TTL %v, got %v", want, config.TTL)
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

func TestLoadConfigFromEnvParsesCacheVaryHeaders(t *testing.T) {
	t.Setenv("ORIGIN_URL", "https://example.com")
	t.Setenv("CACHE_VARY_HEADERS", " accept, authorization, ACCEPT ")

	config, err := loadConfigFromEnv()
	if err != nil {
		t.Fatalf("expected config to load, got error: %v", err)
	}

	if got, want := strings.Join(config.VaryHeaders, ","), "Accept,Authorization"; got != want {
		t.Fatalf("expected parsed vary headers %q, got %q", want, got)
	}
}

func TestLoadConfigFromEnvAllowsEmptyCacheVaryHeaders(t *testing.T) {
	t.Setenv("ORIGIN_URL", "https://example.com")
	t.Setenv("CACHE_VARY_HEADERS", "")

	config, err := loadConfigFromEnv()
	if err != nil {
		t.Fatalf("expected config to load, got error: %v", err)
	}

	if len(config.VaryHeaders) != 0 {
		t.Fatalf("expected empty vary headers, got %#v", config.VaryHeaders)
	}
}

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

func TestParseOriginURLWrapsUnderlyingParseError(t *testing.T) {
	_, err := parseOriginURL("https://example.com/\tbad")
	if err == nil {
		t.Skip("url.Parse accepted this input on this Go version; skipping wrap test")
	}
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		t.Errorf("expected error to wrap *url.Error (%%w), errors.As returned false; got: %v", err)
	}
}

func TestSimpleCacheConcurrentSetsDoNotRace(t *testing.T) {
	c := NewSimpleCache()
	var wg sync.WaitGroup
	const N = 200
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", n%5)
			c.Set(key, cache.CacheItem{Content: []byte(fmt.Sprintf("v%d", n)), Header: http.Header{}, Status: http.StatusOK}, time.Minute)
		}(i)
	}
	wg.Wait()
}

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

func TestSimpleCacheExpiredEntriesAreEvicted(t *testing.T) {
	c := NewSimpleCache()

	const n = 1000
	for i := 0; i < n; i++ {
		c.Set(fmt.Sprintf("key%d", i), cache.CacheItem{Content: []byte("value"), Header: http.Header{}, Status: http.StatusOK}, time.Nanosecond)
	}
	time.Sleep(time.Millisecond)

	for i := 0; i < n; i++ {
		if _, found := c.Get(fmt.Sprintf("key%d", i)); found {
			t.Fatalf("expected expired key%d to miss", i)
		}
	}

	c.mu.Lock()
	remaining := len(c.store)
	c.mu.Unlock()
	if remaining != 0 {
		t.Errorf("expected expired entries to be evicted, %d remain in store", remaining)
	}
}

func TestSimpleCacheLiveEntriesSurviveExpiredGet(t *testing.T) {
	c := NewSimpleCache()

	c.Set("live", cache.CacheItem{Content: []byte("value"), Header: http.Header{}, Status: http.StatusOK}, time.Minute)
	c.Set("dead", cache.CacheItem{Content: []byte("value"), Header: http.Header{}, Status: http.StatusOK}, time.Nanosecond)
	time.Sleep(time.Millisecond)

	if _, found := c.Get("dead"); found {
		t.Fatal("expected dead to miss")
	}
	item, found := c.Get("live")
	if !found {
		t.Fatal("expected live to be found after expired-key access")
	}
	if string(item.Content) != "value" {
		t.Errorf("expected value, got %s", string(item.Content))
	}
}

func TestSimpleCacheInvalidatePrefix(t *testing.T) {
	c := NewSimpleCache()

	items := []string{
		"example.com#/resource",
		"example.com#/resource#h=abc123",
		"example.com#/other",
		"example.com#/resource2",
	}
	for _, key := range items {
		c.Set(key, cache.CacheItem{Content: []byte(key), Header: http.Header{}, Status: http.StatusOK}, time.Minute)
	}

	c.InvalidatePrefix("example.com#/resource")

	if _, found := c.Get("example.com#/resource"); found {
		t.Error("expected exact key to be invalidated")
	}
	if _, found := c.Get("example.com#/resource#h=abc123"); found {
		t.Error("expected vary-variant key to be invalidated")
	}
	for _, key := range []string{"example.com#/other", "example.com#/resource2"} {
		if _, found := c.Get(key); !found {
			t.Errorf("expected unrelated key %q to survive invalidation", key)
		}
	}
}

func TestSimpleCacheInvalidatePrefixWithNoMatchingKeys(t *testing.T) {
	c := NewSimpleCache()
	c.Set("example.com#/resource", cache.CacheItem{Content: []byte("value"), Header: http.Header{}, Status: http.StatusOK}, time.Minute)

	c.InvalidatePrefix("other.com#/resource")

	if _, found := c.Get("example.com#/resource"); !found {
		t.Error("expected key to survive invalidation of an unrelated prefix")
	}
}

// SimpleCache must keep the response time of a stored entry so that cache
// hits can report an accurate Age.
func TestSimpleCachePreservesStoredAt(t *testing.T) {
	storedAt := time.Now().Add(-time.Minute)
	c := NewSimpleCache()
	c.Set("key", cache.CacheItem{Content: []byte("body"), Status: 200, StoredAt: storedAt}, time.Minute)

	item, found := c.Get("key")
	if !found {
		t.Fatal("expected stored entry to be found")
	}
	if !item.StoredAt.Equal(storedAt) {
		t.Fatalf("expected StoredAt %v, got %v", storedAt, item.StoredAt)
	}
}
