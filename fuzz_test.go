package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// FuzzCacheKeyForRequest ensures key generation never panics and exercises the
// header-signature path with arbitrary request metadata.
func FuzzCacheKeyForRequest(f *testing.F) {
	f.Add("/path", "Authorization", "Bearer x")
	f.Add("/", "", "")

	f.Fuzz(func(t *testing.T, path, hkey, hval string) {
		r := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
		r.URL.Path = path
		if hkey != "" {
			r.Header.Add(hkey, hval)
		}
		_ = cacheKeyForRequest(r)
	})
}

// FuzzParseOriginURL ensures origin parsing never panics on arbitrary strings.
func FuzzParseOriginURL(f *testing.F) {
	f.Add("https://httpbin.org")
	f.Add("")
	f.Add("://")
	f.Fuzz(func(t *testing.T, raw string) {
		_, _ = parseOriginURL(raw)
	})
}
