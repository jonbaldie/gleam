package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestProxyForwardsRequestsWithOriginHost(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
			var gotHost string
			origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotHost = r.Host
				w.Header().Set("Cache-Control", "no-store")
				_, _ = fmt.Fprint(w, "ok")
			})

			handler := NewHandler(NewOriginReverseProxy(testOriginURL, handlerTransport(origin)), newMockCache(), time.Minute, nil)
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

			if gotHost != testOriginURL.Host {
				t.Fatalf("origin Host = %q, want %q", gotHost, testOriginURL.Host)
			}
		})
	}
}

func TestProxySeparatesCacheEntriesByInboundHost(t *testing.T) {
	t.Parallel()
	var originCalls int
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls++
		w.Header().Set("Cache-Control", "public, max-age=60")
		_, _ = fmt.Fprintf(w, "response-%d", originCalls)
	})

	c := newMockCache()
	handler := NewHandler(NewOriginReverseProxy(testOriginURL, handlerTransport(origin)), c, time.Minute, nil)
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
	t.Parallel()
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Cache-Control", "public, max-age=60")
			_, _ = fmt.Fprint(w, "cached response")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	c := newMockCache()
	handler := NewHandler(NewOriginReverseProxy(testOriginURL, handlerTransport(origin)), c, time.Minute, nil)
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

func TestDefaultTransportPreservesHostScopedCache(t *testing.T) {
	t.Parallel()
	constructors := []struct {
		name string
		new  func(*url.URL) http.Handler
	}{
		{"New", func(origin *url.URL) http.Handler {
			return New(origin, newMockCache(), time.Minute)
		}},
		{"NewWithVaryHeaders", func(origin *url.URL) http.Handler {
			return NewWithVaryHeaders(origin, newMockCache(), time.Minute, DefaultVaryHeaders())
		}},
		{"NewOriginReverseProxy", func(origin *url.URL) http.Handler {
			return NewHandler(NewOriginReverseProxy(origin, nil), newMockCache(), time.Minute, DefaultVaryHeaders())
		}},
	}
	for _, constructor := range constructors {
		t.Run(constructor.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.Header().Set("Cache-Control", "public, max-age=60")
				_, _ = fmt.Fprintf(w, "%s response-%d", r.Host, calls.Add(1))
			}))
			defer origin.Close()
			originURL, err := url.Parse(origin.URL)
			if err != nil {
				t.Fatalf("parse origin URL: %v", err)
			}
			handler := constructor.new(originURL)
			steps := []struct {
				method      string
				host        string
				credentials string
				cookie      string
				status      int
				body        string
			}{
				{http.MethodGet, "client-a.example", "Bearer alpha", "first", 200, "response-1"},
				{http.MethodGet, "client-a.example", "Bearer alpha", "first", 200, "response-1"},
				{http.MethodGet, "client-b.example", "Bearer alpha", "first", 200, "response-2"},
				{http.MethodGet, "client-a.example", "Bearer beta", "first", 200, "response-3"},
				{http.MethodGet, "client-a.example", "Bearer beta", "second", 200, "response-4"},
				{http.MethodPost, "client-a.example", "", "", 204, ""},
				{http.MethodGet, "client-b.example", "Bearer alpha", "first", 200, "response-2"},
				{http.MethodGet, "client-a.example", "Bearer alpha", "first", 200, "response-5"},
			}
			for i, step := range steps {
				request := httptest.NewRequest(step.method, "http://"+step.host+"/shared", nil)
				request.Header.Set("Authorization", step.credentials)
				request.Header.Set("Cookie", step.cookie)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				wantBody := step.body
				if wantBody != "" {
					wantBody = originURL.Host + " " + wantBody
				}
				if response.Code != step.status || response.Body.String() != wantBody {
					t.Fatalf("step %d response = %d %q, want %d %q", i, response.Code, response.Body.String(), step.status, wantBody)
				}
				if request.Host != step.host {
					t.Fatalf("step %d inbound Host = %q, want %q", i, request.Host, step.host)
				}
			}
		})
	}
}
