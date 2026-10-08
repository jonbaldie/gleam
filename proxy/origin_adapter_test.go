package proxy

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The origin that NewOriginReverseProxy proxies to in these tests. Its traffic
// never leaves the process: the transport each test passes serves it.
var testOriginURL = &url.URL{Scheme: "http", Host: "origin.internal:8081"}

// roundTripFunc lets the reverse-proxy adapter reach an in-process origin
// instead of a network listener.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// handlerTransport serves the adapter's outbound requests with origin.
func handlerTransport(origin http.Handler) http.RoundTripper {
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Body == nil {
			r.Body = http.NoBody
		}
		rec := httptest.NewRecorder()
		origin.ServeHTTP(rec, r)
		response := rec.Result()
		response.Request = r
		return response, nil
	})
}

func TestNewSeparatesCachedGetsByDefaultVaryHeaders(t *testing.T) {
	t.Parallel()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=60")
		_, _ = w.Write([]byte(r.Header.Get("Authorization")))
	}))
	defer origin.Close()
	originURL, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatalf("parse origin URL: %v", err)
	}
	handler := New(originURL, newMockCache(), time.Minute)

	for _, credentials := range []string{"Bearer alpha", "Bearer beta"} {
		req := httptest.NewRequest(http.MethodGet, "/profile", nil)
		req.Header.Set("Authorization", credentials)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if got := rec.Body.String(); got != credentials {
			t.Fatalf("body for %q = %q, want the origin's response for that caller", credentials, got)
		}
	}
}

// streamingOrigin answers through the adapter with an unknown-length body
// that it writes in two chunks. The pipe blocks each write until the adapter
// reads it, so the adapter must deliver and flush the first chunk before it
// can read the second.
func streamingOrigin() http.RoundTripper {
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, writer := io.Pipe()
		go func() {
			_, _ = writer.Write([]byte("first\n"))
			_, _ = writer.Write([]byte("second\n"))
			_ = writer.Close()
		}()
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"Content-Type": {"text/plain"}},
			Body:          body,
			ContentLength: -1,
			Request:       r,
		}, nil
	})
}

func TestProxyAdapterStreamsFirstChunkBeforeOriginFinishes(t *testing.T) {
	t.Parallel()
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			handler := NewHandler(NewOriginReverseProxy(testOriginURL, streamingOrigin()), newMockCache(), time.Minute, defaultVaryHeaders)
			client := newFlushRecorder()
			handler.ServeHTTP(client, httptest.NewRequest(method, "/stream", strings.NewReader("payload")))

			if got := client.firstBodyFlush(t); got != "first\n" {
				t.Fatalf("body delivered at first flush = %q, want %q", got, "first\n")
			}
			if got := client.Body.String(); got != "first\nsecond\n" {
				t.Fatalf("body = %q, want %q", got, "first\nsecond\n")
			}
		})
	}
}

// The reverse-proxy adapter tunnels a GET protocol upgrade between the client
// and the origin; the cache must neither answer nor break it.
func TestProxyGetProtocolUpgradeReachesOrigin(t *testing.T) {
	t.Parallel()
	originConn, originPeer := net.Pipe()
	defer originPeer.Close()
	var originRequestHeader http.Header
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		originRequestHeader = r.Header.Clone()
		return &http.Response{
			StatusCode: http.StatusSwitchingProtocols,
			Header:     http.Header{"Connection": {"Upgrade"}, "Upgrade": {"websocket"}},
			Body:       originConn,
			Request:    r,
		}, nil
	})
	handler := NewHandler(NewOriginReverseProxy(testOriginURL, transport), newMockCache(), time.Minute, defaultVaryHeaders)

	clientConn, proxyConn := net.Pipe()
	defer clientConn.Close()
	req := httptest.NewRequest(http.MethodGet, "/socket", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	served := make(chan struct{})
	go func() {
		defer close(served)
		handler.ServeHTTP(&hijackRecorder{ResponseRecorder: httptest.NewRecorder(), conn: proxyConn}, req)
	}()

	clientReader := bufio.NewReader(clientConn)
	resp, err := http.ReadResponse(clientReader, req)
	if err != nil {
		t.Fatalf("read upgrade response: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status = %d, want %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}
	if originRequestHeader.Get("Connection") != "Upgrade" || originRequestHeader.Get("Upgrade") != "websocket" {
		t.Fatalf("origin did not receive upgrade request: %v", originRequestHeader)
	}

	go func() { _, _ = clientConn.Write([]byte("ping")) }()
	if got := readN(t, originPeer, 4); got != "ping" {
		t.Fatalf("origin received %q through the tunnel, want %q", got, "ping")
	}
	go func() { _, _ = originPeer.Write([]byte("pong")) }()
	if got := readN(t, clientReader, 4); got != "pong" {
		t.Fatalf("client received %q through the tunnel, want %q", got, "pong")
	}

	_ = clientConn.Close()
	_ = originPeer.Close()
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("proxy did not finish serving after both tunnel ends closed")
	}
}

func readN(t *testing.T, r io.Reader, n int) string {
	t.Helper()
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatalf("read %d bytes: %v", n, err)
	}
	return string(buf)
}

func TestNewOriginReverseProxySendsOriginRequestsThroughTransport(t *testing.T) {
	t.Parallel()
	var originPath string
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originPath = r.URL.Path
		_, _ = w.Write([]byte("from transport"))
	})
	handler := NewOriginReverseProxy(testOriginURL, handlerTransport(origin))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/via-transport", nil))

	if got := rec.Body.String(); got != "from transport" {
		t.Fatalf("body = %q, want %q", got, "from transport")
	}
	if originPath != "/via-transport" {
		t.Fatalf("origin path = %q, want %q", originPath, "/via-transport")
	}
}
