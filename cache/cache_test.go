package cache

import "testing"

func TestMatchesPrefix(t *testing.T) {
	tests := []struct {
		name   string
		key    string
		prefix string
		want   bool
	}{
		{"exact key", "example.com#/resource", "example.com#/resource", true},
		{"vary variant", "example.com#/resource#h=abc123", "example.com#/resource", true},
		{"other URI", "example.com#/other", "example.com#/resource", false},
		{"URI sharing the prefix string", "example.com#/resource2", "example.com#/resource", false},
		{"URI sharing the prefix plus a query", "example.com#/resource?x=1", "example.com#/resource", false},
		{"other host", "other.com#/resource", "example.com#/resource", false},
		{"hash suffix on other URI", "example.com#/other#h=abc123", "example.com#/resource", false},
		{"empty prefix", "example.com#/resource", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MatchesPrefix(tt.key, tt.prefix); got != tt.want {
				t.Errorf("MatchesPrefix(%q, %q) = %v, want %v", tt.key, tt.prefix, got, tt.want)
			}
		})
	}
}