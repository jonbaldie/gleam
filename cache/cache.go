package cache

import (
	"net/http"
	"time"
)

// Cache interface defines the methods for a caching backend.
type Cache interface {
	Set(key string, item CacheItem, ttl time.Duration)
	Get(key string) (*CacheItem, bool)
}

// CacheItem represents a single cache entry.
type CacheItem struct {
	Content    []byte
	Header     http.Header
	Status     int
	Expiration time.Time
}
