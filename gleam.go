package main

import (
	"bytes"
	"context"
	"github.com/go-redis/redis/v8"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"encoding/base64"
	"encoding/binary"
)

var ctx = context.Background()

type Cache interface {
	Set(key string, content []byte, header http.Header, ttl time.Duration)
	Get(key string) (*CacheItem, bool)
}

// SimpleCache holds the cache data
type SimpleCache struct {
	mu    sync.Mutex
	store map[string]*CacheItem
}

// CacheItem represents a single cache entry
type CacheItem struct {
	content    []byte
	header     http.Header
	expiration time.Time
}

// Set stores data in the cache
func (c *SimpleCache) Set(key string, content []byte, header http.Header, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.store[key] = &CacheItem{
		content:    content,
		header:     header,
		expiration: time.Now().Add(ttl),
	}
}

// Get retrieves data from the cache
func (c *SimpleCache) Get(key string) (*CacheItem, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	item, found := c.store[key]
	if !found || item.expiration.Before(time.Now()) {
		return nil, false
	}
	return item, true
}

// NewSimpleCache initializes and returns a new SimpleCache
func NewSimpleCache() *SimpleCache {
	return &SimpleCache{
		store: make(map[string]*CacheItem),
	}
}

// RedisCache implements the Cache interface using Redis
type RedisCache struct {
	client *redis.Client
}

// NewRedisCache initializes and returns a new RedisCache using a single Redis URL
func NewRedisCache(redisURL string) *RedisCache {
	// Parse the Redis URL
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("Failed to parse Redis URL: %v", err)
	}

	// Initialize the Redis client using the parsed options
	rdb := redis.NewClient(opt)
	return &RedisCache{
		client: rdb,
	}
}

// Set stores data in Redis
func (r *RedisCache) Set(key string, content []byte, header http.Header, ttl time.Duration) {
	// Serialize CacheItem
	cacheItem := CacheItem{
		content: content,
		header:  header,
	}
	itemBytes, _ := encodeCacheItem(cacheItem)
	r.client.Set(ctx, key, itemBytes, ttl).Err()
}

// Get retrieves data from Redis
func (r *RedisCache) Get(key string) (*CacheItem, bool) {
	// Fetch from Redis
	result, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil || err != nil {
		return nil, false
	}

	// Deserialize CacheItem
	var cacheItem *CacheItem
	cacheItem, err = decodeCacheItem([]byte(result))
	if err != nil {
		return nil, false
	}

	return cacheItem, true
}

type CacheResponseWriter struct {
	http.ResponseWriter
	buf    *bytes.Buffer
	header http.Header
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
	TTL       time.Duration
	Port      string
	RedisURL  string
	CacheType string
}

// loadConfig loads configuration from environment variables
func loadConfig() *Config {
	ttlMinutes, err := strconv.Atoi(getenv("TTL_MINUTES", "5"))
	if err != nil {
		log.Fatalf("Error parsing TTL_MINUTES: %v", err)
	}

	redisUrl := getenv("REDIS_URL", "redis://localhost:6379/0")
	cacheType := getenv("CACHE_TYPE", "memory")
	if cacheType != "redis" && cacheType != "memory" {
		log.Fatalf("Invalid CACHE_TYPE, must be 'memory' (default) or 'redis'")
	}

	return &Config{
		OriginURL: getenv("ORIGIN_URL", "https://httpbin.org"),
		TTL:       time.Duration(ttlMinutes) * time.Minute,
		Port:      getenv("PORT", "8080"),
		RedisURL:  redisUrl,
		CacheType: cacheType,
	}
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if len(value) == 0 {
		return fallback
	}
	return value
}

func main() {
	config := loadConfig()

	log.Printf("Gleam started with Origin: %s, TTL: %v, Port: %s", config.OriginURL, config.TTL, config.Port)

	var cache Cache

	if config.CacheType == "redis" {
		cache = NewRedisCache(config.RedisURL)
	} else {
		cache = NewSimpleCache()
	}

	origin, _ := url.Parse(config.OriginURL) // URL of the backend server
	proxy := httputil.NewSingleHostReverseProxy(origin)
	ttl := config.TTL // Time to live for cache entries

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Received request: %s %s", r.Method, r.URL.Path)

		if r.Method == "GET" {
			cacheKey := r.URL.String()
			if cachedItem, found := cache.Get(cacheKey); found {
				for key, values := range cachedItem.header {
					for _, value := range values {
						w.Header().Add(key, value)
					}
				}
				w.Write(cachedItem.content)
				return
			}

			crw := &CacheResponseWriter{ResponseWriter: w, buf: new(bytes.Buffer)}
			proxy.ServeHTTP(crw, r)

			cache.Set(cacheKey, crw.buf.Bytes(), crw.Header(), ttl)
		} else {
			proxy.ServeHTTP(w, r)
		}
	})

	log.Fatal(http.ListenAndServe(":"+config.Port, nil))
}

func encodeCacheItem(item CacheItem) ([]byte, error) {
	// Initialize a buffer to write the data into
	var buf bytes.Buffer

	// Write content length and content
	contentLen := uint32(len(item.content))
	if err := binary.Write(&buf, binary.LittleEndian, contentLen); err != nil {
		return nil, err
	}
	if _, err := buf.Write(item.content); err != nil {
		return nil, err
	}

	// Write the headers
	headerLen := uint32(len(item.header))
	if err := binary.Write(&buf, binary.LittleEndian, headerLen); err != nil {
		return nil, err
	}
	for key, values := range item.header {
		// Write the header key
		keyLen := uint32(len(key))
		if err := binary.Write(&buf, binary.LittleEndian, keyLen); err != nil {
			return nil, err
		}
		if _, err := buf.Write([]byte(key)); err != nil {
			return nil, err
		}

		// Write the number of values for this header key
		valuesLen := uint32(len(values))
		if err := binary.Write(&buf, binary.LittleEndian, valuesLen); err != nil {
			return nil, err
		}
		for _, value := range values {
			// Write the value
			valueLen := uint32(len(value))
			if err := binary.Write(&buf, binary.LittleEndian, valueLen); err != nil {
				return nil, err
			}
			if _, err := buf.Write([]byte(value)); err != nil {
				return nil, err
			}
		}
	}

	// Write expiration time
	expirationBytes, err := item.expiration.MarshalBinary()
	if err != nil {
		return nil, err
	}
	expirationLen := uint32(len(expirationBytes))
	if err := binary.Write(&buf, binary.LittleEndian, expirationLen); err != nil {
		return nil, err
	}
	if _, err := buf.Write(expirationBytes); err != nil {
		return nil, err
	}

	// Base64 encode the resulting byte slice
	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())
	return []byte(encoded), nil
}

func decodeCacheItem(data []byte) (*CacheItem, error) {
	// Decode the base64 input
	decoded, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return nil, err
	}

	buf := bytes.NewReader(decoded)
	item := &CacheItem{}

	// Read content length and content
	var contentLen uint32
	if err := binary.Read(buf, binary.LittleEndian, &contentLen); err != nil {
		return nil, err
	}
	item.content = make([]byte, contentLen)
	if _, err := buf.Read(item.content); err != nil {
		return nil, err
	}

	// Read the headers
	var headerLen uint32
	if err := binary.Read(buf, binary.LittleEndian, &headerLen); err != nil {
		return nil, err
	}
	item.header = make(http.Header, headerLen)
	for i := uint32(0); i < headerLen; i++ {
		// Read the header key
		var keyLen uint32
		if err := binary.Read(buf, binary.LittleEndian, &keyLen); err != nil {
			return nil, err
		}
		key := make([]byte, keyLen)
		if _, err := buf.Read(key); err != nil {
			return nil, err
		}

		// Read the number of values for this header key
		var valuesLen uint32
		if err := binary.Read(buf, binary.LittleEndian, &valuesLen); err != nil {
			return nil, err
		}
		values := make([]string, valuesLen)
		for j := uint32(0); j < valuesLen; j++ {
			// Read each value
			var valueLen uint32
			if err := binary.Read(buf, binary.LittleEndian, &valueLen); err != nil {
				return nil, err
			}
			value := make([]byte, valueLen)
			if _, err := buf.Read(value); err != nil {
				return nil, err
			}
			values[j] = string(value)
		}

		// Store the key-value pair in the header map
		item.header[string(key)] = values
	}

	// Read expiration time
	var expirationLen uint32
	if err := binary.Read(buf, binary.LittleEndian, &expirationLen); err != nil {
		return nil, err
	}
	expirationBytes := make([]byte, expirationLen)
	if _, err := buf.Read(expirationBytes); err != nil {
		return nil, err
	}
	if err := item.expiration.UnmarshalBinary(expirationBytes); err != nil {
		return nil, err
	}

	return item, nil
}
