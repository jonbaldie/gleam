package proxy

import (
	"testing"

	"github.com/jonbaldie/gleam/cache"
)

func newTestCache(t *testing.T) *cache.MemoryCache {
	t.Helper()
	c := cache.NewMemoryCache()
	t.Cleanup(c.Close)
	return c
}
