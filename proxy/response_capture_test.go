package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestUncacheableResponsesStreamWithoutCapture(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		header http.Header
	}{
		{"no-store", http.StatusOK, http.Header{"Cache-Control": {"no-store"}}},
		{"private", http.StatusOK, http.Header{"Cache-Control": {"private"}}},
		{"error", http.StatusInternalServerError, nil},
		{"cookie", http.StatusOK, http.Header{"Set-Cookie": {"a=b"}}},
		{"vary", http.StatusOK, http.Header{"Vary": {"Accept-Language"}}},
		{"not found", http.StatusNotFound, http.Header{"Cache-Control": {"public, max-age=60"}}},
		{"redirect", http.StatusFound, http.Header{"Cache-Control": {"public, max-age=60"}}},
		{"partial", http.StatusPartialContent, http.Header{"Cache-Control": {"public, max-age=60"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := bytes.Repeat([]byte("response chunk"), 8192)
			calls := 0
			handler := NewHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				for key, values := range tc.header {
					for _, value := range values {
						w.Header().Add(key, value)
					}
				}
				w.WriteHeader(tc.status)
				for _, chunk := range [][]byte{body[:len(body)/2], body[len(body)/2:]} {
					if _, err := w.Write(chunk); err != nil {
						t.Fatal(err)
					}
					if got := w.(*cacheResponseWriter).buf.Len(); got != 0 {
						t.Errorf("retained %d body bytes for an uncacheable response", got)
					}
				}
			}), newMockCache(), time.Minute, nil)
			for i := 0; i < 2; i++ {
				client := httptest.NewRecorder()
				handler.ServeHTTP(client, httptest.NewRequest(http.MethodGet, "/", nil))
				if client.Code != tc.status || !bytes.Equal(client.Body.Bytes(), body) {
					t.Fatal("response status or body changed")
				}
			}
			if calls != 2 {
				t.Fatalf("origin calls = %d, want 2", calls)
			}
		})
	}
}

func TestResponseCaptureTransitions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		write     func(*testing.T, *cacheResponseWriter)
		wantCalls int
		wantBody  string
	}{
		{"late header", func(t *testing.T, w *cacheResponseWriter) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("a"))
			w.Header().Set("Cache-Control", "max-age=60")
			_, _ = w.Write([]byte("b"))
		}, 1, "ab"},
		{"release rejected body", func(t *testing.T, w *cacheResponseWriter) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(bytes.Repeat([]byte("a"), 65536))
			if w.buf.Cap() == 0 {
				t.Fatal("expected captured allocation")
			}
			w.Header().Set("Set-Cookie", "a=b")
			_, _ = w.Write([]byte("b"))
			if w.buf.Len() != 0 || w.buf.Cap() != 0 {
				t.Fatal("buffer allocation was not released")
			}
		}, 2, string(bytes.Repeat([]byte("a"), 65536)) + "b"},
		{"rejected then storable", func(t *testing.T, w *cacheResponseWriter) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("a"))
			w.Header().Set("Cache-Control", "max-age=60")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("b"))
		}, 2, "ab"},
		{"empty write", func(t *testing.T, w *cacheResponseWriter) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write(nil)
			w.Header().Set("Cache-Control", "max-age=60")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ab"))
		}, 1, "ab"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h := NewHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				tc.write(t, w.(*cacheResponseWriter))
			}), newMockCache(), time.Minute, nil)
			for i := 0; i < 2; i++ {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
				if rec.Body.String() != tc.wantBody {
					t.Fatalf("response body differs from expected")
				}
			}
			if calls != tc.wantCalls {
				t.Fatalf("calls = %d, want %d", calls, tc.wantCalls)
			}
		})
	}
}

func TestAuthenticatedResponseCapture(t *testing.T) {
	for _, tc := range []struct {
		directive               string
		wantCalls, wantCaptured int
	}{
		{"max-age=60", 2, 0}, {"public, max-age=60", 1, 2},
	} {
		t.Run(tc.directive, func(t *testing.T) {
			calls := 0
			h := NewHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Cache-Control", tc.directive)
				_, _ = w.Write([]byte("a"))
				_, _ = w.Write([]byte("b"))
				if got := w.(*cacheResponseWriter).buf.Len(); got != tc.wantCaptured {
					t.Errorf("captured %d, want %d", got, tc.wantCaptured)
				}
			}), newMockCache(), time.Minute, nil)
			for i := 0; i < 2; i++ {
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.Header.Set("Authorization", "Bearer token")
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Body.String() != "ab" {
					t.Fatalf("body = %q", rec.Body.String())
				}
			}
			if calls != tc.wantCalls {
				t.Fatalf("calls = %d, want %d", calls, tc.wantCalls)
			}
		})
	}
}

func TestEarlyHintsThenFinalResponseCapture(t *testing.T) {
	for _, tc := range []struct {
		directive string
		wantCalls int32
	}{{"max-age=60", 1}, {"no-store", 2}} {
		t.Run(tc.directive, func(t *testing.T) {
			var calls int32
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				atomic.AddInt32(&calls, 1)
				w.Header().Set("Link", "</style.css>; rel=preload")
				w.WriteHeader(http.StatusEarlyHints)
				w.Header().Del("Link")
				w.Header().Set("Cache-Control", tc.directive)
				_, _ = w.Write([]byte("response"))
			}))
			defer origin.Close()
			u, err := url.Parse(origin.URL)
			if err != nil {
				t.Fatal(err)
			}
			front := httptest.NewServer(NewHandler(NewOriginReverseProxy(u, nil), newMockCache(), time.Minute, nil))
			defer front.Close()
			for i := 0; i < 2; i++ {
				var hints int
				trace := &httptrace.ClientTrace{Got1xxResponse: func(code int, header textproto.MIMEHeader) error {
					if code == http.StatusEarlyHints && header.Get("Link") == "</style.css>; rel=preload" {
						hints++
					}
					return nil
				}}
				req, err := http.NewRequest(http.MethodGet, front.URL, nil)
				if err != nil {
					t.Fatal(err)
				}
				resp, err := front.Client().Do(req.WithContext(httptrace.WithClientTrace(req.Context(), trace)))
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil || resp.StatusCode != http.StatusOK || string(body) != "response" {
					t.Fatalf("response: status %d, body %q, error %v", resp.StatusCode, body, err)
				}
				wantHints := 1
				if i == 1 && tc.wantCalls == 1 {
					wantHints = 0
				}
				if hints != wantHints {
					t.Fatalf("early hints = %d, want %d", hints, wantHints)
				}
			}
			if got := atomic.LoadInt32(&calls); got != tc.wantCalls {
				t.Fatalf("calls = %d, want %d", got, tc.wantCalls)
			}
		})
	}
}
