package main

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// FuzzEncodeCacheItem directly fuzzes the encoding function with various cache items
func FuzzEncodeCacheItem(f *testing.F) {
	f.Add("", "", "", int32(200))
	f.Add("hello", "Content-Type", "text/plain", int32(200))
	f.Add("x", "X-Custom", "", int32(404))
	f.Fuzz(func(t *testing.T, body, hkey, hval string, status int32) {
		if status < 0 || status > 999 {
			return
		}
		hdr := http.Header{}
		if hkey != "" {
			hdr.Add(hkey, hval)
		}
		item := CacheItem{
			content:    []byte(body),
			header:     hdr,
			status:     int(status),
			expiration: time.Now().Add(time.Hour),
		}
		enc, err := encodeCacheItem(item)
		if err != nil {
			return // encoding failures are allowed
		}
		// Any valid encoding should decode without panic
		_, _ = decodeCacheItem(enc)
	})
}

// FuzzCacheItemWithBinaryContent fuzzes with binary content that might include null bytes
func FuzzCacheItemWithBinaryContent(f *testing.F) {
	f.Add([]byte(""), []byte(""), []byte(""), int32(200))
	f.Add([]byte("\x00\x01\x02"), []byte("X-Bin"), []byte("\x00data"), int32(200))
	f.Fuzz(func(t *testing.T, content, hkey, hval []byte, status int32) {
		if status < 0 || status > 999 {
			return
		}
		hdr := http.Header{}
		if len(hkey) > 0 {
			hdr.Add(string(hkey), string(hval))
		}
		item := CacheItem{
			content:    content,
			header:     hdr,
			status:     int(status),
			expiration: time.Now().Truncate(0),
		}
		enc, err := encodeCacheItem(item)
		if err != nil {
			return
		}
		dec, err := decodeCacheItem(enc)
		if err != nil {
			return
		}
		// Verify round-trip for content and status
		if !bytes.Equal(dec.content, content) || dec.status != int(status) {
			t.Fatalf("round-trip mismatch: content %v -> %v, status %d -> %d", content, dec.content, status, dec.status)
		}
	})
}

// FuzzCacheItemWithVariedStatus fuzzes encoding with different status codes
func FuzzCacheItemWithVariedStatus(f *testing.F) {
	f.Add([]byte("data"), []byte("ContentType"), []byte("text/plain"), int32(200))
	f.Add([]byte("x"), []byte("X-Custom"), []byte("value"), int32(404))
	f.Add([]byte(""), []byte(""), []byte(""), int32(500))
	f.Fuzz(func(t *testing.T, content, hkey, hval []byte, status int32) {
		if status < 0 || status > 999 {
			return
		}
		hdr := http.Header{}
		if len(hkey) > 0 {
			hdr.Add(string(hkey), string(hval))
		}
		item := CacheItem{
			content:    content,
			header:     hdr,
			status:     int(status),
			expiration: time.Now().Truncate(0),
		}
		enc, err := encodeCacheItem(item)
		if err != nil {
			return
		}
		dec, err := decodeCacheItem(enc)
		if err != nil {
			return
		}
		if !bytes.Equal(dec.content, content) || dec.status != int(status) {
			t.Fatalf("round-trip mismatch")
		}
	})
}

// FuzzCacheKeyCollisions looks for hash collisions or incorrect key generation
func FuzzCacheKeyCollisions(f *testing.F) {
	f.Add("/", "Authorization", "Bearer token", "/", "Authorization", "Bearer token")
	f.Add("/a", "X-Custom", "v1", "/a", "X-Custom", "v1")
	f.Fuzz(func(t *testing.T, path1, key1, val1, path2, key2, val2 string) {
		r1 := newTestRequest(path1, key1, val1)
		r2 := newTestRequest(path2, key2, val2)

		k1 := cacheKeyForRequest(r1)
		k2 := cacheKeyForRequest(r2)

		// Same request parameters should give same key
		if path1 == path2 && key1 == key2 && val1 == val2 {
			if k1 != k2 {
				t.Fatalf("same requests gave different cache keys")
			}
		}

		// Keys should be deterministic (call twice, get same result)
		k1again := cacheKeyForRequest(r1)
		if k1 != k1again {
			t.Fatalf("cache key not deterministic")
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
