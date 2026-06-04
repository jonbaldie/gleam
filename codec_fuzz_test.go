package main

import (
	"net/http"
	"testing"
	"time"
)

func FuzzDecodeCacheItem(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("not-base64!!!"))
	// Length prefixes of 0xFFFFFFFF that drove multi-gigabyte allocations
	// before readCount bounded them against the remaining input.
	f.Add([]byte("/////w=="))
	f.Add([]byte("AAAAAMgAAAD/////"))
	f.Add([]byte("AAAAAMgAAAABAAAAAAAAAP////8="))
	hdr := http.Header{"Content-Type": {"text/plain"}}
	if enc, err := encodeCacheItem(CacheItem{content: []byte("hi"), header: hdr, status: 200, expiration: time.Unix(0, 0)}); err == nil {
		f.Add(enc)
	}
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = decodeCacheItem(data) })
}

func FuzzEncodeDecodeRoundTrip(f *testing.F) {
	f.Add("body", "Content-Type", "text/plain", 200)
	f.Fuzz(func(t *testing.T, body, hkey, hval string, status int) {
		if status < 0 {
			status = -status
		}
		status %= 1000
		hdr := http.Header{}
		if hkey != "" {
			hdr.Add(hkey, hval)
		}
		in := CacheItem{content: []byte(body), header: hdr, status: status, expiration: time.Now().Truncate(0)}
		enc, err := encodeCacheItem(in)
		if err != nil {
			return
		}
		out, err := decodeCacheItem(enc)
		if err != nil {
			t.Fatalf("decode of freshly encoded item failed: %v", err)
		}
		if string(out.content) != body || out.status != status {
			t.Fatalf("round-trip mismatch")
		}
	})
}
