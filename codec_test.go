package main

import (
	"jonbaldie/gleam/cache"
	"net/http"
	"testing"
	"time"
)

func TestBinaryCodec_ImplementsCodec(t *testing.T) {
	var _ Codec = (*BinaryCodec)(nil)
}

func TestBinaryCodec_RoundTrip(t *testing.T) {
	codec := &BinaryCodec{}
	item := cache.CacheItem{
		Content:    []byte("test"),
		Header:     http.Header{"X-Test": {"1"}},
		Status:     200,
		Expiration: time.Now().Truncate(time.Second),
	}

	data, err := codec.Encode(item)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	decoded, err := codec.Decode(data)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if string(decoded.Content) != string(item.Content) {
		t.Errorf("content mismatch")
	}
}
