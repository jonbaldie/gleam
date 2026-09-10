package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestBugHuntOriginAgeIgnoredForFreshness reproduces jonbaldie/gleam#66: a
// response that arrives already at the end of its freshness lifetime — max-age
// fully consumed by the origin's Age — must not be reused without validation.
func TestBugHuntOriginAgeIgnoredForFreshness(t *testing.T) {
	var calls int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		w.Header().Set("Cache-Control", "max-age=60")
		w.Header().Set("Age", "60")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fmt.Sprintf("response-%d", n)))
	}))
	defer origin.Close()

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

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

func TestCacheTTLForResponseAccountsForOriginAge(t *testing.T) {
	receivedAt := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name          string
		header        http.Header
		configuredTTL time.Duration
		wantTTL       time.Duration
		wantStore     bool
	}{
		{
			name:          "origin age shortens the remaining freshness",
			header:        http.Header{"Cache-Control": {"max-age=60"}, "Age": {"45"}},
			configuredTTL: time.Minute,
			wantTTL:       15 * time.Second,
			wantStore:     true,
		},
		{
			name:          "origin age exhausts the freshness lifetime",
			header:        http.Header{"Cache-Control": {"max-age=60"}, "Age": {"60"}},
			configuredTTL: time.Minute,
			wantStore:     false,
		},
		{
			name:          "apparent age from Date shortens the remaining freshness",
			header:        http.Header{"Cache-Control": {"max-age=60"}, "Date": {receivedAt.Add(-50 * time.Second).Format(http.TimeFormat)}},
			configuredTTL: time.Minute,
			wantTTL:       10 * time.Second,
			wantStore:     true,
		},
		{
			name:          "unparsable Age is ignored",
			header:        http.Header{"Cache-Control": {"max-age=60"}, "Age": {"soon"}},
			configuredTTL: 10 * time.Second,
			wantTTL:       10 * time.Second,
			wantStore:     true,
		},
		{
			name:          "no freshness directive keeps the configured TTL",
			header:        http.Header{"Age": {"120"}},
			configuredTTL: 30 * time.Second,
			wantTTL:       30 * time.Second,
			wantStore:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ttl, store := cacheTTLForResponse(tt.header, tt.configuredTTL, receivedAt)
			if store != tt.wantStore {
				t.Fatalf("expected shouldStore %v, got %v", tt.wantStore, store)
			}
			if store && ttl != tt.wantTTL {
				t.Fatalf("expected TTL %v, got %v", tt.wantTTL, ttl)
			}
		})
	}
}
