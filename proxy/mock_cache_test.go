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
		Header:     cloneHeader(item.Header),
		Trailer:    cloneHeader(item.Trailer),
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

func (c *mockCache) InvalidatePrefix(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key := range c.store {
		if cache.MatchesPrefix(key, prefix) {
			delete(c.store, key)
		}
	}
}

func newMockCache() *mockCache {
	return &mockCache{
		store: make(map[string]*cache.CacheItem),
	}
}
