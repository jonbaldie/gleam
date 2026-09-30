package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestInvalidCacheControlAgeMustNotBeCached reproduces jonbaldie/gleam#122:
// RFC 9111 section 4.2.1 encourages caches to treat responses with invalid
// freshness information, such as a negative or non-integer max-age, as stale.
// Gleam serves hits without revalidation, so it must not store them.
func TestInvalidCacheControlAgeMustNotBeCached(t *testing.T) {
	for _, directive := range []string{
		"max-age=-1",
		"max-age=invalid",
		"s-maxage=-1",
		"s-maxage=invalid",
	} {
		t.Run(directive, func(t *testing.T) {
			var originCalls atomic.Int32
			origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := originCalls.Add(1)
				w.Header().Set("Cache-Control", directive)
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprintf(w, "origin-%d", call)
			})

			handler := mustCachingProxyHandler(t, origin, newMockCache(), time.Minute)

			for i := 1; i <= 2; i++ {
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/resource", nil))
				if got, want := rec.Body.String(), fmt.Sprintf("origin-%d", i); got != want {
					t.Fatalf("request %d: expected body %q, got %q", i, want, got)
				}
			}
			if got := originCalls.Load(); got != 2 {
				t.Fatalf("expected the origin to be called twice, got %d", got)
			}
		})
	}
}

func TestCacheTTLForResponseInvalidCacheControlAge(t *testing.T) {
	receivedAt := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	configuredTTL := 5 * time.Minute

	for _, cacheControl := range []string{
		"max-age=-1",
		"max-age=invalid",
		"max-age=1.5",
		"max-age=",
		"s-maxage=-1",
		"s-maxage=invalid",
		"s-maxage=bad, max-age=30",
		"max-age=bad, max-age=30",
	} {
		t.Run(cacheControl, func(t *testing.T) {
			header := http.Header{"Cache-Control": []string{cacheControl}}
			ttl, store := cacheTTLForResponse(header, configuredTTL, receivedAt)
			if store || ttl != 0 {
				t.Fatalf("cacheTTLForResponse() = (%v, %v), want (0, false)", ttl, store)
			}
		})
	}
}
