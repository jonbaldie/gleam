package proxy

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewHandlerCachesInProcessUpstreamGET(t *testing.T) {
	var upstreamCalls int
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		w.Header().Set("Cache-Control", "max-age=60")
		_, _ = fmt.Fprintf(w, "response-%d", upstreamCalls)
	})
	handler := NewHandler(upstream, newMockCache(), time.Minute, DefaultVaryHeaders())

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/resource", nil))
		if got := rec.Body.String(); got != "response-1" {
			t.Fatalf("request %d body = %q, want %q", i+1, got, "response-1")
		}
	}
	if upstreamCalls != 1 {
		t.Fatalf("upstream calls = %d, want 1", upstreamCalls)
	}
}

func TestNewHandlerForwardsNonGETToInProcessUpstreamAndInvalidates(t *testing.T) {
	var methods []string
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Method == http.MethodGet {
			w.Header().Set("Cache-Control", "max-age=60")
		}
		_, _ = fmt.Fprint(w, r.Method)
	})
	c := newMockCache()
	handler := NewHandler(upstream, c, time.Minute, nil)
	get := httptest.NewRequest(http.MethodGet, "/resource", nil)
	handler.ServeHTTP(httptest.NewRecorder(), get)

	post := httptest.NewRecorder()
	handler.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/resource", nil))
	if got := post.Body.String(); got != http.MethodPost {
		t.Fatalf("POST body = %q, want %q", got, http.MethodPost)
	}
	if _, found := c.Get(cacheKeyForRequest(get)); found {
		t.Fatal("successful POST did not invalidate the cached GET")
	}
	if want := []string{http.MethodGet, http.MethodPost}; fmt.Sprint(methods) != fmt.Sprint(want) {
		t.Fatalf("upstream methods = %v, want %v", methods, want)
	}
}

func TestNewHandlerDelegatesHijackToClientWriter(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			var hijacked net.Conn
			upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hijacker, ok := w.(http.Hijacker)
				if !ok {
					t.Fatal("upstream response writer is not a hijacker")
				}
				conn, _, err := hijacker.Hijack()
				if err != nil {
					t.Fatalf("hijack failed: %v", err)
				}
				hijacked = conn
			})
			clientConn, proxyConn := net.Pipe()
			defer clientConn.Close()
			defer proxyConn.Close()

			handler := NewHandler(upstream, newMockCache(), time.Minute, nil)
			handler.ServeHTTP(&hijackRecorder{ResponseRecorder: httptest.NewRecorder(), conn: proxyConn}, httptest.NewRequest(method, "/socket", nil))

			if hijacked != proxyConn {
				t.Fatalf("upstream hijacked %v, want the client connection", hijacked)
			}
		})
	}
}

func TestNewHandlerReportsHijackUnsupportedByClientWriter(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			var hijackErr error
			upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _, hijackErr = w.(http.Hijacker).Hijack()
			})

			handler := NewHandler(upstream, newMockCache(), time.Minute, nil)
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, "/socket", nil))

			if hijackErr == nil {
				t.Fatal("expected hijack to fail when the client writer cannot be hijacked")
			}
		})
	}
}
