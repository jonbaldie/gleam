package main

import (
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"jonbaldie/gleam/cache"
)

func TestSimpleCacheEvictsExpiredEntriesWhileIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewSimpleCache()
		for i := 0; i < 10; i++ {
			c.Set(fmt.Sprintf("expired-%d", i), cache.CacheItem{Content: []byte("expired")}, time.Second)
		}
		c.Set("live", cache.CacheItem{Content: []byte("live")}, 10*time.Minute)

		time.Sleep(2 * time.Minute)
		synctest.Wait()

		c.mu.Lock()
		expired := 0
		for key := range c.store {
			if key != "live" {
				expired++
			}
		}
		live, found := c.store["live"]
		liveOK := found && string(live.item.Content) == "live"
		c.mu.Unlock()

		if expired != 0 {
			t.Errorf("expired entries remaining in store: got %d, want 0", expired)
		}
		if !liveOK {
			t.Error("live entry missing or changed")
		}
	})
}
