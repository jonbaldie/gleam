package codec

import (
	"bytes"
	"encoding/base64"
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
		Content: []byte("test"),
		Header:  http.Header{"X-Test": {"1"}},
		Status:  200,
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
}

func TestBinaryCodec_RoundTripWithTrailers(t *testing.T) {
	codec := &BinaryCodec{}
	item := cache.CacheItem{
		Content: []byte("body"),
		Header: http.Header{
			"Content-Type": {"text/plain"},
			"Trailer":      {"X-Origin-Trailer"},
		},
		Trailer: http.Header{"X-Origin-Trailer": {"done"}},
		Status:  200,
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
		Content:  []byte("body"),
		Status:   200,
		Header:   http.Header{"Date": []string{"Thu, 10 Sep 2026 03:58:45 GMT"}},
		StoredAt: storedAt,
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

// Entries written before expiry moved out of CacheItem carry a real
// timestamp in the reserved slot between Header and Trailer. The decoder must
// skip it and still recover the fields that follow.
func TestBinaryCodecDecodesEntriesWithLegacyExpiration(t *testing.T) {
	storedAt := time.Now().UTC().Truncate(time.Second)

	var buf bytes.Buffer
	if err := writeSized(&buf, []byte("body")); err != nil {
		t.Fatal(err)
	}
	if err := writeU32(&buf, 200); err != nil {
		t.Fatal(err)
	}
	if err := encodeHeaders(&buf, http.Header{"X-Test": {"1"}}); err != nil {
		t.Fatal(err)
	}
	if err := encodeTime(&buf, storedAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := encodeHeaders(&buf, http.Header{"X-Origin-Trailer": {"done"}}); err != nil {
		t.Fatal(err)
	}
	if err := encodeTime(&buf, storedAt); err != nil {
		t.Fatal(err)
	}

	c := &BinaryCodec{}
	decoded, err := c.Decode([]byte(base64.StdEncoding.EncodeToString(buf.Bytes())))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(decoded.Content) != "body" || decoded.Status != 200 || decoded.Header.Get("X-Test") != "1" {
		t.Fatalf("unexpected leading fields: %+v", decoded)
	}
	if got := decoded.Trailer.Get("X-Origin-Trailer"); got != "done" {
		t.Fatalf("trailer mismatch: got %q, want done", got)
	}
	if !decoded.StoredAt.Equal(storedAt) {
		t.Fatalf("expected StoredAt %v, got %v", storedAt, decoded.StoredAt)
	}
}

// Readers from earlier versions expect a timestamp between Header and
// Trailer. New entries must keep writing that slot, as the zero time, so a
// mixed deployment sharing one Redis database never misaligns Trailer.
func TestBinaryCodecWritesZeroTimeInReservedExpirationSlot(t *testing.T) {
	c := &BinaryCodec{}
	encoded, err := c.Encode(cache.CacheItem{
		Content:  []byte("body"),
		Status:   200,
		Header:   http.Header{"X-Test": {"1"}},
		Trailer:  http.Header{"X-Origin-Trailer": {"done"}},
		StoredAt: time.Now().UTC().Truncate(time.Second),
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(string(encoded))
	if err != nil {
		t.Fatal(err)
	}

	r := bytes.NewReader(raw)
	if _, err := readSized(r); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeStatus(r); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeHeaders(r); err != nil {
		t.Fatal(err)
	}
	reserved, err := decodeTime(r)
	if err != nil {
		t.Fatalf("reserved expiration slot missing: %v", err)
	}
	if !reserved.IsZero() {
		t.Fatalf("expected zero time in reserved slot, got %v", reserved)
	}
	trailer, err := decodeHeaders(r)
	if err != nil {
		t.Fatalf("trailer after reserved slot: %v", err)
	}
	if got := trailer.Get("X-Origin-Trailer"); got != "done" {
		t.Fatalf("trailer mismatch: got %q, want done", got)
	}
}
