package cache

import (
	"net/http"
	"testing"
	"testing/synctest"
	"time"
)

func TestMemoryCacheRemovesExpiredEntryOnGet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewMemoryCache()
		defer c.Close()
		c.Set("expired", CacheItem{Status: 200}, time.Second)
		time.Sleep(time.Second)
		if _, ok := c.Get("expired"); ok {
			t.Fatal("expired entry returned")
		}
		c.mu.Lock()
		_, retained := c.store["expired"]
		c.mu.Unlock()
		if retained {
			t.Fatal("Get did not remove the expired entry")
		}
	})
}

func TestMemoryCacheContract(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewMemoryCache()
		defer c.Close()
		var backend Cache = c
		header := http.Header{"X-Test": {"original"}}
		trailer := http.Header{"X-Finished": {"yes"}}
		backend.Set("host#/item", CacheItem{Content: []byte("body"), Header: header, Trailer: trailer, Status: 200}, time.Minute)
		backend.Set("host#/item#h=variant", CacheItem{Status: 200}, time.Minute)
		backend.Set("host#/item2", CacheItem{Status: 200}, time.Minute)
		header.Set("X-Test", "changed")
		trailer.Set("X-Finished", "changed")
		item, ok := backend.Get("host#/item")
		if !ok || string(item.Content) != "body" || item.Header.Get("X-Test") != "original" || item.Trailer.Get("X-Finished") != "yes" {
			t.Fatalf("stored item = %#v, %v", item, ok)
		}
		backend.InvalidatePrefix("host#/item")
		if _, ok := backend.Get("host#/item"); ok {
			t.Fatal("exact key survived invalidation")
		}
		if _, ok := backend.Get("host#/item#h=variant"); ok {
			t.Fatal("variant survived invalidation")
		}
		if _, ok := backend.Get("host#/item2"); !ok {
			t.Fatal("unrelated key was invalidated")
		}
		time.Sleep(time.Minute)
		if _, ok := backend.Get("host#/item2"); ok {
			t.Fatal("entry survived its expiry deadline")
		}
	})
}
