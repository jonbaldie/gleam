package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"jonbaldie/gleam/cache"
	"jonbaldie/gleam/codec"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
)

var ctx = context.Background()

func newCachingProxyHandler(origin *url.URL, c cache.Cache, ttl time.Duration) http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(origin)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			cacheKey := cacheKeyForRequest(r)
			if cachedItem, found := c.Get(cacheKey); found {
				for key, values := range cachedItem.Header {
					for _, value := range values {
						w.Header().Add(key, value)
					}
				}
				w.WriteHeader(cachedItem.Status)
				_, _ = w.Write(cachedItem.Content)
				return
			}

			crw := &CacheResponseWriter{ResponseWriter: w, buf: new(bytes.Buffer), status: http.StatusOK}
			proxy.ServeHTTP(crw, r)

			if crw.status >= http.StatusOK && crw.status < http.StatusMultipleChoices {
				c.Set(cacheKey, cache.CacheItem{Content: crw.buf.Bytes(), Header: crw.Header(), Status: crw.status}, ttl)
			}
			return
		}

		proxy.ServeHTTP(w, r)
	})
}

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
		Header:     item.Header,
		Status:     item.Status,
		Expiration: time.Now().Add(ttl),
	}
}

// Get retrieves data from the cache
func (c *SimpleCache) Get(key string) (*cache.CacheItem, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	item, found := c.store[key]
	if !found || item.Expiration.Before(time.Now()) {
		return nil, false
	}
	return item, true
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

type CacheResponseWriter struct {
	http.ResponseWriter
	buf    *bytes.Buffer
	status int
}

func (w *CacheResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *CacheResponseWriter) Write(b []byte) (int, error) {
	w.buf.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *CacheResponseWriter) Header() http.Header {
	return w.ResponseWriter.Header()
}

// Config holds all configurable options
type Config struct {
	OriginURL string
	Origin    *url.URL
	TTL       time.Duration
	Port      string
	RedisURL  string
	CacheType string
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

	originURL := getenv("ORIGIN_URL", "https://httpbin.org")
	origin, err := parseOriginURL(originURL)
	if err != nil {
		return nil, err
	}

	return &Config{
		OriginURL: originURL,
		Origin:    origin,
		TTL:       time.Duration(ttlMinutes) * time.Minute,
		Port:      getenv("PORT", "8080"),
		RedisURL:  redisUrl,
		CacheType: cacheType,
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

// Include request headers in the cache key so one caller's GET response is not
// served to another caller with different request metadata.
func cacheKeyForRequest(r *http.Request) string {
	base := r.Host + "#" + r.URL.String()
	if len(r.Header) == 0 {
		return base
	}

	headerNames := make([]string, 0, len(r.Header))
	for name := range r.Header {
		headerNames = append(headerNames, name)
	}
	sort.Strings(headerNames)

	var signature strings.Builder
	for _, name := range headerNames {
		values := append([]string(nil), r.Header.Values(name)...)
		sort.Strings(values)
		safeName := strings.ReplaceAll(name, "\n", "\\n")
		signature.WriteString(safeName)
		signature.WriteByte(':')
		escapedValues := make([]string, len(values))
		for i, val := range values {
			escaped := strings.ReplaceAll(val, "\n", "\\n")
			escapedValues[i] = strings.ReplaceAll(escaped, "\x00", "\\0")
		}
		signature.WriteString(strings.Join(escapedValues, "\x00"))
		signature.WriteByte('\n')
	}

	sum := sha256.Sum256([]byte(signature.String()))
	return base + "#h=" + hex.EncodeToString(sum[:])
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
	handler := newCachingProxyHandler(config.Origin, c, config.TTL)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Received request: %s %s", r.Method, r.URL.Path)
		handler.ServeHTTP(w, r)
	})

	log.Fatal(http.ListenAndServe(":"+config.Port, nil))
}
