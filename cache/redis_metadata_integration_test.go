package cache

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func redisMetadataTestCache(t *testing.T) (*RedisCache, string) {
	t.Helper()
	redisURL := os.Getenv("GLEAM_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("set GLEAM_TEST_REDIS_URL to run the Redis integration test")
	}
	c, err := NewRedisCache(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("gleam-metadata-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	t.Cleanup(func() {
		_ = c.client.Del(context.Background(), key).Err()
		_ = c.Close()
	})
	return c, key
}

func TestRedisMetadataExpiryAndCorruption(t *testing.T) {
	c, key := redisMetadataTestCache(t)
	ctx := context.Background()
	item := CacheItem{Status: http.StatusOK, Header: http.Header{"Etag": {`"v1"`}}, Content: []byte("body")}
	c.Set(key, item, time.Minute)
	metadata, ok := c.GetMetadata(key)
	if !ok || metadata.Header.Get("Etag") != `"v1"` || len(metadata.Content) != 0 {
		t.Fatalf("metadata = %#v, %v", metadata, ok)
	}
	data, err := c.client.Get(ctx, key).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	corruptBody := append([]byte(nil), data...)
	corruptBody[len(corruptBody)-1] ^= 1
	if err := c.client.Set(ctx, key, corruptBody, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if got, ok := c.Get(key); ok {
		t.Fatalf("corrupt body returned %#v", got)
	}
	corruptLength := append([]byte(nil), data[:len(data)-1]...)
	binary.BigEndian.PutUint64(corruptLength[12:20], uint64(len(item.Content)-1))
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"truncated-body", data[:len(data)-1]},
		{"truncated-body-with-adjusted-length", corruptLength},
		{"truncated-metadata", data[:redisFormatHeaderSize+1]},
		{"bad-metadata", append(append([]byte(nil), data[:redisFormatHeaderSize]...), append([]byte(strings.Repeat("!", len(data)-redisFormatHeaderSize-len(item.Content))), item.Content...)...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := c.client.Set(ctx, key, tc.data, time.Minute).Err(); err != nil {
				t.Fatal(err)
			}
			if got, ok := c.GetMetadata(key); ok {
				t.Fatalf("corrupt metadata returned %#v", got)
			}
			if got, ok := c.Get(key); ok {
				t.Fatalf("corrupt entry returned %#v", got)
			}
		})
	}
	c.Set(key, item, time.Minute)
	if err := c.client.Del(ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	if got, ok := c.GetMetadata(key); ok {
		t.Fatalf("deleted entry returned %#v", got)
	}
	c.Set(key, item, 30*time.Millisecond)
	time.Sleep(80 * time.Millisecond)
	if got, ok := c.GetMetadata(key); ok {
		t.Fatalf("expired entry returned %#v", got)
	}
}

func TestRedisConcurrentReplacementReturnsWholeRepresentation(t *testing.T) {
	c, key := redisMetadataTestCache(t)
	items := []CacheItem{
		{Status: http.StatusOK, Header: http.Header{"Etag": {`"a"`}}, Content: []byte("body-a")},
		{Status: http.StatusOK, Header: http.Header{"Etag": {`"b"`}}, Content: []byte("body-b")},
	}
	c.Set(key, items[0], time.Minute)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			c.Set(key, items[i%2], time.Minute)
		}
	}()
	for i := 0; i < 500; i++ {
		metadata, ok := c.GetMetadata(key)
		if !ok || (metadata.Header.Get("Etag") != `"a"` && metadata.Header.Get("Etag") != `"b"`) || len(metadata.Content) != 0 {
			t.Errorf("invalid metadata snapshot: %#v, %v", metadata, ok)
			break
		}
		got, ok := c.Get(key)
		if !ok {
			t.Error("entry missing during replacement")
			break
		}
		if (got.Header.Get("Etag") != `"a"` || string(got.Content) != "body-a") && (got.Header.Get("Etag") != `"b"` || string(got.Content) != "body-b") {
			t.Errorf("mixed representation: %#v", got)
			break
		}
	}
	wg.Wait()
}

func TestRedisMetadataLegacyOverwrite(t *testing.T) {
	c, key := redisMetadataTestCache(t)
	ctx := context.Background()
	newItem := CacheItem{Status: http.StatusOK, Header: http.Header{"Etag": {`"new"`}}, Content: []byte("new")}
	oldItem := CacheItem{Status: http.StatusOK, Header: http.Header{"Etag": {`"old"`}}, Content: []byte("old")}
	c.Set(key, newItem, time.Minute)
	legacy, err := encodeCacheItem(oldItem)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.client.Set(ctx, key, legacy, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if got, ok := c.GetMetadata(key); ok {
		t.Fatalf("legacy entry returned metadata %#v", got)
	}
	if got, ok := c.Get(key); !ok || got.Header.Get("Etag") != `"old"` || string(got.Content) != "old" {
		t.Fatalf("legacy entry = %#v, %v", got, ok)
	}
}
