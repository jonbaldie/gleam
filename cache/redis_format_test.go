package cache

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

func TestRedisFormatLargeMetadataFallsBackToLegacy(t *testing.T) {
	item := CacheItem{Status: http.StatusOK, Header: http.Header{"X-Large": {strings.Repeat("x", 13<<20)}}, Content: []byte("replacement")}
	data, err := encodeRedisItem(item)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(data, []byte(redisFormatMagic)) {
		t.Fatal("oversized metadata did not use the legacy format")
	}
	got, err := decodeRedisItem(data)
	if err != nil || got.Header.Get("X-Large") != item.Header.Get("X-Large") || string(got.Content) != "replacement" {
		t.Fatalf("legacy fallback failed: %v", err)
	}
}
