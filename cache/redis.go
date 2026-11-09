package cache

import (
	"context"
	"log"
	"time"

	"github.com/go-redis/redis/v8"
)

// RedisCache stores cache entries in Redis.
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
	itemBytes, err := encodeRedisItem(item)
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
	item, err := decodeRedisItem([]byte(result))
	if err != nil {
		return nil, false
	}
	return item, true
}

var redisMetadataScript = redis.NewScript(`
local header = redis.call('GETRANGE', KEYS[1], 0, 19)
if string.len(header) ~= 20 or string.sub(header, 1, 8) ~= '\000gleam1\n' then
  return nil
end
local a, b, c, d = string.byte(header, 9, 12)
local metadataLen = ((a * 256 + b) * 256 + c) * 256 + d
local high1, high2, high3, high4, e, f, g, h = string.byte(header, 13, 20)
if high1 ~= 0 or high2 ~= 0 or high3 ~= 0 or high4 ~= 0 or metadataLen > 16777216 then
  return nil
end
local bodyLen = ((e * 256 + f) * 256 + g) * 256 + h
if redis.call('STRLEN', KEYS[1]) ~= 84 + metadataLen + bodyLen then
  return nil
end
return redis.call('GETRANGE', KEYS[1], 0, 83 + metadataLen)
`)

// GetMetadata reads a complete metadata snapshot without transferring the body.
// Legacy or malformed entries fall back to Get.
func (r *RedisCache) GetMetadata(key string) (*CacheItem, bool) {
	result, err := redisMetadataScript.Run(context.Background(), r.client, []string{key}).Text()
	if err != nil {
		return nil, false
	}
	item, err := decodeRedisMetadata([]byte(result))
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
