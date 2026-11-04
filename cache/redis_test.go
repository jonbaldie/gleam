package cache

import "testing"

func TestNewRedisCacheParsesURL(t *testing.T) {
	tests := []struct {
		url, addr, username, password string
		db                            int
	}{
		{"redis://myuser:mypassword@myhost:1234/5", "myhost:1234", "myuser", "mypassword", 5},
		{"redis://localhost:6379/0", "localhost:6379", "", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			c, err := NewRedisCache(tt.url)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = c.Close() })
			opts := c.client.Options()
			if opts.Addr != tt.addr || opts.Username != tt.username || opts.Password != tt.password || opts.DB != tt.db {
				t.Fatalf("Redis options = %#v", opts)
			}
		})
	}
}

func TestNewRedisCacheRejectsInvalidURL(t *testing.T) {
	c, err := NewRedisCache("not-a-redis-url")
	if err == nil || c != nil {
		t.Fatalf("invalid Redis URL: cache = %v, error = %v", c, err)
	}
}
