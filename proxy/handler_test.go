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

func TestNewHandlerCachesGETAndInvalidatesOnPOST(t *testing.T) {
	var getCalls atomic.Int32
	var postCalls atomic.Int32

	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			call := getCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, "content-%d", call)
		case http.MethodPost:
			postCalls.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected method: %s", r.Method)
		}
	})

	c := newMockCache()
	handler := NewHandler(upstream, c, time.Minute, defaultVaryHeaders)

	// First GET populates cache
	req := httptest.NewRequest(http.MethodGet, "/item", nil)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, req)
	if first.Code != http.StatusOK || first.Body.String() != "content-1" {
		t.Fatalf("first GET: status=%d, body=%q, want 200 content-1", first.Code, first.Body.String())
	}
	if getCalls.Load() != 1 {
		t.Fatalf("expected 1 upstream GET call, got %d", getCalls.Load())
	}

	// Second GET served from cache
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, req)
	if second.Code != http.StatusOK || second.Body.String() != "content-1" {
		t.Fatalf("second GET: status=%d, body=%q, want 200 content-1", second.Code, second.Body.String())
	}
	if getCalls.Load() != 1 {
		t.Fatalf("expected cached GET not to call upstream again, got %d calls", getCalls.Load())
	}

	// POST invalidates cache
	postReq := httptest.NewRequest(http.MethodPost, "/item", nil)
	postRec := httptest.NewRecorder()
	handler.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusNoContent {
		t.Fatalf("POST: status=%d, want 204", postRec.Code)
	}
	if postCalls.Load() != 1 {
		t.Fatalf("expected 1 upstream POST call, got %d", postCalls.Load())
	}

	// Third GET reaches upstream because cache was invalidated
	third := httptest.NewRecorder()
	handler.ServeHTTP(third, req)
	if third.Code != http.StatusOK || third.Body.String() != "content-2" {
		t.Fatalf("third GET: status=%d, body=%q, want 200 content-2", third.Code, third.Body.String())
	}
	if getCalls.Load() != 2 {
		t.Fatalf("expected upstream GET after invalidation, got %d calls", getCalls.Load())
	}
}

func TestNewHandlerWithVaryHeaders(t *testing.T) {
	var calls atomic.Int32
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		w.Header().Set("Vary", "X-Role")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "role-%s-%d", r.Header.Get("X-Role"), call)
	})

	c := newMockCache()
	handler := NewHandler(upstream, c, time.Minute, []string{"X-Role"})

	req1 := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req1.Header.Set("X-Role", "admin")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Body.String() != "role-admin-1" {
		t.Fatalf("got body %q, want role-admin-1", rec1.Body.String())
	}

	// Repeated with same vary header: hit
	rec1Cached := httptest.NewRecorder()
	handler.ServeHTTP(rec1Cached, req1)
	if rec1Cached.Body.String() != "role-admin-1" {
		t.Fatalf("got cached body %q, want role-admin-1", rec1Cached.Body.String())
	}

	// Different vary header: miss
	req2 := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req2.Header.Set("X-Role", "viewer")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Body.String() != "role-viewer-2" {
		t.Fatalf("got body %q, want role-viewer-2", rec2.Body.String())
	}

	if calls.Load() != 2 {
		t.Fatalf("expected 2 calls, got %d", calls.Load())
	}
}

func TestNewDelegatesToNewHandler(t *testing.T) {
	var calls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("new-ok"))
	}))
	defer origin.Close()

	originURL, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatalf("parse origin URL: %v", err)
	}

	c := newMockCache()
	handler := New(originURL, c, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/test-new", nil)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req)
	if rec1.Body.String() != "new-ok" {
		t.Fatalf("first GET body = %q, want new-ok", rec1.Body.String())
	}

	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req)
	if rec2.Body.String() != "new-ok" {
		t.Fatalf("second GET body = %q, want new-ok", rec2.Body.String())
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected 1 call (cached), got %d", got)
	}
}

func TestNewWithVaryHeadersDelegatesToNewHandler(t *testing.T) {
	var calls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Vary", "X-Custom")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "val-%s", r.Header.Get("X-Custom"))
	}))
	defer origin.Close()

	originURL, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatalf("parse origin URL: %v", err)
	}

	c := newMockCache()
	handler := NewWithVaryHeaders(originURL, c, time.Minute, []string{"X-Custom"})

	req1 := httptest.NewRequest(http.MethodGet, "/test-vary", nil)
	req1.Header.Set("X-Custom", "A")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Body.String() != "val-A" {
		t.Fatalf("first GET body = %q, want val-A", rec1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodGet, "/test-vary", nil)
	req2.Header.Set("X-Custom", "B")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Body.String() != "val-B" {
		t.Fatalf("second GET body = %q, want val-B", rec2.Body.String())
	}

	if got := calls.Load(); got != 2 {
		t.Fatalf("expected 2 calls, got %d", got)
	}
}
