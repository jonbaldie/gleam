package proxy

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestBugHuntIfNoneMatchIgnoredForNon200CachedResponse reproduces
// jonbaldie/gleam#108: RFC 9110 section 15.4.5 defines 304 as standing in for
// a 200, so a matching If-None-Match against a cached non-200 representation
// must not be answered with 304; the cache serves the stored response in full.
func TestBugHuntIfNoneMatchIgnoredForNon200CachedResponse(t *testing.T) {
	const etag = `"v1"`

	tests := []struct {
		name         string
		status       int
		cacheControl string
		body         string
	}{
		{name: "203 non-authoritative information", status: http.StatusNonAuthoritativeInfo, body: "transformed-body"},
		{name: "204 no content", status: http.StatusNoContent},
		{name: "201 created with max-age", status: http.StatusCreated, cacheControl: "max-age=60", body: "created"},
		{name: "202 accepted with max-age", status: http.StatusAccepted, cacheControl: "max-age=60", body: "accepted"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var originCalls atomic.Int32
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				originCalls.Add(1)
				w.Header().Set("ETag", etag)
				if tt.cacheControl != "" {
					w.Header().Set("Cache-Control", tt.cacheControl)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer origin.Close()

			handler := mustCachingProxyHandler(t, origin.URL, newMockCache(), time.Minute)
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/resource", nil))

			conditional := httptest.NewRequest(http.MethodGet, "/resource", nil)
			conditional.Header.Set("If-None-Match", etag)
			second := httptest.NewRecorder()
			handler.ServeHTTP(second, conditional)

			if got := originCalls.Load(); got != 1 {
				t.Fatalf("expected conditional request to be a cache hit, got %d origin calls", got)
			}
			if second.Code != tt.status {
				t.Fatalf("expected If-None-Match not to yield 304 for cached %d, got status %d", tt.status, second.Code)
			}
			if body := second.Body.String(); body != tt.body {
				t.Fatalf("expected full cached body %q, got %q", tt.body, body)
			}
		})
	}
}
