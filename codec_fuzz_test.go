//go:build fuzz

package main

import (
	"jonbaldie/gleam/cache"
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
	codec := &BinaryCodec{}
	if enc, err := codec.Encode(cache.CacheItem{Content: []byte("hi"), Header: hdr, Status: 200, Expiration: time.Unix(0, 0)}); err == nil {
		f.Add(enc)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		codec := &BinaryCodec{}
		_, _ = codec.Decode(data)
	})
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
		in := cache.CacheItem{Content: []byte(body), Header: hdr, Status: status, Expiration: time.Now().Truncate(0)}
		codec := &BinaryCodec{}
		enc, err := codec.Encode(in)
		if err != nil {
			return
		}
		out, err := codec.Decode(enc)
		if err != nil {
			t.Fatalf("decode of freshly encoded item failed: %v", err)
		}
		if string(out.Content) != body || out.Status != status {
			t.Fatalf("round-trip mismatch")
		}
	})
}
