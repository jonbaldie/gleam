package cache

import (
	"sync"
	"time"
)

// MemoryCache stores entries in memory and periodically removes expired entries.
// Call Close when the cache is no longer needed to stop its expiry worker.
type MemoryCache struct {
	mu       sync.Mutex
	store    map[string]*memoryEntry
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

type memoryEntry struct {
	item      *CacheItem
	expiresAt time.Time
}

// NewMemoryCache starts an in-memory cache and its expiry worker.
func NewMemoryCache() *MemoryCache {
	c := &MemoryCache{
		store: make(map[string]*memoryEntry),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	ticker := time.NewTicker(time.Minute)
	go c.evictExpired(ticker)
	return c
}

// Set stores an item for the given lifetime.
func (c *MemoryCache) Set(key string, item CacheItem, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.store[key] = &memoryEntry{
		item: &CacheItem{
			Content:  item.Content,
			Header:   CloneHeader(item.Header),
			Trailer:  CloneHeader(item.Trailer),
			Status:   item.Status,
			StoredAt: item.StoredAt,
		},
		expiresAt: time.Now().Add(ttl),
	}
}

// Get returns a live item, removing it if it has expired.
func (c *MemoryCache) Get(key string) (*CacheItem, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, found := c.store[key]
	if !found {
		return nil, false
	}
	if !time.Now().Before(entry.expiresAt) {
		delete(c.store, key)
		return nil, false
	}
	return entry.item, true
}

// InvalidatePrefix removes the exact key and its vary variants.
func (c *MemoryCache) InvalidatePrefix(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key := range c.store {
		if MatchesPrefix(key, prefix) {
			delete(c.store, key)
		}
	}
}

func (c *MemoryCache) evictExpired(ticker *time.Ticker) {
	defer close(c.done)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.mu.Lock()
			now := time.Now()
			for key, entry := range c.store {
				if !now.Before(entry.expiresAt) {
					delete(c.store, key)
				}
			}
			c.mu.Unlock()
		case <-c.stop:
			return
		}
	}
}

// Close stops the expiry worker and waits for it to finish. Cache operations
// remain available after Close, but expired entries are no longer swept.
func (c *MemoryCache) Close() {
	c.stopOnce.Do(func() { close(c.stop) })
	<-c.done
}
