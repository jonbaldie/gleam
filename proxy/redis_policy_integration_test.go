package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jonbaldie/gleam/cache"
)

func TestRedisMetadataRespectsVaryAndAuthorization(t *testing.T) {
	redisURL := os.Getenv("GLEAM_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("set GLEAM_TEST_REDIS_URL to run the Redis integration test")
	}
	backend, err := cache.NewRedisCache(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	path := fmt.Sprintf("/policy-%d-%d", os.Getpid(), time.Now().UnixNano())
	t.Cleanup(func() { backend.InvalidatePrefix("example.com#" + path) })
	calls := 0
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Vary", "Accept-Language")
		w.Header().Set("ETag", `"`+r.Header.Get("Accept-Language")+`"`)
		_, _ = w.Write([]byte("body-" + r.Header.Get("Accept-Language")))
	})
	h := NewHandler(upstream, backend, time.Minute, []string{"Accept-Language"})
	request := func(language, validator, authorization string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "http://example.com"+path, nil)
		r.Header.Set("Accept-Language", language)
		if validator != "" {
			r.Header.Set("If-None-Match", validator)
		}
		if authorization != "" {
			r.Header.Set("Authorization", authorization)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("en", "", ""); w.Code != 200 || w.Body.String() != "body-en" {
		t.Fatalf("initial response = %d %q", w.Code, w.Body.String())
	}
	if w := request("en", `"en"`, ""); w.Code != 304 || w.Body.Len() != 0 || calls != 1 {
		t.Fatalf("matching response = %d %q, calls %d", w.Code, w.Body.String(), calls)
	}
	if w := request("fr", `"en"`, ""); w.Code != 200 || w.Body.String() != "body-fr" || calls != 2 {
		t.Fatalf("vary response = %d %q, calls %d", w.Code, w.Body.String(), calls)
	}
	if w := request("en", `"en"`, "Bearer token"); w.Code != 200 || w.Body.String() != "body-en" || calls != 3 {
		t.Fatalf("authorized response = %d %q, calls %d", w.Code, w.Body.String(), calls)
	}
}
