package main

import (
	"fmt"
	"net/http"
	"time"

	"jonbaldie/gleam/cachepolicy"
)

func main() {
	receivedAt := time.Date(2026, 10, 3, 2, 0, 0, 0, time.UTC)
	defaultTTL := 5 * time.Minute

	// Test 1: Bare max-age (without =)
	h1 := http.Header{"Cache-Control": []string{"public, max-age"}}
	ttl1, store1 := cachepolicy.FreshnessLifetime(h1, receivedAt, defaultTTL)
	fmt.Printf("1. Bare max-age: ttl=%v, store=%v (want: store=false, ttl=0)\n", ttl1, store1)

	// Test 2: Bare s-maxage (without =)
	h2 := http.Header{"Cache-Control": []string{"public, s-maxage"}}
	ttl2, store2 := cachepolicy.FreshnessLifetime(h2, receivedAt, defaultTTL)
	fmt.Printf("2. Bare s-maxage: ttl=%v, store=%v (want: store=false, ttl=0)\n", ttl2, store2)

	// Test 3: Bare s-maxage followed by max-age=30
	h3 := http.Header{"Cache-Control": []string{"s-maxage, max-age=30"}}
	ttl3, store3 := cachepolicy.FreshnessLifetime(h3, receivedAt, defaultTTL)
	fmt.Printf("3. Bare s-maxage before max-age: ttl=%v, store=%v (want: store=false, ttl=0)\n", ttl3, store3)

	// Test 4: Multiple Date headers
	h4 := http.Header{
		"Date":          []string{"Sat, 03 Oct 2026 01:00:00 GMT", "Sat, 03 Oct 2026 02:00:00 GMT"},
		"Cache-Control": []string{"public, max-age=60"},
	}
	ttl4, store4 := cachepolicy.FreshnessLifetime(h4, receivedAt, defaultTTL)
	fmt.Printf("4. Multiple Date headers with max-age=60: ttl=%v, store=%v\n", ttl4, store4)
	age4 := cachepolicy.CurrentAge(h4, receivedAt, receivedAt.Add(5*time.Second))
	fmt.Printf("   CurrentAge with multiple Date: age=%d (if Date ignored, apparentAge=0 so age=5)\n", age4)

	// Test 5: Multiple Age headers
	h5 := http.Header{
		"Date":          []string{"Sat, 03 Oct 2026 02:00:00 GMT"},
		"Age":           []string{"100", "200"},
		"Cache-Control": []string{"public, max-age=60"},
	}
	ttl5, store5 := cachepolicy.FreshnessLifetime(h5, receivedAt, defaultTTL)
	fmt.Printf("5. Multiple Age headers: ttl=%v, store=%v\n", ttl5, store5)

	// Test 6: Multiple Last-Modified headers in stored response
	reqH := http.Header{"If-Modified-Since": []string{"Sat, 03 Oct 2026 02:00:00 GMT"}}
	storedH := http.Header{
		"Last-Modified": []string{"Sat, 03 Oct 2026 01:00:00 GMT", "Sat, 03 Oct 2026 03:00:00 GMT"},
	}
	notMod := cachepolicy.NotModified(reqH, http.StatusOK, storedH)
	fmt.Printf("6. Multiple Last-Modified headers: NotModified=%v (RFC 9110 §8.8.2 says MUST ignore Last-Modified if >1 member, so should be false/ignored)\n", notMod)
}
