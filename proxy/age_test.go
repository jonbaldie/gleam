package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// backdateStoredEntries rewinds every stored entry's response time by d, so a
// cache hit is served as though it had been resident for that long.
func backdateStoredEntries(c *mockCache, d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, item := range c.store {
		item.StoredAt = item.StoredAt.Add(-d)
	}
}

// TestBugHuntCachedResponseOmitsAgeHeader reproduces jonbaldie/gleam#60: a
// cache hit replays the stored headers verbatim, so the response carries no
// Age header and appears to downstream caches to be as fresh as the origin's.
func TestBugHuntCachedResponseOmitsAgeHeader(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("body"))
	}))
	defer origin.Close()

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/resource", nil)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, req)
	if first.Code != http.StatusOK {
		t.Fatalf("expected first response status %d, got %d", http.StatusOK, first.Code)
	}
	if got := first.Header().Get("Age"); got != "" {
		t.Fatalf("expected forwarded origin response to carry no Age, got %q", got)
	}

	backdateStoredEntries(c, 30*time.Second)

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, req)
	if second.Code != http.StatusOK {
		t.Fatalf("expected cached response status %d, got %d", http.StatusOK, second.Code)
	}
	if got := second.Header().Get("Age"); got != "30" {
		t.Fatalf("expected cache hit to report an age of 30 seconds, got %q", got)
	}
}

func TestProxyCacheHitAgeHeader(t *testing.T) {
	tests := []struct {
		name         string
		originHeader http.Header
		resident     time.Duration
		wantAge      string
	}{
		{
			name:     "fresh hit reports zero",
			resident: 0,
			wantAge:  "0",
		},
		{
			name:     "resident time accumulates",
			resident: 45 * time.Second,
			wantAge:  "45",
		},
		{
			name:         "origin age is carried forward",
			originHeader: http.Header{"Age": []string{"100"}},
			resident:     20 * time.Second,
			wantAge:      "120",
		},
		{
			name:         "unparsable origin age is ignored",
			originHeader: http.Header{"Age": []string{"not-a-number"}},
			resident:     5 * time.Second,
			wantAge:      "5",
		},
		{
			name:         "negative origin age is ignored",
			originHeader: http.Header{"Age": []string{"-10"}},
			resident:     5 * time.Second,
			wantAge:      "5",
		},
		{
			// An origin Date older than the response itself makes the
			// response already aged on arrival; backdating the entry moves
			// the response time, so the age measured from Date stays 90.
			name:         "apparent age from Date is honoured",
			originHeader: http.Header{"Date": []string{time.Now().UTC().Add(-90 * time.Second).Format(http.TimeFormat)}},
			resident:     10 * time.Second,
			wantAge:      "90",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for key, values := range tc.originHeader {
					for _, value := range values {
						w.Header().Add(key, value)
					}
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("body"))
			}))
			defer origin.Close()

			c := newMockCache()
			handler := mustCachingProxyHandler(t, origin.URL, c, time.Hour)

			req := httptest.NewRequest(http.MethodGet, "/resource", nil)
			handler.ServeHTTP(httptest.NewRecorder(), req)

			backdateStoredEntries(c, tc.resident)

			hit := httptest.NewRecorder()
			handler.ServeHTTP(hit, req)
			if got := hit.Header().Get("Age"); got != tc.wantAge {
				t.Fatalf("expected Age %q, got %q", tc.wantAge, got)
			}
			if got := len(hit.Header().Values("Age")); got != 1 {
				t.Fatalf("expected exactly one Age header, got %d", got)
			}
		})
	}
}

// A 304 generated from a cache hit is still a reuse of the stored response,
// so it carries the same Age the full response would have (RFC 9111 § 4.2.3).
func TestProxyCachedNotModifiedCarriesAge(t *testing.T) {
	const etag = `"v1"`

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("body"))
	}))
	defer origin.Close()

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/resource", nil))

	backdateStoredEntries(c, 12*time.Second)

	conditional := httptest.NewRequest(http.MethodGet, "/resource", nil)
	conditional.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, conditional)

	if rec.Code != http.StatusNotModified {
		t.Fatalf("expected status %d, got %d", http.StatusNotModified, rec.Code)
	}
	if got := rec.Header().Get("Age"); got != "12" {
		t.Fatalf("expected 304 cache hit to report an age of 12 seconds, got %q", got)
	}
}
