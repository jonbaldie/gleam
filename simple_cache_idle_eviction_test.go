package main

import (
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"jonbaldie/gleam/cache"
)

func TestSimpleCacheEvictsExpiredEntriesWhileIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewSimpleCache()
		defer c.Close()
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

func TestSimpleCacheSweepOverwriteAndInvalidation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewSimpleCache()
		defer c.Close()
		c.Set("overwrite", cache.CacheItem{Content: []byte("old")}, time.Minute)
		c.Set("host/path", cache.CacheItem{}, time.Minute)
		c.Set("host/path#h=variant", cache.CacheItem{}, time.Minute)
		time.Sleep(30 * time.Second)
		c.Set("overwrite", cache.CacheItem{Content: []byte("new")}, 2*time.Minute)
		c.InvalidatePrefix("host/path")
		time.Sleep(30 * time.Second)
		synctest.Wait()
		c.mu.Lock()
		entry, found := c.store["overwrite"]
		count := len(c.store)
		c.mu.Unlock()
		if !found || string(entry.item.Content) != "new" || count != 1 {
			t.Fatalf("after sweep: overwrite found = %v, store size = %d", found, count)
		}
	})
}

func TestSimpleCacheExactDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewSimpleCache()
		defer c.Close()
		c.Set("get", cache.CacheItem{}, 30*time.Second)
		c.Set("sweep", cache.CacheItem{}, time.Minute)
		time.Sleep(30 * time.Second)
		if _, found := c.Get("get"); found {
			t.Fatal("Get returned an entry at its expiry deadline")
		}
		time.Sleep(30 * time.Second)
		synctest.Wait()
		c.mu.Lock()
		_, found := c.store["sweep"]
		c.mu.Unlock()
		if found {
			t.Fatal("sweep retained an entry at its expiry deadline")
		}
	})
}

func TestSimpleCacheCloseConcurrentAndOperationsAfterClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewSimpleCache()
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c.Close()
			}()
		}
		wg.Wait()
		c.Close()
		c.Set("expired", cache.CacheItem{}, time.Second)
		c.Set("live", cache.CacheItem{Content: []byte("live")}, 10*time.Minute)
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		c.mu.Lock()
		_, retained := c.store["expired"]
		c.mu.Unlock()
		if !retained {
			t.Error("worker swept an entry after Close returned")
		}
		if _, found := c.Get("expired"); found {
			t.Error("Get returned an expired entry after Close")
		}
		if item, found := c.Get("live"); !found || string(item.Content) != "live" {
			t.Error("live entry unavailable after Close")
		}
		c.InvalidatePrefix("live")
		if _, found := c.Get("live"); found {
			t.Error("InvalidatePrefix failed after Close")
		}
	})
}

func TestSimpleCacheConcurrentSweepAndOperations(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewSimpleCache()
		defer c.Close()
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					key := fmt.Sprintf("host/%d", i)
					c.Set(key, cache.CacheItem{}, time.Second)
					c.Get(key)
					c.InvalidatePrefix(key)
					time.Sleep(time.Second)
				}
			}(i)
		}
		wg.Wait()
	})
}
