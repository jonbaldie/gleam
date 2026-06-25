package proxy

import (
	"sync"
	"time"

	"jonbaldie/gleam/cache"
)

type mockCache struct {
	mu    sync.Mutex
	store map[string]*cache.CacheItem
}

func (c *mockCache) Set(key string, item cache.CacheItem, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.store[key] = &cache.CacheItem{
		Content:    item.Content,
		Header:     item.Header,
		Status:     item.Status,
		Expiration: time.Now().Add(ttl),
	}
}

func (c *mockCache) Get(key string) (*cache.CacheItem, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	item, found := c.store[key]
	if !found || item.Expiration.Before(time.Now()) {
		return nil, false
	}
	return item, true
}

func newMockCache() *mockCache {
	return &mockCache{
		store: make(map[string]*cache.CacheItem),
	}
}
