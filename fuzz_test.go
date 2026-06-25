package main

import (
	"testing"
)

// FuzzParseOriginURL ensures origin parsing never panics on arbitrary strings.
func FuzzParseOriginURL(f *testing.F) {
	f.Add("https://httpbin.org")
	f.Add("")
	f.Add("://")
	f.Fuzz(func(t *testing.T, raw string) {
		_, _ = parseOriginURL(raw)
	})
}
