package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
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

func TestLoadConfig(t *testing.T) {
	// Save current env vars
	oldOriginURL := os.Getenv("ORIGIN_URL")
	oldTTLMinutes := os.Getenv("TTL_MINUTES")
	oldPort := os.Getenv("PORT")

	// Set test env vars
	os.Setenv("ORIGIN_URL", "https://example.com")
	os.Setenv("TTL_MINUTES", "10")
	os.Setenv("PORT", "9090")

	config := loadConfig()

	if config.OriginURL != "https://example.com" {
		t.Errorf("Expected OriginURL to be https://example.com, got %s", config.OriginURL)
	}
	if config.TTL != 10*time.Minute {
		t.Errorf("Expected TTL to be 10 minutes, got %v", config.TTL)
	}
	if config.Port != "9090" {
		t.Errorf("Expected Port to be 9090, got %s", config.Port)
	}

	// Restore original env vars
	os.Setenv("ORIGIN_URL", oldOriginURL)
	os.Setenv("TTL_MINUTES", oldTTLMinutes)
	os.Setenv("PORT", oldPort)
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
	handler := newCachingProxyHandler(t, origin.URL, cache, time.Minute)

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
	handler := newCachingProxyHandler(t, origin.URL, cache, time.Minute)

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

func newCachingProxyHandler(t *testing.T, originURL string, cache Cache, ttl time.Duration) http.Handler {
	t.Helper()

	origin, err := url.Parse(originURL)
	if err != nil {
		t.Fatalf("parse origin URL: %v", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(origin)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			cacheKey := r.URL.String()
			if cachedItem, found := cache.Get(cacheKey); found {
				for key, values := range cachedItem.header {
					for _, value := range values {
						w.Header().Add(key, value)
					}
				}
				w.WriteHeader(cachedItem.status)
				w.Write(cachedItem.content)
				return
			}

			crw := &CacheResponseWriter{ResponseWriter: w, buf: new(bytes.Buffer), status: http.StatusOK}
			proxy.ServeHTTP(crw, r)
			if crw.status >= http.StatusOK && crw.status < http.StatusMultipleChoices {
				cache.Set(cacheKey, crw.buf.Bytes(), crw.Header(), crw.status, ttl)
			}
			return
		}

		proxy.ServeHTTP(w, r)
	})
}
