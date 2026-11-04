package proxy

import (
	"testing"

	"jonbaldie/gleam/cache"
)

func newTestCache(t *testing.T) *cache.MemoryCache {
	t.Helper()
	c := cache.NewMemoryCache()
	t.Cleanup(c.Close)
	return c
}
