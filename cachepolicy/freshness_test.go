package cachepolicy_test

import (
	"net/http"
	"testing"
	"time"

	"jonbaldie/gleam/cachepolicy"
)

func TestFreshnessLifetime(t *testing.T) {
	const configuredTTL = time.Minute
	// Every case's Date, where present, is the moment the response arrived, so
	// no origin-side age is consumed before storage.
	receivedAt := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		header    http.Header
		wantTTL   time.Duration
		wantStore bool
	}{
		{
			name:      "no explicit freshness keeps configured ttl",
			header:    http.Header{"Date": []string{"Thu, 10 Sep 2026 00:00:00 GMT"}},
			wantTTL:   configuredTTL,
			wantStore: true,
		},
		{
			name:      "max-age zero skips storage",
			header:    http.Header{"Cache-Control": []string{"max-age=0"}},
			wantTTL:   0,
			wantStore: false,
		},
		{
			name:      "max-age below configured ttl caps storage",
			header:    http.Header{"Cache-Control": []string{"max-age=30"}},
			wantTTL:   30 * time.Second,
			wantStore: true,
		},
		{
			name:      "max-age above configured ttl keeps configured ttl",
			header:    http.Header{"Cache-Control": []string{"max-age=120"}},
			wantTTL:   configuredTTL,
			wantStore: true,
		},
		{
			name:      "s-maxage takes precedence over max-age",
			header:    http.Header{"Cache-Control": []string{"max-age=60, s-maxage=0"}},
			wantTTL:   0,
			wantStore: false,
		},
		{
			name: "expires minus date provides fallback freshness",
			header: http.Header{
				"Date":    []string{"Thu, 10 Sep 2026 00:00:00 GMT"},
				"Expires": []string{"Thu, 10 Sep 2026 00:00:45 GMT"},
			},
			wantTTL:   45 * time.Second,
			wantStore: true,
		},
		{
			name: "expires at date skips storage",
			header: http.Header{
				"Date":    []string{"Thu, 10 Sep 2026 00:00:00 GMT"},
				"Expires": []string{"Thu, 10 Sep 2026 00:00:00 GMT"},
			},
			wantTTL:   0,
			wantStore: false,
		},
		{
			name:      "malformed max-age skips storage",
			header:    http.Header{"Cache-Control": []string{"public, max-age=not-a-number"}},
			wantTTL:   0,
			wantStore: false,
		},
		{
			name:      "bare max-age skips storage",
			header:    http.Header{"Cache-Control": []string{"public, max-age"}},
			wantTTL:   0,
			wantStore: false,
		},
		{
			name:      "bare s-maxage skips storage",
			header:    http.Header{"Cache-Control": []string{"public, s-maxage"}},
			wantTTL:   0,
			wantStore: false,
		},
		{
			name:      "bare s-maxage takes precedence over max-age",
			header:    http.Header{"Cache-Control": []string{"s-maxage, max-age=30"}},
			wantTTL:   0,
			wantStore: false,
		},
		{
			name:      "malformed s-maxage skips storage despite max-age",
			header:    http.Header{"Cache-Control": []string{"s-maxage=bad, max-age=30"}},
			wantTTL:   0,
			wantStore: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTTL, gotStore := cachepolicy.FreshnessLifetime(tt.header, receivedAt, configuredTTL)
			if gotTTL != tt.wantTTL {
				t.Errorf("FreshnessLifetime() ttl = %v, want %v", gotTTL, tt.wantTTL)
			}
			if gotStore != tt.wantStore {
				t.Errorf("FreshnessLifetime() store = %v, want %v", gotStore, tt.wantStore)
			}
		})
	}
}

func TestFreshnessLifetimeExpiresZeroAndInvalid(t *testing.T) {
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
			name: "multiple Expires fields with Date is already expired",
			header: http.Header{
				"Date": {"Fri, 11 Sep 2026 04:00:00 GMT"},
				"Expires": {
					"Fri, 11 Sep 2026 05:00:00 GMT",
					"Fri, 11 Sep 2026 06:00:00 GMT",
				},
			},
			wantStore: false,
			wantTTL:   0,
		},
		{
			name: "multiple Expires values in one field with Date is already expired",
			header: http.Header{
				"Date":    {"Fri, 11 Sep 2026 04:00:00 GMT"},
				"Expires": {"Fri, 11 Sep 2026 05:00:00 GMT, Fri, 11 Sep 2026 06:00:00 GMT"},
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
			gotTTL, gotStore := cachepolicy.FreshnessLifetime(tt.header, receivedAt, configuredTTL)
			if gotStore != tt.wantStore {
				t.Fatalf("FreshnessLifetime() store = %v, want %v", gotStore, tt.wantStore)
			}
			if gotTTL != tt.wantTTL {
				t.Fatalf("FreshnessLifetime() ttl = %v, want %v", gotTTL, tt.wantTTL)
			}
		})
	}
}

func TestFreshnessLifetimeExpiresZeroReceivedAtZero(t *testing.T) {
	// When receivedAt is zero and Date is omitted, a past Expires still results in not storing.
	header := http.Header{
		"Expires": {"Thu, 01 Jan 1970 00:00:00 GMT"},
	}
	gotTTL, gotStore := cachepolicy.FreshnessLifetime(header, time.Time{}, 5*time.Minute)
	if gotStore {
		t.Fatalf("expected store=false, got %v (ttl=%v)", gotStore, gotTTL)
	}
}

func TestFreshnessLifetimeInvalidCacheControlAge(t *testing.T) {
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
			ttl, store := cachepolicy.FreshnessLifetime(header, receivedAt, configuredTTL)
			if store || ttl != 0 {
				t.Fatalf("FreshnessLifetime() = (%v, %v), want (0, false)", ttl, store)
			}
		})
	}
}

func TestFreshnessLifetimeAccountsForOriginAge(t *testing.T) {
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
			name:          "Date after receipt adds no apparent age",
			header:        http.Header{"Cache-Control": {"max-age=30"}, "Date": {receivedAt.Add(time.Hour).Format(http.TimeFormat)}},
			configuredTTL: time.Minute,
			wantTTL:       30 * time.Second,
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
			name:          "no freshness directive but origin age exceeds the configured TTL is not stored",
			header:        http.Header{"Age": {"120"}},
			configuredTTL: 30 * time.Second,
			wantStore:     false,
		},
		{
			name:          "no freshness directive and origin age within the configured TTL shortens it",
			header:        http.Header{"Age": {"45"}},
			configuredTTL: time.Minute,
			wantTTL:       15 * time.Second,
			wantStore:     true,
		},
		{
			name:          "no freshness directive and no origin age keeps the configured TTL",
			header:        http.Header{},
			configuredTTL: 30 * time.Second,
			wantTTL:       30 * time.Second,
			wantStore:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ttl, store := cachepolicy.FreshnessLifetime(tt.header, receivedAt, tt.configuredTTL)
			if store != tt.wantStore {
				t.Fatalf("expected shouldStore %v, got %v", tt.wantStore, store)
			}
			if store && ttl != tt.wantTTL {
				t.Fatalf("expected TTL %v, got %v", tt.wantTTL, ttl)
			}
		})
	}
}
