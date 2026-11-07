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
	"sync/atomic"
	"time"

	"github.com/jonbaldie/gleam/cache"
	"github.com/jonbaldie/gleam/proxy"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

func run() error {
	stall := &originStall{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	mem := cache.NewMemoryCache()
	defer mem.Close()
	handler := proxy.NewHandler(stall, mem, time.Minute, nil)

	firstDone := startGET(handler)
	if err := wait(stall.entered, "GET never reached the origin"); err != nil {
		return err
	}
	post := record(handler, http.MethodPost)
	if post.Code != http.StatusNoContent {
		return fmt.Errorf("POST status %d", post.Code)
	}
	close(stall.release)

	first, err := take(firstDone, "in-flight GET did not return")
	if err != nil {
		return err
	}
	if first.Body.String() != "pre-update" {
		return fmt.Errorf("in-flight GET body %q", first.Body.String())
	}

	later := record(handler, http.MethodGet)
	fmt.Printf("later GET body=%q originGETs=%d\n", later.Body.String(), stall.originGets.Load())
	return nil
}

type originStall struct {
	version    atomic.Int32
	originGets atomic.Int32
	entered    chan struct{}
	release    chan struct{}
}

func (s *originStall) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.writeGET(w)
	case http.MethodPost:
		s.version.Store(1)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
	}
}

func (s *originStall) writeGET(w http.ResponseWriter) {
	seen := s.version.Load()
	if s.originGets.Add(1) == 1 {
		close(s.entered)
		if err := wait(s.release, "in-flight GET stalled"); err != nil {
			http.Error(w, err.Error(), http.StatusGatewayTimeout)
			return
		}
	}
	body := "post-update"
	if seen == 0 {
		body = "pre-update"
	}
	_, _ = w.Write([]byte(body))
}

func startGET(handler http.Handler) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- record(handler, http.MethodGet)
	}()
	return done
}

func record(handler http.Handler, method string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(method, "http://example.test/resource", nil))
	return rec
}

func wait(ch <-chan struct{}, what string) error {
	select {
	case <-ch:
		return nil
	case <-time.After(2 * time.Second):
		return fmt.Errorf("%s", what)
	}
}

func take(ch <-chan *httptest.ResponseRecorder, what string) (*httptest.ResponseRecorder, error) {
	select {
	case rec := <-ch:
		return rec, nil
	case <-time.After(2 * time.Second):
		return nil, fmt.Errorf("%s", what)
	}
}
