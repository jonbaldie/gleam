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

func FuzzCacheKeyCollisionSearch(f *testing.F) {
	f.Add("A", "B\nC:D", "A", "B", "C", "D")
	f.Fuzz(func(t *testing.T, hname1, hval1, hname2, hval2, hname3, hval3 string) {
		r1 := newTestRequest("/", hname1, hval1)
		r2 := &http.Request{
			Method:     "GET",
			URL:        &url.URL{Path: "/"},
			Header:     http.Header{},
			RequestURI: "/",
		}
		if hname2 != "" {
			r2.Header.Add(hname2, hval2)
		}
		if hname3 != "" {
			r2.Header.Add(hname3, hval3)
		}

		if !headersEqual(r1.Header, r2.Header) {
			k1 := cacheKeyForRequest(r1)
			k2 := cacheKeyForRequest(r2)
			if k1 == k2 {
				t.Fatalf("Cache key collision found!\nReq1 Header: %v\nReq2 Header: %v\nBoth keys: %s", r1.Header, r2.Header, k1)
			}
		}
	})
}

func headersEqual(h1, h2 http.Header) bool {
	if len(h1) != len(h2) {
		return false
	}
	for k, v1 := range h1 {
		v2, ok := h2[k]
		if !ok || len(v1) != len(v2) {
			return false
		}
		for i := range v1 {
			if v1[i] != v2[i] {
				return false
			}
		}
	}
	return true
}
