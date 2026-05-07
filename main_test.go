package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
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
	crw.Write([]byte("Hello, World!"))

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
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/resource", nil))
	if first.Code != http.StatusCreated {
		t.Fatalf("expected first response status %d, got %d", http.StatusCreated, first.Code)
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/resource", nil))
	if second.Code != http.StatusCreated {
		t.Fatalf("expected cached response status %d, got %d", http.StatusCreated, second.Code)
	}
	if body := second.Body.String(); body != "created" {
		t.Fatalf("expected cached body %q, got %q", "created", body)
	}
	if got := originCalls.Load(); got != 1 {
		t.Fatalf("expected one origin call after cache hit, got %d", got)
	}
	if item, found := cache.Get("/resource"); !found {
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
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/flaky", nil))
	if first.Code != http.StatusBadGateway {
		t.Fatalf("expected first response status %d, got %d", http.StatusBadGateway, first.Code)
	}
	if _, found := cache.Get("/flaky"); found {
		t.Fatal("expected transient failure response to not be cached")
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/flaky", nil))
	if second.Code != http.StatusOK {
		t.Fatalf("expected recovery response status %d, got %d", http.StatusOK, second.Code)
	}
	if body := second.Body.String(); body != "recovered" {
		t.Fatalf("expected recovery body %q, got %q", "recovered", body)
	}
	if got := originCalls.Load(); got != 2 {
		t.Fatalf("expected second request to reach origin after failure, got %d calls", got)
	}
	if item, found := cache.Get("/flaky"); !found {
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
