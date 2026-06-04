package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/go-redis/redis/v8"
	"io"
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

	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
)

var ctx = context.Background()

func newCachingProxyHandler(origin *url.URL, cache Cache, ttl time.Duration) http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(origin)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			cacheKey := cacheKeyForRequest(r)
			if cachedItem, found := cache.Get(cacheKey); found {
				for key, values := range cachedItem.header {
					for _, value := range values {
						w.Header().Add(key, value)
					}
				}
				w.WriteHeader(cachedItem.status)
				_, _ = w.Write(cachedItem.content)
				return
			}

			crw := &CacheResponseWriter{ResponseWriter: w, buf: new(bytes.Buffer), status: http.StatusOK}
			proxy.ServeHTTP(crw, r)

			if crw.status >= http.StatusOK && crw.status < http.StatusMultipleChoices {
				cache.Set(cacheKey, crw.buf.Bytes(), crw.Header(), crw.status, ttl)
			}
			return
		}

		proxy.ServeHTTP(w, r)
	})
}

type Cache interface {
	Set(key string, content []byte, header http.Header, status int, ttl time.Duration)
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
	status     int
	expiration time.Time
}

// Set stores data in the cache
func (c *SimpleCache) Set(key string, content []byte, header http.Header, status int, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.store[key] = &CacheItem{
		content:    content,
		header:     header,
		status:     status,
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
func (r *RedisCache) Set(key string, content []byte, header http.Header, status int, ttl time.Duration) {
	// Serialize CacheItem
	cacheItem := CacheItem{
		content: content,
		header:  header,
		status:  status,
	}
	itemBytes, err := encodeCacheItem(cacheItem)
	if err != nil {
		log.Printf("failed to encode cache item for key %q: %v", key, err)
		return
	}
	if err := r.client.Set(ctx, key, itemBytes, ttl).Err(); err != nil {
		log.Printf("failed to write cache item for key %q: %v", key, err)
	}
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

	var cache Cache

	if config.CacheType == "redis" {
		cache = NewRedisCache(config.RedisURL)
	} else {
		cache = NewSimpleCache()
	}
	handler := newCachingProxyHandler(config.Origin, cache, config.TTL)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Received request: %s %s", r.Method, r.URL.Path)
		handler.ServeHTTP(w, r)
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

	status := uint32(item.status)
	if err := binary.Write(&buf, binary.LittleEndian, status); err != nil {
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

// readCount reads a uint32 length/count prefix and rejects any value that
// cannot be backed by the bytes remaining in r. Cache entries come from Redis
// and may be corrupt or maliciously crafted; without this bound a prefix of
// 0xFFFFFFFF would drive a multi-gigabyte allocation from a few input bytes.
func readCount(r *bytes.Reader) (uint32, error) {
	var n uint32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return 0, err
	}
	if int64(n) > int64(r.Len()) {
		return 0, fmt.Errorf("cache item: prefix %d exceeds %d remaining bytes", n, r.Len())
	}
	return n, nil
}

// readSized reads a uint32 length prefix then exactly that many bytes from r.
// It collapses the repeated binary.Read/buf.Read pairs in decodeCacheItem,
// keeping cyclomatic complexity under the gocyclo threshold of 15.
func readSized(r *bytes.Reader) ([]byte, error) {
	n, err := readCount(r)
	if err != nil {
		return nil, err
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	return b, nil
}

func decodeCacheItem(data []byte) (*CacheItem, error) {
	decoded, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return nil, err
	}

	buf := bytes.NewReader(decoded)
	item := &CacheItem{}

	if item.content, err = readSized(buf); err != nil {
		return nil, err
	}

	var status uint32
	if err := binary.Read(buf, binary.LittleEndian, &status); err != nil {
		return nil, err
	}
	if status < 100 || status > 999 {
		return nil, fmt.Errorf("cache item: invalid status code %d", status)
	}
	item.status = int(status)

	headerLen, err := readCount(buf)
	if err != nil {
		return nil, err
	}
	item.header = make(http.Header, headerLen)
	for i := uint32(0); i < headerLen; i++ {
		key, err := readSized(buf)
		if err != nil {
			return nil, err
		}

		valuesLen, err := readCount(buf)
		if err != nil {
			return nil, err
		}
		values := make([]string, valuesLen)
		for j := uint32(0); j < valuesLen; j++ {
			value, err := readSized(buf)
			if err != nil {
				return nil, err
			}
			values[j] = string(value)
		}

		item.header[string(key)] = values
	}

	expirationBytes, err := readSized(buf)
	if err != nil {
		return nil, err
	}
	if err := item.expiration.UnmarshalBinary(expirationBytes); err != nil {
		return nil, err
	}

	return item, nil
}
