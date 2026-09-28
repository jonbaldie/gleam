package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"
	"time"
)

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

// newInMemoryOriginAdapter builds the production reverse-proxy adapter for an
// origin whose traffic goes through transport.
func newInMemoryOriginAdapter(transport http.RoundTripper) (*httputil.ReverseProxy, *url.URL) {
	originURL := &url.URL{Scheme: "http", Host: "origin.internal:8081"}
	adapter := newOriginReverseProxy(originURL)
	adapter.Transport = transport
	return adapter, originURL
}

func TestProxyForwardsRequestsWithOriginHost(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		cacheBypass bool
	}{
		{name: "GET cache miss", method: http.MethodGet},
		{name: "GET cache bypass", method: http.MethodGet, cacheBypass: true},
		{name: "POST", method: http.MethodPost},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotHost string
			origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotHost = r.Host
				w.Header().Set("Cache-Control", "no-store")
				_, _ = fmt.Fprint(w, "ok")
			})

			adapter, originURL := newInMemoryOriginAdapter(handlerTransport(origin))
			handler := NewHandler(adapter, newMockCache(), time.Minute, nil)
			request := httptest.NewRequest(tt.method, "http://client.example:8080/x", nil)
			request.Host = "client.example:8080"
			if tt.cacheBypass {
				request.Header.Set("Cache-Control", "no-store")
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("response status = %d, want %d", response.Code, http.StatusOK)
			}
			if got := response.Body.String(); got != "ok" {
				t.Fatalf("response body = %q, want %q", got, "ok")
			}

			if gotHost != originURL.Host {
				t.Fatalf("origin Host = %q, want %q", gotHost, originURL.Host)
			}
		})
	}
}

func TestProxySeparatesCacheEntriesByInboundHost(t *testing.T) {
	var originCalls int
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls++
		w.Header().Set("Cache-Control", "public, max-age=60")
		_, _ = fmt.Fprintf(w, "response-%d", originCalls)
	})

	adapter, _ := newInMemoryOriginAdapter(handlerTransport(origin))
	c := newMockCache()
	handler := NewHandler(adapter, c, time.Minute, nil)
	requestA := httptest.NewRequest(http.MethodGet, "http://client-a.example/shared", nil)
	requestA.Host = "client-a.example"
	requestB := httptest.NewRequest(http.MethodGet, "http://client-b.example/shared", nil)
	requestB.Host = "client-b.example"

	responseA := httptest.NewRecorder()
	handler.ServeHTTP(responseA, requestA)
	responseB := httptest.NewRecorder()
	handler.ServeHTTP(responseB, requestB)

	if got, want := responseA.Body.String(), "response-1"; got != want {
		t.Fatalf("first response body = %q, want %q", got, want)
	}
	if got, want := responseB.Body.String(), "response-2"; got != want {
		t.Fatalf("second response body = %q, want %q", got, want)
	}
	if originCalls != 2 {
		t.Fatalf("origin calls = %d, want 2 for distinct inbound Hosts", originCalls)
	}
	keyA := cacheKeyForRequest(requestA)
	keyB := cacheKeyForRequest(requestB)
	if keyA == keyB {
		t.Fatalf("inbound Host values produced the same cache key %q", keyA)
	}
	if _, found := c.Get(keyA); !found {
		t.Fatalf("cache has no entry for inbound Host %q", requestA.Host)
	}
	if _, found := c.Get(keyB); !found {
		t.Fatalf("cache has no entry for inbound Host %q", requestB.Host)
	}
}

func TestProxyInvalidatesCacheEntriesByInboundHost(t *testing.T) {
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Cache-Control", "public, max-age=60")
			_, _ = fmt.Fprint(w, "cached response")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	adapter, _ := newInMemoryOriginAdapter(handlerTransport(origin))
	c := newMockCache()
	handler := NewHandler(adapter, c, time.Minute, nil)
	getRequest := httptest.NewRequest(http.MethodGet, "http://client.example/resource", nil)
	getRequest.Host = "client.example"
	handler.ServeHTTP(httptest.NewRecorder(), getRequest)

	key := cacheKeyForRequest(getRequest)
	if _, found := c.Get(key); !found {
		t.Fatal("expected GET response to be cached under the inbound Host")
	}

	postRequest := httptest.NewRequest(http.MethodPost, "http://client.example/resource", nil)
	postRequest.Host = "client.example"
	postResponse := httptest.NewRecorder()
	handler.ServeHTTP(postResponse, postRequest)
	if postResponse.Code != http.StatusNoContent {
		t.Fatalf("POST response status = %d, want %d", postResponse.Code, http.StatusNoContent)
	}
	if _, found := c.Get(key); found {
		t.Fatal("successful POST did not invalidate the inbound Host cache entry")
	}
}

// The reverse-proxy adapter tunnels a GET protocol upgrade between the client
// and the origin; the cache must neither answer nor break it.
func TestProxyGetProtocolUpgradeReachesOrigin(t *testing.T) {
	originConn, originPeer := net.Pipe()
	defer originPeer.Close()
	var originRequestHeader http.Header
	adapter, _ := newInMemoryOriginAdapter(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		originRequestHeader = r.Header.Clone()
		return &http.Response{
			StatusCode: http.StatusSwitchingProtocols,
			Header:     http.Header{"Connection": {"Upgrade"}, "Upgrade": {"websocket"}},
			Body:       originConn,
			Request:    r,
		}, nil
	}))
	handler := NewHandler(adapter, newMockCache(), time.Minute, nil)

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
