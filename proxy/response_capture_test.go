package proxy

import (
	"bytes"
	"net/http"
	"net/http/httptest"
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
