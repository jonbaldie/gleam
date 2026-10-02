package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestMultipleExpiresHeadersMustNotBeCached reproduces jonbaldie/gleam#107:
// RFC 9111 section 5.3 requires a response with more than one Expires header
// field (or field value) to be treated as having an invalid date format, i.e.
// already expired. The proxy must not store it and later requests must reach
// the origin.
func TestMultipleExpiresHeadersMustNotBeCached(t *testing.T) {
	var originCalls atomic.Int32
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := originCalls.Add(1)
		w.Header().Set("Date", time.Now().UTC().Format(http.TimeFormat))
		w.Header().Add("Expires", time.Now().UTC().Add(1*time.Hour).Format(http.TimeFormat))
		w.Header().Add("Expires", time.Now().UTC().Add(2*time.Hour).Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "origin-%d", call)
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/resource", nil)
	for i := 1; i <= 2; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if got, want := rec.Body.String(), fmt.Sprintf("origin-%d", i); got != want {
			t.Fatalf("request %d: expected body %q, got %q", i, want, got)
		}
	}
	if got := originCalls.Load(); got != 2 {
		t.Fatalf("expected the origin to be called twice, got %d", got)
	}
}

// TestBugHuntExpiresZeroServedFromCache reproduces jonbaldie/gleam#76:
// When an origin response carries Expires: 0, it explicitly indicates that the
// response is already expired. It must not be cached for the configured TTL.
func TestBugHuntExpiresZeroServedFromCache(t *testing.T) {
	var calls int64
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		w.Header().Set("Date", "Fri, 11 Sep 2026 04:00:00 GMT")
		w.Header().Set("Expires", "0")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "response-%d", n)
	})

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin, c, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/resource", nil)
	for i := 1; i <= 2; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if got, want := rec.Body.String(), fmt.Sprintf("response-%d", i); got != want {
			t.Fatalf("request %d: expected body %q, got %q", i, want, got)
		}
	}
	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Fatalf("expected the origin to be called twice, got %d", got)
	}
}
