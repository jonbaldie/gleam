package proxy_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"jonbaldie/gleam/proxy"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestOriginTransportPreservesRequestBoundaries(t *testing.T) {
	t.Parallel()
	origin := &url.URL{Scheme: "https", Host: "origin.internal:8443", Path: "/base", RawQuery: "origin=1"}
	var outbound *http.Request
	transport := transportFunc(func(r *http.Request) (*http.Response, error) {
		outbound = r
		return &http.Response{
			StatusCode: http.StatusCreated,
			Header:     http.Header{"X-Origin": {"custom transport"}},
			Body:       io.NopCloser(strings.NewReader("custom response")),
			Request:    r,
		}, nil
	})
	handler := proxy.NewOriginReverseProxy(origin, transport)
	request := httptest.NewRequest(http.MethodPost, "http://client.example/item?client=2", strings.NewReader("payload"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated || response.Body.String() != "custom response" || response.Header().Get("X-Origin") != "custom transport" {
		t.Fatalf("response = %d %q %v, want 201 custom response with X-Origin", response.Code, response.Body.String(), response.Header())
	}
	if outbound == nil {
		t.Fatal("custom transport received no request")
	}
	if outbound.Method != http.MethodPost || outbound.Host != "origin.internal:8443" || outbound.URL.String() != "https://origin.internal:8443/base/item?origin=1&client=2" {
		t.Fatalf("outbound request = %s %s Host=%s", outbound.Method, outbound.URL, outbound.Host)
	}
	if request.Host != "client.example" || request.URL.String() != "http://client.example/item?client=2" {
		t.Fatalf("inbound request changed to %s Host=%s", request.URL, request.Host)
	}
}
