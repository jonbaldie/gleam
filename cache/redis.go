package cache

import (
	"context"
	"log"
	"time"

	"github.com/go-redis/redis/v8"
)

// RedisCache stores cache entries in Redis using their existing binary format.
type RedisCache struct {
	client *redis.Client
}

// NewRedisCache creates a Redis-backed cache. It does not connect until used.
func NewRedisCache(redisURL string) (*RedisCache, error) {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	return &RedisCache{client: redis.NewClient(opt)}, nil
}

// Close releases the Redis client's resources.
func (r *RedisCache) Close() error {
	return r.client.Close()
}

func (r *RedisCache) Set(key string, item CacheItem, ttl time.Duration) {
	itemBytes, err := encodeCacheItem(item)
	if err != nil {
		log.Printf("failed to encode cache item for key %q: %v", key, err)
		return
	}
	if err := r.client.Set(context.Background(), key, itemBytes, ttl).Err(); err != nil {
		log.Printf("failed to write cache item for key %q: %v", key, err)
	}
}

func (r *RedisCache) Get(key string) (*CacheItem, bool) {
	result, err := r.client.Get(context.Background(), key).Result()
	if err != nil {
		return nil, false
	}
	item, err := decodeCacheItem([]byte(result))
	if err != nil {
		return nil, false
	}
	return item, true
}

// InvalidatePrefix removes the exact key and its vary variants.
// Redis keys are scanned and filtered client-side rather than glob-matched.
func (r *RedisCache) InvalidatePrefix(prefix string) {
	ctx := context.Background()
	var toDelete []string
	var cursor uint64
	for {
		keys, next, err := r.client.Scan(ctx, cursor, "", 100).Result()
		if err != nil {
			log.Printf("failed to enumerate cache keys to invalidate %q: %v", prefix, err)
			return
		}
		for _, key := range keys {
			if MatchesPrefix(key, prefix) {
				toDelete = append(toDelete, key)
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	if len(toDelete) == 0 {
		return
	}
	if err := r.client.Del(ctx, toDelete...).Err(); err != nil {
		log.Printf("failed to invalidate %d cache entries for %q: %v", len(toDelete), prefix, err)
	}
}
