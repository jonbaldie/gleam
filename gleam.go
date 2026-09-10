package main

import (
	"context"
	"fmt"
	"jonbaldie/gleam/cache"
	"jonbaldie/gleam/codec"
	"jonbaldie/gleam/proxy"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
)

var ctx = context.Background()

// SimpleCache holds the cache data
type SimpleCache struct {
	mu    sync.Mutex
	store map[string]*cache.CacheItem
}

// Set stores data in the cache
func (c *SimpleCache) Set(key string, item cache.CacheItem, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.store[key] = &cache.CacheItem{
		Content:    item.Content,
		Header:     cloneHeader(item.Header),
		Trailer:    cloneHeader(item.Trailer),
		Status:     item.Status,
		Expiration: time.Now().Add(ttl),
		StoredAt:   item.StoredAt,
	}
}

// Get retrieves data from the cache
func (c *SimpleCache) Get(key string) (*cache.CacheItem, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	item, found := c.store[key]
	if !found {
		return nil, false
	}
	if item.Expiration.Before(time.Now()) {
		delete(c.store, key)
		return nil, false
	}
	return item, true
}

// InvalidatePrefix removes every entry whose key matches the given host+URI
// base key, including vary-variant entries derived from it.
func (c *SimpleCache) InvalidatePrefix(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key := range c.store {
		if cache.MatchesPrefix(key, prefix) {
			delete(c.store, key)
		}
	}
}

// NewSimpleCache initializes and returns a new SimpleCache
func NewSimpleCache() *SimpleCache {
	return &SimpleCache{
		store: make(map[string]*cache.CacheItem),
	}
}

// RedisCache implements the Cache interface using Redis
type RedisCache struct {
	client *redis.Client
	codec  codec.Codec
}

// NewRedisCache initializes and returns a new RedisCache using a single Redis URL
func NewRedisCache(redisURL string, cd codec.Codec) *RedisCache {
	// Parse the Redis URL
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("Failed to parse Redis URL: %v", err)
	}

	// Initialize the Redis client using the parsed options
	rdb := redis.NewClient(opt)
	return &RedisCache{
		client: rdb,
		codec:  cd,
	}
}

// Set stores data in Redis
func (r *RedisCache) Set(key string, item cache.CacheItem, ttl time.Duration) {
	itemBytes, err := r.codec.Encode(item)
	if err != nil {
		log.Printf("failed to encode cache item for key %q: %v", key, err)
		return
	}
	if err := r.client.Set(ctx, key, itemBytes, ttl).Err(); err != nil {
		log.Printf("failed to write cache item for key %q: %v", key, err)
	}
}

// Get retrieves data from Redis
func (r *RedisCache) Get(key string) (*cache.CacheItem, bool) {
	// Fetch from Redis
	result, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil || err != nil {
		return nil, false
	}

	// Deserialize CacheItem
	var cacheItem *cache.CacheItem
	cacheItem, err = r.codec.Decode([]byte(result))
	if err != nil {
		return nil, false
	}

	return cacheItem, true
}

// InvalidatePrefix removes every entry whose key matches the given host+URI
// base key, including vary-variant entries derived from it. Redis keys embed
// the vary-header hash, so the keyspace is enumerated and filtered
// client-side rather than matched by a glob pattern.
func (r *RedisCache) InvalidatePrefix(prefix string) {
	var toDelete []string
	var cursor uint64
	for {
		keys, next, err := r.client.Scan(ctx, cursor, "", 100).Result()
		if err != nil {
			log.Printf("failed to enumerate cache keys to invalidate %q: %v", prefix, err)
			return
		}
		for _, key := range keys {
			if cache.MatchesPrefix(key, prefix) {
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

// Config holds all configurable options
type Config struct {
	OriginURL   string
	Origin      *url.URL
	TTL         time.Duration
	Port        string
	RedisURL    string
	CacheType   string
	VaryHeaders []string
}

// loadConfig loads configuration from environment variables
func loadConfig() *Config {
	config, err := loadConfigFromEnv()
	if err != nil {
		log.Fatalf("%v", err)
	}
	return config
}

func loadConfigFromEnv() (*Config, error) {
	ttlMinutes, err := strconv.Atoi(getenv("TTL_MINUTES", "5"))
	if err != nil {
		return nil, fmt.Errorf("error parsing TTL_MINUTES: %w", err)
	}
	if ttlMinutes <= 0 {
		return nil, fmt.Errorf("invalid TTL_MINUTES %d: must be greater than 0", ttlMinutes)
	}

	redisUrl := getenv("REDIS_URL", "redis://localhost:6379/0")
	cacheType := getenv("CACHE_TYPE", "memory")
	if cacheType != "redis" && cacheType != "memory" {
		return nil, fmt.Errorf("invalid CACHE_TYPE, must be 'memory' (default) or 'redis'")
	}
	varyHeaders := proxy.DefaultVaryHeaders()
	if raw, ok := os.LookupEnv("CACHE_VARY_HEADERS"); ok {
		varyHeaders = proxy.ParseVaryHeaders(raw)
	}

	originURL := getenv("ORIGIN_URL", "https://httpbin.org")
	origin, err := parseOriginURL(originURL)
	if err != nil {
		return nil, err
	}

	return &Config{
		OriginURL:   originURL,
		Origin:      origin,
		TTL:         time.Duration(ttlMinutes) * time.Minute,
		Port:        getenv("PORT", "8080"),
		RedisURL:    redisUrl,
		CacheType:   cacheType,
		VaryHeaders: varyHeaders,
	}, nil
}

func parseOriginURL(raw string) (*url.URL, error) {
	origin, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid ORIGIN_URL %q: %w", raw, err)
	}
	if origin.Scheme != "http" && origin.Scheme != "https" {
		return nil, fmt.Errorf("invalid ORIGIN_URL %q: must include http or https scheme", raw)
	}
	if origin.Host == "" {
		return nil, fmt.Errorf("invalid ORIGIN_URL %q: host is required", raw)
	}
	return origin, nil
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if len(value) == 0 {
		return fallback
	}
	return value
}

func cloneHeader(header http.Header) http.Header {
	if len(header) == 0 {
		return nil
	}
	clone := make(http.Header, len(header))
	for key, values := range header {
		clone[key] = append([]string(nil), values...)
	}
	return clone
}

func main() {
	config := loadConfig()

	log.Printf("Gleam started with Origin: %s, TTL: %v, Port: %s", config.OriginURL, config.TTL, config.Port)

	var c cache.Cache

	if config.CacheType == "redis" {
		c = NewRedisCache(config.RedisURL, &codec.BinaryCodec{})
	} else {
		c = NewSimpleCache()
	}
	handler := proxy.NewWithVaryHeaders(config.Origin, c, config.TTL, config.VaryHeaders)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Received request: %s %s", r.Method, r.URL.Path)
		handler.ServeHTTP(w, r)
	})

	log.Fatal(http.ListenAndServe(":"+config.Port, nil))
}
