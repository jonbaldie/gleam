package main

import (
	"net/http"
	"net/url"
	"testing"
)

// FuzzCacheKeyCollisions validates cache key generation is deterministic
// and consistent (no collisions for equivalent requests).
func FuzzCacheKeyCollisions(f *testing.F) {
	f.Add("/", "Authorization", "Bearer token", "/", "Authorization", "Bearer token")
	f.Add("/a", "X-Custom", "v1", "/a", "X-Custom", "v1")
	f.Add("/path", "Header", "value1", "/path", "Header", "value1")
	f.Fuzz(func(t *testing.T, path1, key1, val1, path2, key2, val2 string) {
		r1 := newTestRequest(path1, key1, val1)
		r2 := newTestRequest(path2, key2, val2)

		k1 := cacheKeyForRequest(r1)
		k2 := cacheKeyForRequest(r2)

		// Identical request parameters must produce identical keys
		if path1 == path2 && key1 == key2 && val1 == val2 {
			if k1 != k2 {
				t.Fatalf("identical requests produced different keys")
			}
		}

		// Keys must be deterministic: calling twice produces same result
		k1again := cacheKeyForRequest(r1)
		if k1 != k1again {
			t.Fatalf("cache key generation is non-deterministic")
		}
	})
}

func newTestRequest(path, hkey, hval string) *http.Request {
	r := &http.Request{
		Method:     "GET",
		URL:        &url.URL{Path: path},
		Header:     http.Header{},
		RequestURI: path,
	}
	if hkey != "" {
		r.Header.Add(hkey, hval)
	}
	return r
}
