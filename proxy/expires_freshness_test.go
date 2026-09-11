package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestBugHuntExpiresZeroServedFromCache reproduces jonbaldie/gleam#76:
// When an origin response carries Expires: 0, it explicitly indicates that the
// response is already expired. It must not be cached for the configured TTL.
func TestBugHuntExpiresZeroServedFromCache(t *testing.T) {
	var calls int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		w.Header().Set("Date", "Fri, 11 Sep 2026 04:00:00 GMT")
		w.Header().Set("Expires", "0")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "response-%d", n)
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

func TestCacheTTLForResponseExpiresZeroAndInvalid(t *testing.T) {
	receivedAt := time.Date(2026, 9, 11, 4, 0, 0, 0, time.UTC)
	configuredTTL := 5 * time.Minute

	tests := []struct {
		name      string
		header    http.Header
		wantStore bool
		wantTTL   time.Duration
	}{
		{
			name: "Expires: 0 with Date is already expired",
			header: http.Header{
				"Date":    {"Fri, 11 Sep 2026 04:00:00 GMT"},
				"Expires": {"0"},
			},
			wantStore: false,
			wantTTL:   0,
		},
		{
			name: "Expires: -1 with Date is already expired",
			header: http.Header{
				"Date":    {"Fri, 11 Sep 2026 04:00:00 GMT"},
				"Expires": {"-1"},
			},
			wantStore: false,
			wantTTL:   0,
		},
		{
			name: "Expires: invalid-date with Date is already expired",
			header: http.Header{
				"Date":    {"Fri, 11 Sep 2026 04:00:00 GMT"},
				"Expires": {"invalid-date"},
			},
			wantStore: false,
			wantTTL:   0,
		},
		{
			name: "Expires in past without Date is already expired",
			header: http.Header{
				"Expires": {"Thu, 01 Jan 1970 00:00:00 GMT"},
			},
			wantStore: false,
			wantTTL:   0,
		},
		{
			name: "Expires: 0 without Date is already expired",
			header: http.Header{
				"Expires": {"0"},
			},
			wantStore: false,
			wantTTL:   0,
		},
		{
			name: "empty Expires with Date is already expired",
			header: http.Header{
				"Date":    {"Fri, 11 Sep 2026 04:00:00 GMT"},
				"Expires": {""},
			},
			wantStore: false,
			wantTTL:   0,
		},
		{
			name: "Expires in future without Date uses receivedAt",
			header: http.Header{
				"Expires": {receivedAt.Add(45 * time.Second).Format(http.TimeFormat)},
			},
			wantStore: true,
			wantTTL:   45 * time.Second,
		},
		{
			name: "Expires in future with invalid Date uses receivedAt",
			header: http.Header{
				"Date":    {"invalid-date"},
				"Expires": {receivedAt.Add(30 * time.Second).Format(http.TimeFormat)},
			},
			wantStore: true,
			wantTTL:   30 * time.Second,
		},
		{
			name: "Expires in future with Date but arrived stale skips storage",
			header: http.Header{
				"Date":    {receivedAt.Add(-60 * time.Second).Format(http.TimeFormat)},
				"Expires": {receivedAt.Add(-10 * time.Second).Format(http.TimeFormat)},
			},
			wantStore: false,
			wantTTL:   0,
		},
		{
			name: "max-age takes precedence over Expires: 0",
			header: http.Header{
				"Cache-Control": {"max-age=60"},
				"Date":          {"Fri, 11 Sep 2026 04:00:00 GMT"},
				"Expires":       {"0"},
			},
			wantStore: true,
			wantTTL:   60 * time.Second,
		},
		{
			name: "s-maxage takes precedence over Expires: 0",
			header: http.Header{
				"Cache-Control": {"s-maxage=120"},
				"Date":          {"Fri, 11 Sep 2026 04:00:00 GMT"},
				"Expires":       {"0"},
			},
			wantStore: true,
			wantTTL:   120 * time.Second,
		},
		{
			name: "Expires in future with Date uses Date as reference time",
			header: http.Header{
				"Date":    {receivedAt.Add(-10 * time.Second).Format(http.TimeFormat)},
				"Expires": {receivedAt.Add(20 * time.Second).Format(http.TimeFormat)},
			},
			wantStore: true,
			wantTTL:   20 * time.Second,
		},
		{
			name: "no freshness directive keeps configured TTL",
			header: http.Header{
				"Date": {"Fri, 11 Sep 2026 04:00:00 GMT"},
			},
			wantStore: true,
			wantTTL:   configuredTTL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTTL, gotStore := cacheTTLForResponse(tt.header, configuredTTL, receivedAt)
			if gotStore != tt.wantStore {
				t.Fatalf("cacheTTLForResponse() store = %v, want %v", gotStore, tt.wantStore)
			}
			if gotTTL != tt.wantTTL {
				t.Fatalf("cacheTTLForResponse() ttl = %v, want %v", gotTTL, tt.wantTTL)
			}
		})
	}
}

func TestCacheTTLForResponseExpiresZeroReceivedAtZero(t *testing.T) {
	// When receivedAt is zero and Date is omitted, a past Expires still results in not storing.
	header := http.Header{
		"Expires": {"Thu, 01 Jan 1970 00:00:00 GMT"},
	}
	gotTTL, gotStore := cacheTTLForResponse(header, 5*time.Minute, time.Time{})
	if gotStore {
		t.Fatalf("expected store=false, got %v (ttl=%v)", gotStore, gotTTL)
	}
}
