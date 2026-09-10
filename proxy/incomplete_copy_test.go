package proxy

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

// failingWriter fails the first body write, simulating a downstream client
// that disconnects mid-copy.
type failingWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
	writes int
}

func (w *failingWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}

func (w *failingWriter) WriteHeader(status int) { w.status = status }

func (w *failingWriter) Write(b []byte) (int, error) {
	w.writes++
	if w.writes == 1 {
		return 0, errors.New("client disconnected")
	}
	return w.body.Write(b)
}

func TestProxyDoesNotCacheResponseAfterDownstreamWriteError(t *testing.T) {
	body := bytes.Repeat([]byte("a"), 64*1024)
	var originCalls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer origin.Close()

	originURL, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatalf("failed to parse origin URL: %v", err)
	}

	c := newMockCache()
	handler := New(originURL, c, time.Minute)

	func() {
		defer func() {
			// A real server context turns a copy error into ErrAbortHandler;
			// either unwind is acceptable, neither may cache.
			if rec := recover(); rec != nil && rec != http.ErrAbortHandler {
				panic(rec)
			}
		}()
		handler.ServeHTTP(&failingWriter{}, httptest.NewRequest(http.MethodGet, "/big", nil))
	}()

	if len(c.store) != 0 {
		for key, item := range c.store {
			t.Fatalf("expected nothing cached after a failed copy, got key %q with %d bytes", key, len(item.Content))
		}
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/big", nil))
	if rec.Body.Len() != len(body) {
		t.Fatalf("expected a complete %d-byte response, got %d bytes", len(body), rec.Body.Len())
	}
	if originCalls.Load() != 2 {
		t.Fatalf("expected the second GET to reach the origin, origin calls = %d", originCalls.Load())
	}
}
