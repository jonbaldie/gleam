package main

import (
	"fmt"
	"github.com/jonbaldie/gleam/cache"
	"github.com/jonbaldie/gleam/proxy"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
)

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
	if ttlMinutes > int(math.MaxInt64/int64(time.Minute)) {
		return nil, fmt.Errorf("invalid TTL_MINUTES %d: too large to represent as a duration", ttlMinutes)
	}

	redisUrl := getenv("REDIS_URL", "redis://localhost:6379/0")
	cacheType := getenv("CACHE_TYPE", "memory")
	if err := validateCacheConfig(cacheType, redisUrl); err != nil {
		return nil, err
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

func validateCacheConfig(cacheType, redisURL string) error {
	if cacheType != "redis" && cacheType != "memory" {
		return fmt.Errorf("invalid CACHE_TYPE, must be 'memory' (default) or 'redis'")
	}
	if cacheType == "redis" {
		if _, err := redis.ParseURL(redisURL); err != nil {
			return fmt.Errorf("invalid REDIS_URL: %w", err)
		}
	}
	return nil
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

func requestLoggingHandler(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Received request: %s %s", r.Method, r.URL.EscapedPath())
		handler.ServeHTTP(w, r)
	})
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	config := loadConfig()

	log.Printf("Gleam started with Origin: %s, TTL: %v, Port: %s", config.OriginURL, config.TTL, config.Port)

	var c cache.Cache

	if config.CacheType == "redis" {
		redisCache, err := cache.NewRedisCache(config.RedisURL)
		if err != nil {
			return fmt.Errorf("invalid REDIS_URL: %w", err)
		}
		defer redisCache.Close()
		c = redisCache
	} else {
		memoryCache := cache.NewMemoryCache()
		defer memoryCache.Close()
		c = memoryCache
	}
	handler := proxy.NewWithVaryHeaders(config.Origin, c, config.TTL, config.VaryHeaders)

	http.Handle("/", requestLoggingHandler(handler))

	return http.ListenAndServe(":"+config.Port, nil)
}
