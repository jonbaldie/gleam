package proxy

import (
	"fmt"
	"github.com/jonbaldie/gleam/cache"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countingWriter struct {
	writer io.Writer
	bytes  *atomic.Int64
}

func (w countingWriter) Write(p []byte) (int, error) {
	w.bytes.Add(int64(len(p)))
	return w.writer.Write(p)
}

func countRedisTraffic(t *testing.T, rawURL string) (string, *atomic.Int64) {
	t.Helper()
	address, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	remote := address.Host
	var bytes atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			server, err := net.Dial("tcp", remote)
			if err != nil {
				_ = client.Close()
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer client.Close()
				defer server.Close()
				go func() {
					_, _ = io.Copy(server, client)
					_ = server.Close()
				}()
				_, _ = io.Copy(countingWriter{client, &bytes}, server)
			}()
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); wg.Wait() })
	address.Host = listener.Addr().String()
	return address.String(), &bytes
}

func TestRedisConditionalHitDoesNotTransferBody(t *testing.T) {
	redisURL := os.Getenv("GLEAM_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("set GLEAM_TEST_REDIS_URL to run the Redis integration test")
	}
	redisURL, outputBytes := countRedisTraffic(t, redisURL)
	backend, err := cache.NewRedisCache(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })

	for _, size := range []int{1024, 1 << 20} {
		t.Run(fmt.Sprintf("body-%d", size), func(t *testing.T) {
			path := fmt.Sprintf("/conditional-%d-%d-%d", os.Getpid(), time.Now().UnixNano(), size)
			t.Cleanup(func() { backend.InvalidatePrefix("example.com#" + path) })
			originCalls := 0
			upstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				originCalls++
				w.Header().Set("ETag", `"v1"`)
				w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
				w.Header().Set("Trailer", "X-Finished")
				_, _ = w.Write([]byte(strings.Repeat("x", size)))
				w.Header().Set("X-Finished", "yes")
			})
			handler := NewHandler(upstream, backend, time.Minute, nil)
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://example.com"+path, nil))
			for _, validator := range []struct{ name, value string }{
				{"If-None-Match", `"v1"`},
				{"If-Modified-Since", "Wed, 21 Oct 2015 07:28:00 GMT"},
			} {
				before := outputBytes.Load()
				req := httptest.NewRequest(http.MethodGet, "http://example.com"+path, nil)
				if validator.name == "If-None-Match" {
					req.Header.Add(validator.name, "")
				}
				req.Header.Add(validator.name, validator.value)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, req)
				delta := outputBytes.Load() - before
				if response.Code != http.StatusNotModified || response.Body.Len() != 0 || response.Header().Get("Etag") != `"v1"` || response.Header().Get("Age") == "" || originCalls != 1 {
					t.Fatalf("%s response status=%d body=%d headers=%v origin calls=%d", validator.name, response.Code, response.Body.Len(), response.Header(), originCalls)
				}
				t.Logf("%s Redis output for %d-byte cached body: %d bytes", validator.name, size, delta)
				if delta > 20<<10 {
					t.Fatalf("Redis transferred %d bytes for a conditional hit", delta)
				}
			}
			for _, validator := range []string{"", `"other"`} {
				req := httptest.NewRequest(http.MethodGet, "http://example.com"+path, nil)
				if validator != "" {
					req.Header.Set("If-None-Match", validator)
					req.Header.Set("If-Modified-Since", "Wed, 21 Oct 2015 07:28:00 GMT")
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, req)
				result := response.Result()
				if response.Code != http.StatusOK || response.Body.String() != strings.Repeat("x", size) || result.Trailer.Get("X-Finished") != "yes" || originCalls != 1 {
					t.Fatalf("full response status=%d body=%d trailer=%v origin calls=%d", response.Code, response.Body.Len(), result.Trailer, originCalls)
				}
			}
		})
	}
}
