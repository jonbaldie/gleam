package cache

import (
	"net/http"
	"strings"
	"time"
)

// Cache interface defines the methods for a caching backend.
//
// The backend owns entry expiry: callers pass a lifetime to Set and trust a
// miss from Get, so every implementation must enforce it itself.
type Cache interface {
	// Set stores item under key and keeps it no longer than ttl. Callers pass
	// a positive ttl: an entry with no remaining lifetime is never stored.
	Set(key string, item CacheItem, ttl time.Duration)
	// Get returns the entry stored under key only while it is live: once its
	// ttl has elapsed Get must report a miss, never the expired entry.
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
	Content []byte
	Header  http.Header
	Trailer http.Header
	Status  int
	// StoredAt is the response time of the stored response: the moment this
	// cache received it from the origin. Reuse of the entry reports an Age
	// measured from it (RFC 9111 section 4.2.3). A zero value means the
	// response time is unknown, as for entries stored by earlier versions.
	StoredAt time.Time
}
