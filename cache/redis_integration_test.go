package cache

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

func TestRedisCacheRoundTripAndInvalidation(t *testing.T) {
	redisURL := os.Getenv("GLEAM_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("set GLEAM_TEST_REDIS_URL to run the Redis integration test")
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("Redis ping: %v", err)
	}

	c, err := NewRedisCache(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	var backend Cache = c
	prefix := fmt.Sprintf("gleam-test-%d-%d#/item[*]", os.Getpid(), time.Now().UnixNano())
	keys := []string{prefix, prefix + "#h=variant", prefix + "-other", prefix + "-expiring", prefix + "-bad", prefix + "-legacy"}
	t.Cleanup(func() { _ = client.Del(ctx, keys...).Err() })
	storedAt := time.Date(2026, 9, 10, 3, 58, 45, 0, time.UTC)
	want := CacheItem{
		Content:  []byte("cached response"),
		Status:   http.StatusOK,
		Header:   http.Header{"X-Test": {"one", "two"}},
		Trailer:  http.Header{"X-Finished": {"yes"}},
		StoredAt: storedAt,
	}
	for _, key := range keys[:3] {
		backend.Set(key, want, time.Minute)
	}
	const legacy = "DwAAAGNhY2hlZCByZXNwb25zZcgAAAABAAAABgAAAFgtVGVzdAIAAAADAAAAb25lAwAAAHR3bw8AAAABAAAAAAAAAAAAAAAA//8BAAAACgAAAFgtRmluaXNoZWQBAAAAAwAAAHllcw8AAAABAAAADuI0IfUAAAAA//8="
	encoded, err := client.Get(ctx, prefix).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeCacheItem(encoded); err == nil {
		t.Fatal("legacy reader accepted new Redis encoding")
	}
	if err := client.Set(ctx, keys[5], legacy, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if item, ok := backend.Get(keys[5]); !ok || !reflect.DeepEqual(item, &want) {
		t.Fatalf("legacy Redis entry = %#v, %v; want %#v", item, ok, want)
	}
	got, ok := backend.Get(prefix)
	if !ok || !reflect.DeepEqual(got, &want) {
		t.Fatalf("round trip = %#v, %v; want %#v", got, ok, want)
	}
	backend.InvalidatePrefix(prefix)
	for _, key := range keys[:2] {
		if item, ok := backend.Get(key); ok {
			t.Fatalf("invalidated key %q returned %#v", key, item)
		}
	}
	if item, ok := backend.Get(keys[2]); !ok || !reflect.DeepEqual(item, &want) {
		t.Fatalf("unrelated key = %#v, %v; want %#v", item, ok, want)
	}
	backend.Set(keys[3], want, time.Second)
	if _, ok := backend.Get(keys[3]); !ok {
		t.Fatal("new entry missing before its TTL")
	}
	time.Sleep(1200 * time.Millisecond)
	if item, ok := backend.Get(keys[3]); ok {
		t.Fatalf("expired entry returned %#v", item)
	}
	if err := client.Set(ctx, keys[4], "not-base64", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if item, ok := backend.Get(keys[4]); ok {
		t.Fatalf("corrupt entry returned %#v", item)
	}
}
