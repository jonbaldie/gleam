package proxy

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestBugHuntRangeResponseSharesFullResponseKey reproduces issue #35: a range
// request and a full GET share a cache key, so a 206 partial response cached by
// the range request is later served to a full GET.
func TestBugHuntRangeResponseSharesFullResponseKey(t *testing.T) {
	var originCalls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		if r.Header.Get("Range") != "" {
			w.Header().Set("Content-Range", "bytes 0-3/10")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("0123"))
			return
		}
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer origin.Close()

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

	rangeReq := httptest.NewRequest(http.MethodGet, "/resource", nil)
	rangeReq.Header.Set("Range", "bytes=0-3")

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, rangeReq)
	if first.Code != http.StatusPartialContent {
		t.Fatalf("expected range response status 206, got %d", first.Code)
	}
	if got := originCalls.Load(); got != 1 {
		t.Fatalf("expected range request to reach origin, got %d calls", got)
	}
	if _, found := c.Get(cacheKeyForRequest(rangeReq)); found {
		t.Fatal("expected range response to not be cached")
	}

	getReq := httptest.NewRequest(http.MethodGet, "/resource", nil)

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, getReq)
	if second.Code != http.StatusOK {
		t.Fatalf("expected full GET status 200, got %d", second.Code)
	}
	if body := second.Body.String(); body != "0123456789" {
		t.Fatalf("expected full GET body %q, got %q", "0123456789", body)
	}
	if got := originCalls.Load(); got != 2 {
		t.Fatalf("expected full GET to reach origin (cache poisoned by range response), got %d calls", got)
	}

	// Full GET responses continue to be cached normally.
	third := httptest.NewRecorder()
	handler.ServeHTTP(third, getReq)
	if third.Code != http.StatusOK || third.Body.String() != "0123456789" {
		t.Fatalf("expected cached full GET 200 %q, got %d %q", "0123456789", third.Code, third.Body.String())
	}
	if got := originCalls.Load(); got != 2 {
		t.Fatalf("expected third request served from cache, got %d origin calls", got)
	}
}
