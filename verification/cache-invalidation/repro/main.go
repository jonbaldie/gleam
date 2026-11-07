// Replay the known finding on proxy.NewHandler and MemoryCache.
//
// The first GET reads the origin, then waits inside upstream.ServeHTTP.
// The POST commits and returns, so serveNonGet runs InvalidatePrefix while
// that GET is still upstream. The GET then stores the body it already read.
// The later GET is a hit when its body is the pre-update representation and
// the origin saw only one GET.
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jonbaldie/gleam/cache"
	"github.com/jonbaldie/gleam/proxy"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func run() error {
	var version atomic.Int32
	var originGets atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	var openOnce sync.Once

	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			seen := version.Load()
			if originGets.Add(1) == 1 {
				openOnce.Do(func() { close(entered) })
				select {
				case <-release:
				case <-time.After(2 * time.Second):
					http.Error(w, "in-flight GET stalled", http.StatusGatewayTimeout)
					return
				}
			}
			body := "post-update"
			if seen == 0 {
				body = "pre-update"
			}
			_, _ = w.Write([]byte(body))
		case http.MethodPost:
			version.Store(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	})

	mem := cache.NewMemoryCache()
	defer mem.Close()
	handler := proxy.NewHandler(origin, mem, time.Minute, nil)

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.test/resource", nil))
		firstDone <- rec
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		return fmt.Errorf("GET never reached the origin")
	}

	post := httptest.NewRecorder()
	handler.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "http://example.test/resource", nil))
	if post.Code != http.StatusNoContent {
		return fmt.Errorf("POST status %d", post.Code)
	}
	close(release)

	var first *httptest.ResponseRecorder
	select {
	case first = <-firstDone:
	case <-time.After(2 * time.Second):
		return fmt.Errorf("in-flight GET did not return")
	}
	if first.Body.String() != "pre-update" {
		return fmt.Errorf("in-flight GET body %q", first.Body.String())
	}

	later := httptest.NewRecorder()
	handler.ServeHTTP(later, httptest.NewRequest(http.MethodGet, "http://example.test/resource", nil))
	fmt.Printf("later GET body=%q originGETs=%d\n", later.Body.String(), originGets.Load())
	return nil
}
