package cache

import (
	"net/http"
	"strings"
	"time"
)

// Cache interface defines the methods for a caching backend.
type Cache interface {
	Set(key string, item CacheItem, ttl time.Duration)
	Get(key string) (*CacheItem, bool)
	// InvalidatePrefix removes every stored entry for the given host+URI base
	// key, including any vary-variant entries derived from it. Shared caches
	// use it to satisfy RFC 9111 section 4.4 after a successful unsafe request.
	InvalidatePrefix(prefix string)
}

// MatchesPrefix reports whether key is the exact entry for the given host+URI
// base key or a vary-variant entry derived from it (prefix + "#h=" + hash).
func MatchesPrefix(key, prefix string) bool {
	return key == prefix || strings.HasPrefix(key, prefix+"#h=")
}

// CacheItem represents a single cache entry.
type CacheItem struct {
	Content    []byte
	Header     http.Header
	Trailer    http.Header
	Status     int
	Expiration time.Time
}
