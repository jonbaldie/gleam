//go:build fuzz

package main

import (
	"bytes"
	"net/http"
	"testing"
	"time"
)

// FuzzDecodeCacheItemWithMutations tests decoding of systematically mutated valid inputs
func FuzzDecodeCacheItemWithMutations(f *testing.F) {
	// Create some valid cache items and add their encodings
	testCases := []CacheItem{
		{
			content:    []byte(""),
			header:     http.Header{},
			status:     200,
			expiration: time.Unix(0, 0),
		},
		{
			content:    []byte("hello world"),
			header:     http.Header{"Content-Type": {"text/plain"}},
			status:     200,
			expiration: time.Now(),
		},
		{
			content: []byte("test data"),
			header: http.Header{
				"X-Custom-1": {"value1"},
				"X-Custom-2": {"value2", "value3"},
			},
			status:     404,
			expiration: time.Unix(1000000, 0),
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

		item := CacheItem{
			content:    []byte(content),
			header:     original,
			status:     int(status),
			expiration: time.Now().Truncate(time.Second),
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
		if !bytes.Equal(decoded.content, item.content) {
			t.Fatalf("content mismatch")
		}

		// Check status
		if decoded.status != item.status {
			t.Fatalf("status mismatch: got %d, want %d", decoded.status, item.status)
		}

		// Check headers
		for key, values := range original {
			decodedValues := decoded.header[key]
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
			item := CacheItem{
				content:    content,
				header:     http.Header{},
				status:     int(s),
				expiration: time.Now(),
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
			if dec.status != int(s) {
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

		item := CacheItem{
			content:    content,
			header:     http.Header{"Content-Length": {string(rune(len(content)))}},
			status:     200,
			expiration: time.Now(),
		}

		codec := &BinaryCodec{}
		enc, err := codec.Encode(item)
		if err != nil {
			return
		}

		dec, err := codec.Decode(enc)
		if err != nil {
			t.Fatalf("failed to decode large content: %v", err)
		}

		if !bytes.Equal(dec.content, content) {
			t.Fatalf("large content mismatch")
		}
	})
}
