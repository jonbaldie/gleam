package proxy

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestBugHuntIfModifiedSinceOnCacheHitIsIgnored reproduces jonbaldie/gleam#56:
// a cache hit evaluates If-None-Match but never If-Modified-Since, so a
// date-based conditional GET is answered with a full 200 and the origin's
// bandwidth saving is lost even though the proxy holds the representation.
func TestBugHuntIfModifiedSinceOnCacheHitIsIgnored(t *testing.T) {
	const lastModified = "Thu, 10 Sep 2026 03:58:45 GMT"

	var originCalls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		w.Header().Set("Last-Modified", lastModified)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("body"))
	}))
	defer origin.Close()

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/resource", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("expected first response status %d, got %d", http.StatusOK, first.Code)
	}

	conditional := httptest.NewRequest(http.MethodGet, "/resource", nil)
	conditional.Header.Set("If-Modified-Since", lastModified)
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, conditional)

	if second.Code != http.StatusNotModified {
		t.Fatalf("expected conditional cache hit status %d, got %d", http.StatusNotModified, second.Code)
	}
	if body := second.Body.String(); body != "" {
		t.Fatalf("expected empty 304 body, got %q", body)
	}
	if got := originCalls.Load(); got != 1 {
		t.Fatalf("expected one origin call after conditional cache hit, got %d", got)
	}
	if got := second.Header().Get("Last-Modified"); got != lastModified {
		t.Fatalf("expected 304 to keep cached Last-Modified, got %q", got)
	}
}

func TestProxyConditionalGETWithIfModifiedSince(t *testing.T) {
	const lastModified = "Thu, 10 Sep 2026 03:58:45 GMT"
	const earlier = "Thu, 01 Jan 2026 00:00:00 GMT"

	tests := []struct {
		name            string
		lastModified    string
		ifModifiedSince string
		ifNoneMatch     string
		etag            string
		wantStatus      int
		wantBody        string
	}{
		{
			name:            "equal to stored last-modified",
			lastModified:    lastModified,
			ifModifiedSince: lastModified,
			wantStatus:      http.StatusNotModified,
			wantBody:        "",
		},
		{
			name:            "later than stored last-modified",
			lastModified:    lastModified,
			ifModifiedSince: "Thu, 10 Sep 2026 12:00:00 GMT",
			wantStatus:      http.StatusNotModified,
			wantBody:        "",
		},
		{
			name:            "earlier than stored last-modified",
			lastModified:    lastModified,
			ifModifiedSince: earlier,
			wantStatus:      http.StatusOK,
			wantBody:        "body",
		},
		{
			name:            "no stored last-modified",
			ifModifiedSince: lastModified,
			wantStatus:      http.StatusOK,
			wantBody:        "body",
		},
		{
			name:            "unparseable value ignored",
			lastModified:    lastModified,
			ifModifiedSince: "not a date",
			wantStatus:      http.StatusOK,
			wantBody:        "body",
		},
		{
			name:            "rfc850 date format accepted",
			lastModified:    lastModified,
			ifModifiedSince: "Thursday, 10-Sep-26 03:58:45 GMT",
			wantStatus:      http.StatusNotModified,
			wantBody:        "",
		},
		{
			name:            "asctime date format accepted",
			lastModified:    lastModified,
			ifModifiedSince: "Thu Sep 10 03:58:45 2026",
			wantStatus:      http.StatusNotModified,
			wantBody:        "",
		},
		{
			name:            "if-none-match takes precedence over matching date",
			lastModified:    lastModified,
			ifModifiedSince: lastModified,
			ifNoneMatch:     `"version-1"`,
			etag:            `"version-1"`,
			wantStatus:      http.StatusNotModified,
			wantBody:        "",
		},
		{
			name:            "if-none-match ignored-if-present precedence fails the date too",
			lastModified:    lastModified,
			ifModifiedSince: lastModified,
			ifNoneMatch:     `"version-2"`,
			etag:            `"version-1"`,
			wantStatus:      http.StatusOK,
			wantBody:        "body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var originCalls atomic.Int32
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				originCalls.Add(1)
				if tt.lastModified != "" {
					w.Header().Set("Last-Modified", tt.lastModified)
				}
				if tt.etag != "" {
					w.Header().Set("ETag", tt.etag)
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("body"))
			}))
			defer origin.Close()

			handler := mustCachingProxyHandler(t, origin.URL, newMockCache(), time.Minute)
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/resource", nil))

			conditional := httptest.NewRequest(http.MethodGet, "/resource", nil)
			if tt.ifModifiedSince != "" {
				conditional.Header.Set("If-Modified-Since", tt.ifModifiedSince)
			}
			if tt.ifNoneMatch != "" {
				conditional.Header.Set("If-None-Match", tt.ifNoneMatch)
			}
			second := httptest.NewRecorder()
			handler.ServeHTTP(second, conditional)

			if second.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", second.Code, tt.wantStatus)
			}
			if body := second.Body.String(); body != tt.wantBody {
				t.Fatalf("body = %q, want %q", body, tt.wantBody)
			}
			if got := originCalls.Load(); got != 1 {
				t.Fatalf("origin calls = %d, want 1", got)
			}
		})
	}
}

func TestIfModifiedSinceSatisfied(t *testing.T) {
	const rfc1123 = "Thu, 10 Sep 2026 03:58:45 GMT"

	tests := []struct {
		name            string
		ifModifiedSince []string
		lastModified    string
		want            bool
	}{
		{name: "no condition", ifModifiedSince: nil, lastModified: rfc1123, want: false},
		{name: "no stored last-modified", ifModifiedSince: []string{rfc1123}, lastModified: "", want: false},
		{name: "equal dates", ifModifiedSince: []string{rfc1123}, lastModified: rfc1123, want: true},
		{name: "condition after last-modified", ifModifiedSince: []string{"Fri, 11 Sep 2026 00:00:00 GMT"}, lastModified: rfc1123, want: true},
		{name: "condition before last-modified", ifModifiedSince: []string{"Wed, 09 Sep 2026 00:00:00 GMT"}, lastModified: rfc1123, want: false},
		{name: "unparseable condition", ifModifiedSince: []string{"not a date"}, lastModified: rfc1123, want: false},
		{name: "unparseable last-modified", ifModifiedSince: []string{rfc1123}, lastModified: "yesterday", want: false},
		{name: "surrounding whitespace", ifModifiedSince: []string{"  " + rfc1123 + "  "}, lastModified: rfc1123, want: true},
		{name: "rfc850 format", ifModifiedSince: []string{"Thursday, 10-Sep-26 03:58:45 GMT"}, lastModified: rfc1123, want: true},
		{name: "asctime format", ifModifiedSince: []string{"Thu Sep 10 03:58:45 2026"}, lastModified: rfc1123, want: true},
		{name: "one unparseable then a valid value", ifModifiedSince: []string{"garbage", rfc1123}, lastModified: rfc1123, want: true},
		{name: "second header value valid", ifModifiedSince: []string{"garbage", rfc1123}, lastModified: rfc1123, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ifModifiedSinceSatisfied(tt.ifModifiedSince, tt.lastModified); got != tt.want {
				t.Fatalf("ifModifiedSinceSatisfied(%q, %q) = %v, want %v", tt.ifModifiedSince, tt.lastModified, got, tt.want)
			}
		})
	}
}