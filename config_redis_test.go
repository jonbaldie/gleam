package main

import (
	"strings"
	"testing"
)

func TestLoadConfigRejectsInvalidRedisURL(t *testing.T) {
	t.Setenv("CACHE_TYPE", "redis")
	t.Setenv("REDIS_URL", "not-a-redis-url")

	_, err := loadConfigFromEnv()
	if err == nil || !strings.Contains(err.Error(), "REDIS_URL") {
		t.Fatalf("invalid Redis URL: got %v, want a REDIS_URL configuration error", err)
	}
}
