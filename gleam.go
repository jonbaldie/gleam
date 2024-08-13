package main

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"
)

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
	CacheSize int
	Port      string
}

// loadConfig loads configuration from environment variables
func loadConfig() *Config {
	ttlMinutes, err := strconv.Atoi(getenv("TTL_MINUTES", "5"))
	if err != nil {
		log.Fatalf("Error parsing TTL_MINUTES: %v", err)
	}

	cacheSize, err := strconv.Atoi(getenv("CACHE_SIZE", "100"))
	if err != nil {
		log.Fatalf("Error parsing CACHE_SIZE: %v", err)
	}

	return &Config{
		OriginURL: getenv("ORIGIN_URL", "https://httpbin.org"),
		TTL:       time.Duration(ttlMinutes) * time.Minute,
		CacheSize: cacheSize,
		Port:      getenv("PORT", "8080"),
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

	log.Printf("Gleam started with Origin: %s, TTL: %v, Cache Size: %d, Port: %s", config.OriginURL, config.TTL, config.CacheSize, config.Port)

	origin, _ := url.Parse(config.OriginURL) // URL of the backend server
	proxy := httputil.NewSingleHostReverseProxy(origin)
	cache := NewSimpleCache()
	ttl := config.TTL // Time to live for cache entries

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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
