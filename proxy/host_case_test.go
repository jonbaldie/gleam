package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

// TestProxyHostHeaderCaseSharing reproduces issue #82:
// Under RFC 9110 section 4.2.3, the host component of URI authority is
// case-insensitive and must be normalized to lowercase. Requests for the same
// URI with differing Host header casing must produce identical cache keys and
// share cached responses.
func TestProxyHostHeaderCaseSharing(t *testing.T) {
	var originCalls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := originCalls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "payload-%d", call)
	}))
	defer origin.Close()

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

	testCases := []string{
		"example.com:8080",
		"EXAMPLE.COM:8080",
		"Example.Com:8080",
		"ExAmPlE.cOm:8080",
	}

	for i, host := range testCases {
		req := httptest.NewRequest(http.MethodGet, "/resource", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("case %d (%s): expected status 200, got %d", i, host, rec.Code)
		}
		if body := rec.Body.String(); body != "payload-1" {
			t.Fatalf("case %d (%s): expected cached payload-1, got %q", i, host, body)
		}
	}

	if calls := originCalls.Load(); calls != 1 {
		t.Fatalf("expected exactly 1 origin call across all host casings, got %d", calls)
	}
}

// TestProxyHostHeaderCaseInvalidation reproduces issue #82:
// When an unsafe request is sent with one Host casing, it must invalidate
// cached responses stored under any Host casing for that target URI
// (RFC 9111 section 4.4).
func TestProxyHostHeaderCaseInvalidation(t *testing.T) {
	tests := []struct {
		name      string
		firstHost string
		writeHost string
		readHost  string
		method    string
	}{
		{
			name:      "GET lowercase, POST uppercase, GET lowercase",
			firstHost: "localhost:8080",
			writeHost: "LOCALHOST:8080",
			readHost:  "localhost:8080",
			method:    http.MethodPost,
		},
		{
			name:      "GET uppercase, POST lowercase, GET uppercase",
			firstHost: "LOCALHOST:8080",
			writeHost: "localhost:8080",
			readHost:  "LOCALHOST:8080",
			method:    http.MethodPost,
		},
		{
			name:      "GET mixed-case, DELETE uppercase, GET lowercase",
			firstHost: "LocalHost:8080",
			writeHost: "LOCALHOST:8080",
			readHost:  "localhost:8080",
			method:    http.MethodDelete,
		},
		{
			name:      "GET uppercase, PUT mixed-case, GET mixed-case",
			firstHost: "LOCALHOST:8080",
			writeHost: "LocalHost:8080",
			readHost:  "LocalHost:8080",
			method:    http.MethodPut,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var getCalls atomic.Int32
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					call := getCalls.Add(1)
					_, _ = fmt.Fprintf(w, "version-%d", call)
				case tt.method:
					w.WriteHeader(http.StatusOK)
				default:
					t.Errorf("unexpected method: %s", r.Method)
				}
			}))
			defer origin.Close()

			c := newMockCache()
			handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

			// Prime the cache
			req1 := httptest.NewRequest(http.MethodGet, "/resource", nil)
			req1.Host = tt.firstHost
			rec1 := httptest.NewRecorder()
			handler.ServeHTTP(rec1, req1)
			if body := rec1.Body.String(); body != "version-1" {
				t.Fatalf("prime: expected body version-1, got %q", body)
			}

			// Perform unsafe write to invalidate
			writeReq := httptest.NewRequest(tt.method, "/resource", nil)
			writeReq.Host = tt.writeHost
			writeRec := httptest.NewRecorder()
			handler.ServeHTTP(writeRec, writeReq)
			if writeRec.Code != http.StatusOK {
				t.Fatalf("write: expected status 200, got %d", writeRec.Code)
			}

			// Subsequent GET must reach the origin and return fresh version-2
			req2 := httptest.NewRequest(http.MethodGet, "/resource", nil)
			req2.Host = tt.readHost
			rec2 := httptest.NewRecorder()
			handler.ServeHTTP(rec2, req2)
			if body := rec2.Body.String(); body != "version-2" {
				t.Fatalf("read after write: expected fresh version-2 from origin, got %q", body)
			}
			if calls := getCalls.Load(); calls != 2 {
				t.Fatalf("expected 2 origin GET calls after invalidation, got %d", calls)
			}
		})
	}
}

// TestCacheBaseKeyNormalizesHostCase verifies that cacheBaseKey lowercases
// the host component while keeping path and query case-sensitive.
func TestCacheBaseKeyNormalizesHostCase(t *testing.T) {
	tests := []struct {
		name     string
		req1     *http.Request
		req2     *http.Request
		wantSame bool
	}{
		{
			name: "host difference in case produces same key",
			req1: &http.Request{Host: "EXAMPLE.COM", URL: &url.URL{Path: "/resource"}},
			req2: &http.Request{Host: "example.com", URL: &url.URL{Path: "/resource"}},
			wantSame: true,
		},
		{
			name: "host with port difference in case produces same key",
			req1: &http.Request{Host: "EXAMPLE.COM:8080", URL: &url.URL{Path: "/resource"}},
			req2: &http.Request{Host: "example.com:8080", URL: &url.URL{Path: "/resource"}},
			wantSame: true,
		},
		{
			name: "ipv6 host difference in hex case produces same key",
			req1: &http.Request{Host: "[2001:DB8::1]:8080", URL: &url.URL{Path: "/resource"}},
			req2: &http.Request{Host: "[2001:db8::1]:8080", URL: &url.URL{Path: "/resource"}},
			wantSame: true,
		},
		{
			name: "path case difference produces different key",
			req1: &http.Request{Host: "example.com", URL: &url.URL{Path: "/RESOURCE"}},
			req2: &http.Request{Host: "example.com", URL: &url.URL{Path: "/resource"}},
			wantSame: false,
		},
		{
			name: "query case difference produces different key",
			req1: &http.Request{Host: "example.com", URL: &url.URL{Path: "/resource", RawQuery: "PARAM=1"}},
			req2: &http.Request{Host: "example.com", URL: &url.URL{Path: "/resource", RawQuery: "param=1"}},
			wantSame: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k1 := cacheBaseKey(tt.req1)
			k2 := cacheBaseKey(tt.req2)
			if tt.wantSame && k1 != k2 {
				t.Fatalf("expected same cache key, got %q and %q", k1, k2)
			}
			if !tt.wantSame && k1 == k2 {
				t.Fatalf("expected different cache keys, got %q for both", k1)
			}
		})
	}
}
