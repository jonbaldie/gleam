package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

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
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotHost = r.Host
				w.Header().Set("Cache-Control", "no-store")
				_, _ = fmt.Fprint(w, "ok")
			}))
			defer origin.Close()

			originURL, err := url.Parse(origin.URL)
			if err != nil {
				t.Fatal(err)
			}
			handler := NewWithVaryHeaders(originURL, newMockCache(), time.Minute, nil)
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
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls++
		w.Header().Set("Cache-Control", "public, max-age=60")
		_, _ = fmt.Fprintf(w, "response-%d", originCalls)
	}))
	defer origin.Close()

	originURL, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	c := newMockCache()
	handler := NewWithVaryHeaders(originURL, c, time.Minute, nil)
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
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Cache-Control", "public, max-age=60")
			_, _ = fmt.Fprint(w, "cached response")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer origin.Close()

	originURL, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	c := newMockCache()
	handler := NewWithVaryHeaders(originURL, c, time.Minute, nil)
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
