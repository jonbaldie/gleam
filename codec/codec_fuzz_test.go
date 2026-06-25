//go:build fuzz

package codec

import (
	"bytes"
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

// FuzzDecodeCacheItemWithMutations tests decoding of systematically mutated valid inputs
func FuzzDecodeCacheItemWithMutations(f *testing.F) {
	// Create some valid cache items and add their encodings
	testCases := []cache.CacheItem{
		{
			Content:    []byte(""),
			Header:     http.Header{},
			Status:     200,
			Expiration: time.Unix(0, 0),
		},
		{
			Content:    []byte("hello world"),
			Header:     http.Header{"Content-Type": {"text/plain"}},
			Status:     200,
			Expiration: time.Now(),
		},
		{
			Content: []byte("test data"),
			Header: http.Header{
				"X-Custom-1": {"value1"},
				"X-Custom-2": {"value2", "value3"},
			},
			Status:     404,
			Expiration: time.Unix(1000000, 0),
		},
	}

	codec := &BinaryCodec{}
	for _, tc := range testCases {
		if enc, err := codec.Encode(tc); err == nil {
			f.Add(enc)
		}
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		codec := &BinaryCodec{}
		item, err := codec.Decode(data)
		if err != nil {
			// Decoding errors are acceptable for malformed input
			return
		}
		// If decode succeeds, the item should be valid
		if item == nil {
			t.Fatal("nil item returned without error")
		}
	})
}

// FuzzHeaderRoundTrip ensures headers survive encode/decode cycles exactly
func FuzzHeaderRoundTrip(f *testing.F) {
	f.Add("", "", "", int32(200))
	f.Add("Content-Type", "application/json", "hello", int32(200))
	f.Add("X-Custom", "a,b,c", "test", int32(404))
	f.Add("Authorization", "Bearer token123", "data", int32(500))

	f.Fuzz(func(t *testing.T, hkey, hval, content string, status int32) {
		if status < 0 || status > 999 {
			return
		}

		original := http.Header{}
		if hkey != "" {
			original.Add(hkey, hval)
		}

		item := cache.CacheItem{
			Content:    []byte(content),
			Header:     original,
			Status:     int(status),
			Expiration: time.Now().Truncate(time.Second),
		}

		codec := &BinaryCodec{}
		enc, err := codec.Encode(item)
		if err != nil {
			return
		}

		decoded, err := codec.Decode(enc)
		if err != nil {
			t.Fatalf("failed to decode valid item: %v", err)
		}

		// Check content
		if !bytes.Equal(decoded.Content, item.Content) {
			t.Fatalf("content mismatch")
		}

		// Check status
		if decoded.Status != item.Status {
			t.Fatalf("status mismatch: got %d, want %d", decoded.Status, item.Status)
		}

		// Check headers
		for key, values := range original {
			decodedValues := decoded.Header[key]
			if len(decodedValues) != len(values) {
				t.Fatalf("header value count mismatch for %q", key)
			}
			for i, v := range values {
				if decodedValues[i] != v {
					t.Fatalf("header value mismatch for %q[%d]: got %q, want %q", key, i, decodedValues[i], v)
				}
			}
		}
	})
}

// FuzzStatusCodesExhaustive tests all valid HTTP status codes
func FuzzStatusCodesExhaustive(f *testing.F) {
	f.Add([]byte("test"), int32(100), int32(200), int32(300), int32(400), int32(500))
	f.Fuzz(func(t *testing.T, content []byte, status1, status2, status3, status4, status5 int32) {
		statuses := []int32{status1, status2, status3, status4, status5}
		for _, s := range statuses {
			if s < 0 || s > 999 {
				return
			}
			item := cache.CacheItem{
				Content:    content,
				Header:     http.Header{},
				Status:     int(s),
				Expiration: time.Now(),
			}
			codec := &BinaryCodec{}
			enc, err := codec.Encode(item)
			if err != nil {
				return
			}
			dec, err := codec.Decode(enc)
			if err != nil {
				t.Fatalf("failed to decode status %d", s)
			}
			if dec.Status != int(s) {
				t.Fatalf("status mismatch for %d", s)
			}
		}
	})
}

// FuzzLargeContent tests encoding/decoding of large payloads
func FuzzLargeContent(f *testing.F) {
	f.Add([]byte(""), int32(0))
	f.Add([]byte("small"), int32(5))
	f.Add(bytes.Repeat([]byte("x"), 1000), int32(1000))

	f.Fuzz(func(t *testing.T, content []byte, padding int32) {
		// Ensure we don't allocate unreasonably large content for fuzzing
		if len(content) > 10000 {
			return
		}

		item := cache.CacheItem{
			Content:    content,
			Header:     http.Header{"Content-Length": {string(rune(len(content)))}},
			Status:     200,
			Expiration: time.Now(),
		}

		codec := &BinaryCodec{}
		enc, err := codec.Encode(item)
		if err != nil {
			return
		}

		dec, err := codec.Decode(enc)
		if err != nil {
			t.Fatalf("failed to decode large Content: %v", err)
		}

		if !bytes.Equal(dec.Content, content) {
			t.Fatalf("large content mismatch")
		}
	})
}
