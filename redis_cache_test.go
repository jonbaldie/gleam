package main

import (
	"errors"
	"testing"
	"time"
)

type mockCodec struct {
	encodeCalled bool
	encodeErr    error
}

func (m *mockCodec) Encode(item CacheItem) ([]byte, error) {
	m.encodeCalled = true
	return nil, m.encodeErr
}

func (m *mockCodec) Decode(data []byte) (*CacheItem, error) {
	return nil, nil
}

func TestRedisCache_DelegatesToCodec(t *testing.T) {
	codec := &mockCodec{encodeErr: errors.New("mock error")}
	// Create RedisCache with our mock codec and a nil client.
	// We expect Set to fail early during Encode and not panic on nil client.
	cache := &RedisCache{
		codec: codec,
		// client is nil
	}

	cache.Set("test_key", CacheItem{content: []byte("data"), status: 200}, time.Minute)

	if !codec.encodeCalled {
		t.Errorf("expected RedisCache.Set to call Codec.Encode")
	}
}
