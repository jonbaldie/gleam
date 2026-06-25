package main

import (
	"net/http"
	"testing"
	"time"
)

func TestBinaryCodec_ImplementsCodec(t *testing.T) {
	var _ Codec = (*BinaryCodec)(nil)
}

func TestBinaryCodec_RoundTrip(t *testing.T) {
	codec := &BinaryCodec{}
	item := CacheItem{
		content:    []byte("test"),
		header:     http.Header{"X-Test": {"1"}},
		status:     200,
		expiration: time.Now().Truncate(time.Second),
	}

	data, err := codec.Encode(item)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	decoded, err := codec.Decode(data)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if string(decoded.content) != string(item.content) {
		t.Errorf("content mismatch")
	}
}
