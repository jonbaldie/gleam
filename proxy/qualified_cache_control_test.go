package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestBugHuntQualifiedPrivateFieldStored reproduces jonbaldie/gleam#81: a
// shared cache must not store the fields a qualified `private="..."` lists
// (RFC 9111 section 5.2.2.7).
func TestBugHuntQualifiedPrivateFieldStored(t *testing.T) {
	var calls int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		w.Header().Set("Cache-Control", `private="X-User-Id"`)
		w.Header().Set("X-User-Id", fmt.Sprintf("user-%d", n))
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "public-body")
	}))
	defer origin.Close()

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

	for i := 1; i <= 2; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/resource", nil))
		if got, want := rec.Header().Get("X-User-Id"), fmt.Sprintf("user-%d", i); got != want {
			t.Fatalf("request %d: expected X-User-Id %q from the origin, got %q", i, want, got)
		}
	}
	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Fatalf("expected the origin to be called twice, got %d", got)
	}
}

// TestBugHuntQualifiedNoCacheFieldReplayed reproduces the no-cache half of
// jonbaldie/gleam#81 (RFC 9111 section 5.2.2.4).
func TestBugHuntQualifiedNoCacheFieldReplayed(t *testing.T) {
	var calls int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		w.Header().Set("Cache-Control", `no-cache="X-Secret"`)
		w.Header().Set("X-Secret", fmt.Sprintf("secret-%d", n))
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "public-body")
	}))
	defer origin.Close()

	c := newMockCache()
	handler := mustCachingProxyHandler(t, origin.URL, c, time.Minute)

	for i := 1; i <= 2; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/resource", nil))
		if got, want := rec.Header().Get("X-Secret"), fmt.Sprintf("secret-%d", i); got != want {
			t.Fatalf("request %d: expected X-Secret %q, got %q", i, want, got)
		}
	}
	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Fatalf("expected the origin to be called twice, got %d", got)
	}
}

// A field-name list containing a comma stays attached to its directive, so the
// response is still refused storage, and directives that follow the quoted
// list are still parsed.
func TestProxyHandlesQuotedCommaInCacheControlDirective(t *testing.T) {
	for _, directive := range []string{
		`private="X-A, X-B"`,
		`no-cache="X-A, X-B"`,
		`max-age=60, private="X-A, X-B"`,
		`private="X-A, X-B", max-age=60`,
		`PRIVATE="X-A"`,
	} {
		t.Run(directive, func(t *testing.T) {
			var calls int64
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt64(&calls, 1)
				w.Header().Set("Cache-Control", directive)
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprint(w, "public-body")
			}))
			defer origin.Close()

			handler := mustCachingProxyHandler(t, origin.URL, newMockCache(), time.Minute)
			for i := 1; i <= 2; i++ {
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/resource", nil))
			}
			if got := atomic.LoadInt64(&calls); got != 2 {
				t.Fatalf("expected the origin to be called twice, got %d", got)
			}
		})
	}
}

// A quoted field-name list must not hide a following freshness directive from
// the age parser.
func TestCacheControlAgeSkipsQuotedFieldNameLists(t *testing.T) {
	header := http.Header{"Cache-Control": []string{`private="X-A, X-B", max-age=60`}}
	age, found := cacheControlAge(header, "max-age")
	if !found || age != time.Minute {
		t.Fatalf("expected max-age of one minute, got %v (found=%v)", age, found)
	}
	if _, found := cacheControlAge(header, "s-maxage"); found {
		t.Fatal("expected no s-maxage directive")
	}
}

// A response whose Cache-Control carries no storage-forbidding directive is
// still stored and reused.
func TestProxyStillCachesResponseWithUnrelatedQualifiedDirective(t *testing.T) {
	var calls int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		w.Header().Set("Cache-Control", `max-age=60, no-transform`)
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "response-%d", n)
	}))
	defer origin.Close()

	handler := mustCachingProxyHandler(t, origin.URL, newMockCache(), time.Minute)
	for i := 1; i <= 2; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/resource", nil))
		if got, want := rec.Body.String(), "response-1"; got != want {
			t.Fatalf("request %d: expected body %q, got %q", i, want, got)
		}
	}
	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Fatalf("expected the origin to be called once, got %d", got)
	}
}

// A request's qualified no-cache bypasses the cache in the same way its bare
// form does.
func TestProxyHonoursQualifiedRequestNoCache(t *testing.T) {
	var calls int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		w.Header().Set("Cache-Control", "max-age=60")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "response-%d", n)
	}))
	defer origin.Close()

	handler := mustCachingProxyHandler(t, origin.URL, newMockCache(), time.Minute)
	for i := 1; i <= 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/resource", nil)
		req.Header.Set("Cache-Control", `no-cache="X-Secret"`)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if got, want := rec.Body.String(), fmt.Sprintf("response-%d", i); got != want {
			t.Fatalf("request %d: expected body %q, got %q", i, want, got)
		}
	}
	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Fatalf("expected the origin to be called twice, got %d", got)
	}
}

func TestSplitCacheControlValue(t *testing.T) {
	for _, testCase := range []struct {
		value string
		want  []string
	}{
		{value: "", want: nil},
		{value: " , ,", want: nil},
		{value: "max-age=60, no-transform", want: []string{"max-age=60", "no-transform"}},
		{value: `private="X-A, X-B"`, want: []string{`private="X-A, X-B"`}},
		{value: `private="X-A\", X-B", max-age=60`, want: []string{`private="X-A\", X-B"`, "max-age=60"}},
		{value: `private="unterminated, max-age=60`, want: []string{`private="unterminated, max-age=60`}},
	} {
		got := splitCacheControlValue(testCase.value)
		if len(got) != len(testCase.want) {
			t.Fatalf("%q: expected %q, got %q", testCase.value, testCase.want, got)
		}
		for i := range got {
			if got[i] != testCase.want[i] {
				t.Fatalf("%q: expected %q, got %q", testCase.value, testCase.want, got)
			}
		}
	}
}
