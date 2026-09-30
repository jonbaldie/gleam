package proxy

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestEmptyBodyForbiddenDirectivesNotCached reproduces jonbaldie/gleam#121:
// Empty-body 200 responses from upstream http.Handler (handlers that return
// without explicitly invoking WriteHeader or Write) must not bypass Cache-Control
// or Set-Cookie storage checks in a shared cache.
func TestEmptyBodyForbiddenDirectivesNotCached(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header http.Header
	}{
		{
			name:   "no-store",
			header: http.Header{"Cache-Control": []string{"no-store"}},
		},
		{
			name:   "private",
			header: http.Header{"Cache-Control": []string{"private"}},
		},
		{
			name:   "no-cache",
			header: http.Header{"Cache-Control": []string{"no-cache"}},
		},
		{
			name:   "set-cookie",
			header: http.Header{"Set-Cookie": []string{"session=secret; Path=/"}},
		},
		{
			name:   "unmatched vary",
			header: http.Header{"Vary": []string{"User-Agent"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var originCalls atomic.Int32
			upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				originCalls.Add(1)
				for k, values := range tc.header {
					for _, v := range values {
						w.Header().Add(k, v)
					}
				}
				// returns without calling WriteHeader or Write
			})

			c := newMockCache()
			handler := NewHandler(upstream, c, time.Minute, []string{"Accept-Encoding"})

			first := httptest.NewRecorder()
			handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/empty-forbidden", nil))

			second := httptest.NewRecorder()
			handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/empty-forbidden", nil))

			if got := originCalls.Load(); got != 2 {
				t.Fatalf("expected 2 origin calls for %s response, got %d (response was served from cache!)", tc.name, got)
			}
		})
	}
}

// TestEmptyBodyHeadersPreservedOnCacheHit reproduces jonbaldie/gleam#121:
// Cacheable empty-body responses from upstream http.Handler must preserve all
// response headers when served from the cache.
func TestEmptyBodyHeadersPreservedOnCacheHit(t *testing.T) {
	var originCalls atomic.Int32
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"empty-hash"`)
		w.Header().Set("X-Custom-Header", "custom-value")
		// returns without calling WriteHeader or Write
	})

	c := newMockCache()
	handler := NewHandler(upstream, c, time.Minute, nil)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/empty-headers", nil))

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/empty-headers", nil))

	if got := originCalls.Load(); got != 1 {
		t.Fatalf("expected 1 origin call (cache hit on second request), got %d", got)
	}

	if got := second.Code; got != http.StatusOK {
		t.Fatalf("expected status 200 on cache hit, got %d", got)
	}

	if got := second.Header().Get("X-Custom-Header"); got != "custom-value" {
		t.Fatalf("expected cached response to preserve custom header, got %q (headers were dropped!)", got)
	}

	if got := second.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected cached response to preserve Content-Type, got %q", got)
	}

	if got := second.Header().Get("ETag"); got != `"empty-hash"` {
		t.Fatalf("expected cached response to preserve ETag, got %q", got)
	}
}

// TestEmptyBodyNoHeadersIsCached ensures empty-body 200 responses with no headers
// are heuristically cached and served on cache hit.
func TestEmptyBodyNoHeadersIsCached(t *testing.T) {
	var originCalls atomic.Int32
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		// returns with no headers, no WriteHeader, no Write
	})
	c := newMockCache()
	handler := NewHandler(upstream, c, time.Minute, nil)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/empty-no-headers", nil))

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/empty-no-headers", nil))

	if got := originCalls.Load(); got != 1 {
		t.Fatalf("expected 1 origin call (cache hit on second request), got %d", got)
	}
	if got := second.Code; got != http.StatusOK {
		t.Fatalf("expected status 200, got %d", got)
	}
}

// TestHeadersModifiedAfterWriteHeaderNotStored ensures headers modified after
// WriteHeader was explicitly invoked are not captured on upstream completion.
func TestHeadersModifiedAfterWriteHeaderNotStored(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Before", "stored")
		w.WriteHeader(http.StatusOK)
		w.Header().Set("X-After", "not-stored")
		_, _ = w.Write([]byte("body"))
	})
	c := newMockCache()
	handler := NewHandler(upstream, c, time.Minute, nil)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/after-write-header", nil))

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/after-write-header", nil))

	if got := second.Header().Get("X-After"); got != "" {
		t.Fatalf("expected header set after WriteHeader to not be cached, got %q", got)
	}
	if got := second.Header().Get("X-Before"); got != "stored" {
		t.Fatalf("expected header set before WriteHeader to be cached, got %q", got)
	}
}

// TestEmptyBodyTrailersPreserved ensures announced trailers on empty-body responses
// are properly captured and served on cache hits.
func TestEmptyBodyTrailersPreserved(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Trailer", "X-Checksum")
		w.Header().Set(http.TrailerPrefix+"X-Checksum", "abc123")
		// returns without WriteHeader or Write
	})
	c := newMockCache()
	handler := NewHandler(upstream, c, time.Minute, nil)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/empty-trailers", nil))

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/empty-trailers", nil))

	if got := second.Header().Get("X-Checksum"); got != "abc123" {
		t.Fatalf("expected trailer X-Checksum preserved on cache hit, got %q", got)
	}
}
