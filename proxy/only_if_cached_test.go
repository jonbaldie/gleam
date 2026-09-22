package proxy

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"jonbaldie/gleam/cache"
)

func TestProxyOnlyIfCachedMissReturnsGatewayTimeout(t *testing.T) {
	var originCalls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		http.Error(w, "origin should not receive only-if-cached miss", http.StatusInternalServerError)
	}))
	defer origin.Close()

	handler := mustCachingProxyHandler(t, origin.URL, newMockCache(), time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/nothing-cached", nil)
	req.Header.Set("Cache-Control", "only-if-cached")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected only-if-cached cache miss status %d, got %d", http.StatusGatewayTimeout, rec.Code)
	}
	if got := originCalls.Load(); got != 0 {
		t.Fatalf("expected only-if-cached cache miss to avoid the origin, got %d calls", got)
	}
}

func TestProxyOnlyIfCachedHitUsesCachedResponse(t *testing.T) {
	var originCalls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		w.Header().Set("Cache-Control", "max-age=60")
		_, _ = w.Write([]byte("cached response"))
	}))
	defer origin.Close()

	handler := mustCachingProxyHandler(t, origin.URL, newMockCache(), time.Minute)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/cached", nil))
	if first.Code != http.StatusOK || first.Body.String() != "cached response" {
		t.Fatalf("expected initial origin response 200 %q, got %d %q", "cached response", first.Code, first.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/cached", nil)
	req.Header.Set("Cache-Control", "only-if-cached")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected only-if-cached cache hit status %d, got %d", http.StatusOK, rec.Code)
	}
	if got := rec.Body.String(); got != "cached response" {
		t.Fatalf("expected cached response body %q, got %q", "cached response", got)
	}
	if got := originCalls.Load(); got != 1 {
		t.Fatalf("expected only-if-cached cache hit to avoid a second origin call, got %d calls", got)
	}
}

func TestProxyOnlyIfCachedExpiredEntryReturnsGatewayTimeout(t *testing.T) {
	var originCalls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		http.Error(w, "origin should not receive expired only-if-cached entry", http.StatusInternalServerError)
	}))
	defer origin.Close()

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/expired", nil)
	req.Header.Set("Cache-Control", "only-if-cached")
	c.Set(cacheKeyForRequest(req), cache.CacheItem{
		Content: []byte("expired response"),
		Status:  http.StatusOK,
	}, -time.Second)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected expired only-if-cached entry status %d, got %d", http.StatusGatewayTimeout, rec.Code)
	}
	if got := originCalls.Load(); got != 0 {
		t.Fatalf("expected expired only-if-cached entry to avoid the origin, got %d calls", got)
	}
}

func TestProxyOnlyIfCachedUnauthorizedEntryReturnsGatewayTimeout(t *testing.T) {
	var originCalls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		w.Header().Set("Cache-Control", "max-age=60")
		_, _ = w.Write([]byte("shared response"))
	}))
	defer origin.Close()

	handler := mustCachingProxyHandlerWithVaryHeaders(t, origin.URL, newMockCache(), time.Minute, []string{"Cookie"})
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/authorized", nil))

	req := httptest.NewRequest(http.MethodGet, "/authorized", nil)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Cache-Control", "only-if-cached")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected unauthorized only-if-cached entry status %d, got %d", http.StatusGatewayTimeout, rec.Code)
	}
	if got := originCalls.Load(); got != 1 {
		t.Fatalf("expected unauthorized only-if-cached entry to avoid a second origin call, got %d calls", got)
	}
}

func TestProxyOnlyIfCachedRangeRequestReturnsGatewayTimeout(t *testing.T) {
	var originCalls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		_, _ = w.Write([]byte("full response"))
	}))
	defer origin.Close()

	handler := mustCachingProxyHandler(t, origin.URL, newMockCache(), time.Minute)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/range", nil))

	req := httptest.NewRequest(http.MethodGet, "/range", nil)
	req.Header.Set("Range", "bytes=0-3")
	req.Header.Set("Cache-Control", "only-if-cached")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected range only-if-cached request status %d, got %d", http.StatusGatewayTimeout, rec.Code)
	}
	if got := originCalls.Load(); got != 1 {
		t.Fatalf("expected range only-if-cached request to avoid a second origin call, got %d calls", got)
	}
}
