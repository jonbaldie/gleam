package codec

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
	if decoded.Status != item.Status {
		t.Errorf("status mismatch: got %v, want %v", decoded.Status, item.Status)
	}
	if len(decoded.Header) != len(item.Header) || decoded.Header.Get("X-Test") != "1" {
		t.Errorf("header mismatch: got %v", decoded.Header)
	}
	if !decoded.Expiration.Equal(item.Expiration) {
		t.Errorf("expiration mismatch: got %v, want %v", decoded.Expiration, item.Expiration)
	}
}

func TestBinaryCodec_RoundTripWithTrailers(t *testing.T) {
	codec := &BinaryCodec{}
	item := cache.CacheItem{
		Content: []byte("body"),
		Header: http.Header{
			"Content-Type": {"text/plain"},
			"Trailer":      {"X-Origin-Trailer"},
		},
		Trailer:    http.Header{"X-Origin-Trailer": {"done"}},
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

	if got := decoded.Trailer.Get("X-Origin-Trailer"); got != "done" {
		t.Fatalf("trailer mismatch: got %q, want done", got)
	}
}

func TestBinaryCodecRoundTripsStoredAt(t *testing.T) {
	storedAt := time.Now().UTC().Truncate(time.Second)
	c := &BinaryCodec{}

	encoded, err := c.Encode(cache.CacheItem{
		Content:    []byte("body"),
		Status:     200,
		Header:     http.Header{"Date": []string{"Thu, 10 Sep 2026 03:58:45 GMT"}},
		Expiration: storedAt.Add(time.Minute),
		StoredAt:   storedAt,
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, err := c.Decode(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !decoded.StoredAt.Equal(storedAt) {
		t.Fatalf("expected StoredAt %v, got %v", storedAt, decoded.StoredAt)
	}
}
