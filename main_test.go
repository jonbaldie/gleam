package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSimpleCache(t *testing.T) {
	cache := NewSimpleCache()

	// Test Set and Get
	cache.Set("key1", []byte("value1"), http.Header{}, 1*time.Minute)
	item, found := cache.Get("key1")
	if !found {
		t.Error("Expected to find key1 in cache")
	}
	if string(item.content) != "value1" {
		t.Errorf("Expected value1, got %s", string(item.content))
	}

	// Test expiration
	cache.Set("key2", []byte("value2"), http.Header{}, 1*time.Nanosecond)
	time.Sleep(1 * time.Millisecond)
	_, found = cache.Get("key2")
	if found {
		t.Error("Expected key2 to be expired")
	}
}

func TestCacheResponseWriter(t *testing.T) {
	w := httptest.NewRecorder()
	crw := &CacheResponseWriter{ResponseWriter: w, buf: new(bytes.Buffer)}

	crw.WriteHeader(http.StatusOK)
	crw.Write([]byte("Hello, World!"))

	if crw.status != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, crw.status)
	}

	if crw.buf.String() != "Hello, World!" {
		t.Errorf("Expected 'Hello, World!', got '%s'", crw.buf.String())
	}
}

func TestLoadConfig(t *testing.T) {
	// Save current env vars
	oldOriginURL := os.Getenv("ORIGIN_URL")
	oldTTLMinutes := os.Getenv("TTL_MINUTES")
	oldPort := os.Getenv("PORT")

	// Set test env vars
	os.Setenv("ORIGIN_URL", "https://example.com")
	os.Setenv("TTL_MINUTES", "10")
	os.Setenv("PORT", "9090")

	config := loadConfig()

	if config.OriginURL != "https://example.com" {
		t.Errorf("Expected OriginURL to be https://example.com, got %s", config.OriginURL)
	}
	if config.TTL != 10*time.Minute {
		t.Errorf("Expected TTL to be 10 minutes, got %v", config.TTL)
	}
	if config.Port != "9090" {
		t.Errorf("Expected Port to be 9090, got %s", config.Port)
	}

	// Restore original env vars
	os.Setenv("ORIGIN_URL", oldOriginURL)
	os.Setenv("TTL_MINUTES", oldTTLMinutes)
	os.Setenv("PORT", oldPort)
}

func TestParseOriginURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{
			name:  "valid https origin",
			input: "https://example.com",
			want:  "https://example.com",
		},
		{
			name:    "invalid control character",
			input:   "https://example.com/\tbad",
			wantErr: "invalid ORIGIN_URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origin, err := parseOriginURL(tt.input)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := origin.String(); got != tt.want {
				t.Fatalf("expected origin %q, got %q", tt.want, got)
			}
		})
	}
}

func TestParseOriginURLRejectsMissingScheme(t *testing.T) {
	_, err := parseOriginURL("example.com")
	if err == nil {
		t.Fatal("expected missing-scheme origin to fail")
	}
	if !strings.Contains(err.Error(), "must include http or https scheme") {
		t.Fatalf("unexpected error: %v", err)
	}
}
