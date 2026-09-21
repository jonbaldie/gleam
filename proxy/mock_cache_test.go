package proxy

import (
	"sync"
	"time"

	"jonbaldie/gleam/cache"
)

type mockCache struct {
	mu    sync.Mutex
	store map[string]*mockEntry
}

type mockEntry struct {
	item      *cache.CacheItem
	expiresAt time.Time
}

func (c *mockCache) Set(key string, item cache.CacheItem, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.store[key] = &mockEntry{
		item: &cache.CacheItem{
			Content:  item.Content,
			Header:   cloneHeader(item.Header),
			Trailer:  cloneHeader(item.Trailer),
			Status:   item.Status,
			StoredAt: item.StoredAt,
		},
		expiresAt: time.Now().Add(ttl),
	}
}

func (c *mockCache) Get(key string) (*cache.CacheItem, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, found := c.store[key]
	if !found {
		return nil, false
	}
	if entry.expiresAt.Before(time.Now()) {
		delete(c.store, key)
		return nil, false
	}
	return entry.item, true
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
		store: make(map[string]*mockEntry),
	}
}
