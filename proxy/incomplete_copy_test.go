package proxy

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
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
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})

	c := newMockCache()
	handler := NewHandler(origin, c, time.Minute, DefaultVaryHeaders())

	handler.ServeHTTP(&failingWriter{}, httptest.NewRequest(http.MethodGet, "/big", nil))

	if len(c.store) != 0 {
		for key, entry := range c.store {
			t.Fatalf("expected nothing cached after a failed copy, got key %q with %d bytes", key, len(entry.item.Content))
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

// Under a real server the reverse proxy unwinds with http.ErrAbortHandler when
// its copy to the client fails; the truncated response must not be stored.
func TestProxyDoesNotCacheResponseWhenUpstreamAborts(t *testing.T) {
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "max-age=60")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		panic(http.ErrAbortHandler)
	})
	c := newMockCache()
	handler := NewHandler(origin, c, time.Minute, DefaultVaryHeaders())

	func() {
		defer func() {
			if rec := recover(); rec != http.ErrAbortHandler {
				t.Fatalf("recovered %v, want the upstream's http.ErrAbortHandler", rec)
			}
		}()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/big", nil))
	}()

	if len(c.store) != 0 {
		t.Fatalf("expected nothing cached after an aborted copy, got %d entries", len(c.store))
	}
}
