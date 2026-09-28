package proxy

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// recordGET sends a GET through handler in-process and returns the recorded
// response, with any trailers the handler set after the body.
func recordGET(handler http.Handler, target string) *http.Response {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec.Result()
}

// flushRecorder records a response and captures the body the client had
// received each time the response was flushed.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes []string
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (w *flushRecorder) Flush() {
	w.ResponseRecorder.Flush()
	w.flushes = append(w.flushes, w.Body.String())
}

// firstBodyFlush returns the body the client had received at the first flush
// that delivered any of it; a flush of the headers alone does not count.
func (w *flushRecorder) firstBodyFlush(t *testing.T) string {
	t.Helper()
	for _, body := range w.flushes {
		if body != "" {
			return body
		}
	}
	t.Fatalf("no flush delivered body to the client writer (flushes %q)", w.flushes)
	return ""
}

// hijackRecorder is a client response writer whose connection is one end of
// an in-memory pipe.
type hijackRecorder struct {
	*httptest.ResponseRecorder
	conn net.Conn
}

func (w *hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}
