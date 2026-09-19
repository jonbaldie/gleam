package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type TestLog struct {
	mu      sync.Mutex
	entries []string
}

func (l *TestLog) Logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.mu.Lock()
	l.entries = append(l.entries, msg)
	l.mu.Unlock()
	fmt.Println(msg)
}

func (l *TestLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.entries, "\n")
}

func freePort() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer l.Close()
	_, port, err := net.SplitHostPort(l.Addr().String())
	return port, err
}

func main() {
	tlog := &TestLog{}
	tlog.Logf("=== GLEAM EXPLORATORY TESTING SUITE ===")
	tlog.Logf("Timestamp: %s", time.Now().UTC().Format(time.RFC3339))
	tlog.Logf("Environment: Go %s, macOS", os.Getenv("GOTOOLCHAIN"))

	// Build the Gleam binary
	tlog.Logf("\n--- Step 1: Building Gleam binary ---")
	buildCmd := exec.Command("go", "build", "-o", "bin/gleam", "gleam.go")
	buildOut, err := buildCmd.CombinedOutput()
	if err != nil {
		log.Fatalf("Build failed: %v, output: %s", err, string(buildOut))
	}
	tlog.Logf("Build succeeded: bin/gleam created.")

	// Run exploratory tests
	passedCount := 0
	failedCount := 0

	runTest := func(name string, fn func(tlog *TestLog) error) {
		tlog.Logf("\n>>> RUNNING: %s", name)
		if err := fn(tlog); err != nil {
			failedCount++
			tlog.Logf("FAIL: %s: %v", name, err)
		} else {
			passedCount++
			tlog.Logf("PASS: %s", name)
		}
	}

	// ----------------------------------------------------------------------
	// Journey 1: Caching Lifecycle, Expiration, Freshness & Cache-Control Directives
	// ----------------------------------------------------------------------
	tlog.Logf("\n========================================================")
	tlog.Logf("JOURNEY 1: Caching Lifecycle, Freshness, & Cache-Control")
	tlog.Logf("========================================================")

	runTest("J1.1 Ordinary Path: Miss -> Cache -> Hit with Age Header", func(tl *TestLog) error {
		var originRequests atomic.Int32
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count := originRequests.Add(1)
			w.Header().Set("Cache-Control", "public, max-age=60")
			w.Header().Set("Date", time.Now().UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "representation-v%d", count)
		}))
		defer origin.Close()

		proxyPort, err := freePort()
		if err != nil {
			return err
		}

		cmd := exec.Command("./bin/gleam")
		cmd.Env = append(os.Environ(),
			"ORIGIN_URL="+origin.URL,
			"PORT="+proxyPort,
			"TTL_MINUTES=5",
			"CACHE_TYPE=memory",
		)
		var proxyStderr bytes.Buffer
		cmd.Stderr = &proxyStderr
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start proxy: %w", err)
		}
		defer func() {
			_ = cmd.Process.Kill()
		}()
		time.Sleep(300 * time.Millisecond)

		proxyURL := "http://127.0.0.1:" + proxyPort

		// First request: Cache miss
		res1, err := http.Get(proxyURL + "/resource")
		if err != nil {
			return fmt.Errorf("req 1 failed: %w", err)
		}
		b1, _ := io.ReadAll(res1.Body)
		res1.Body.Close()
		tl.Logf("Req 1 (Miss): status=%d, body=%q, Age=%q", res1.StatusCode, string(b1), res1.Header.Get("Age"))

		if res1.StatusCode != http.StatusOK || string(b1) != "representation-v1" {
			return fmt.Errorf("unexpected req 1 result: %d / %s", res1.StatusCode, string(b1))
		}
		if originRequests.Load() != 1 {
			return fmt.Errorf("expected 1 origin call, got %d", originRequests.Load())
		}

		// Wait briefly so resident time advances
		time.Sleep(1100 * time.Millisecond)

		// Second request: Cache hit
		res2, err := http.Get(proxyURL + "/resource")
		if err != nil {
			return fmt.Errorf("req 2 failed: %w", err)
		}
		b2, _ := io.ReadAll(res2.Body)
		res2.Body.Close()
		age := res2.Header.Get("Age")
		tl.Logf("Req 2 (Hit): status=%d, body=%q, Age=%q", res2.StatusCode, string(b2), age)

		if res2.StatusCode != http.StatusOK || string(b2) != "representation-v1" {
			return fmt.Errorf("unexpected req 2 result: %d / %s", res2.StatusCode, string(b2))
		}
		if originRequests.Load() != 1 {
			return fmt.Errorf("expected origin calls to stay 1, got %d", originRequests.Load())
		}
		if age == "" || age == "0" {
			tl.Logf("Notice: Age header is %q", age)
		}
		return nil
	})

	runTest("J1.2 Variation: Response Cache-Control no-store is never cached", func(tl *TestLog) error {
		var originRequests atomic.Int32
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count := originRequests.Add(1)
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "secret-v%d", count)
		}))
		defer origin.Close()

		proxyPort, _ := freePort()
		cmd := exec.Command("./bin/gleam")
		cmd.Env = append(os.Environ(),
			"ORIGIN_URL="+origin.URL,
			"PORT="+proxyPort,
			"TTL_MINUTES=5",
			"CACHE_TYPE=memory",
		)
		_ = cmd.Start()
		defer func() { _ = cmd.Process.Kill() }()
		time.Sleep(300 * time.Millisecond)
		proxyURL := "http://127.0.0.1:" + proxyPort

		res1, _ := http.Get(proxyURL + "/secret")
		b1, _ := io.ReadAll(res1.Body)
		res1.Body.Close()

		res2, _ := http.Get(proxyURL + "/secret")
		b2, _ := io.ReadAll(res2.Body)
		res2.Body.Close()

		tl.Logf("Req 1: body=%q, Req 2: body=%q, originCalls=%d", string(b1), string(b2), originRequests.Load())
		if originRequests.Load() != 2 {
			return fmt.Errorf("no-store response was cached! originCalls=%d", originRequests.Load())
		}
		return nil
	})

	runTest("J1.3 Variation: Response Cache-Control private is not cached in shared cache", func(tl *TestLog) error {
		var originRequests atomic.Int32
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count := originRequests.Add(1)
			w.Header().Set("Cache-Control", "private, max-age=300")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "user-data-v%d", count)
		}))
		defer origin.Close()

		proxyPort, _ := freePort()
		cmd := exec.Command("./bin/gleam")
		cmd.Env = append(os.Environ(),
			"ORIGIN_URL="+origin.URL,
			"PORT="+proxyPort,
			"TTL_MINUTES=5",
			"CACHE_TYPE=memory",
		)
		_ = cmd.Start()
		defer func() { _ = cmd.Process.Kill() }()
		time.Sleep(300 * time.Millisecond)
		proxyURL := "http://127.0.0.1:" + proxyPort

		res1, _ := http.Get(proxyURL + "/private-data")
		b1, _ := io.ReadAll(res1.Body)
		res1.Body.Close()

		res2, _ := http.Get(proxyURL + "/private-data")
		b2, _ := io.ReadAll(res2.Body)
		res2.Body.Close()

		tl.Logf("Req 1: body=%q, Req 2: body=%q, originCalls=%d", string(b1), string(b2), originRequests.Load())
		if originRequests.Load() != 2 {
			return fmt.Errorf("private response was cached in shared cache! originCalls=%d", originRequests.Load())
		}
		return nil
	})

	runTest("J1.4 Variation: Response with Set-Cookie is not cached in shared cache", func(tl *TestLog) error {
		var originRequests atomic.Int32
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count := originRequests.Add(1)
			w.Header().Set("Set-Cookie", "session=xyz; Path=/")
			w.Header().Set("Cache-Control", "public, max-age=300")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "cookie-content-v%d", count)
		}))
		defer origin.Close()

		proxyPort, _ := freePort()
		cmd := exec.Command("./bin/gleam")
		cmd.Env = append(os.Environ(),
			"ORIGIN_URL="+origin.URL,
			"PORT="+proxyPort,
			"TTL_MINUTES=5",
			"CACHE_TYPE=memory",
		)
		_ = cmd.Start()
		defer func() { _ = cmd.Process.Kill() }()
		time.Sleep(300 * time.Millisecond)
		proxyURL := "http://127.0.0.1:" + proxyPort

		res1, _ := http.Get(proxyURL + "/with-cookie")
		res1.Body.Close()

		res2, _ := http.Get(proxyURL + "/with-cookie")
		res2.Body.Close()

		tl.Logf("Set-Cookie test: originCalls=%d", originRequests.Load())
		if originRequests.Load() != 2 {
			return fmt.Errorf("response with Set-Cookie was cached in shared cache")
		}
		return nil
	})

	runTest("J1.5 Variation: Client request Cache-Control no-cache bypasses cache", func(tl *TestLog) error {
		var originRequests atomic.Int32
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count := originRequests.Add(1)
			w.Header().Set("Cache-Control", "public, max-age=300")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "cached-data-v%d", count)
		}))
		defer origin.Close()

		proxyPort, _ := freePort()
		cmd := exec.Command("./bin/gleam")
		cmd.Env = append(os.Environ(),
			"ORIGIN_URL="+origin.URL,
			"PORT="+proxyPort,
			"TTL_MINUTES=5",
			"CACHE_TYPE=memory",
		)
		_ = cmd.Start()
		defer func() { _ = cmd.Process.Kill() }()
		time.Sleep(300 * time.Millisecond)
		proxyURL := "http://127.0.0.1:" + proxyPort

		// Prime cache
		res1, _ := http.Get(proxyURL + "/bypass-test")
		res1.Body.Close()

		// Request with Cache-Control: no-cache
		req, _ := http.NewRequest(http.MethodGet, proxyURL+"/bypass-test", nil)
		req.Header.Set("Cache-Control", "no-cache")
		res2, _ := http.DefaultClient.Do(req)
		b2, _ := io.ReadAll(res2.Body)
		res2.Body.Close()

		tl.Logf("Req with no-cache: body=%q, originCalls=%d", string(b2), originRequests.Load())
		if originRequests.Load() != 2 {
			return fmt.Errorf("client Cache-Control: no-cache was ignored! originCalls=%d", originRequests.Load())
		}
		return nil
	})

	runTest("J1.6 Variation: Uncacheable HTTP status codes (404, 500) are not stored", func(tl *TestLog) error {
		var originRequests atomic.Int32
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			originRequests.Add(1)
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, "not found")
		}))
		defer origin.Close()

		proxyPort, _ := freePort()
		cmd := exec.Command("./bin/gleam")
		cmd.Env = append(os.Environ(),
			"ORIGIN_URL="+origin.URL,
			"PORT="+proxyPort,
			"TTL_MINUTES=5",
			"CACHE_TYPE=memory",
		)
		_ = cmd.Start()
		defer func() { _ = cmd.Process.Kill() }()
		time.Sleep(300 * time.Millisecond)
		proxyURL := "http://127.0.0.1:" + proxyPort

		res1, _ := http.Get(proxyURL + "/missing")
		res1.Body.Close()

		res2, _ := http.Get(proxyURL + "/missing")
		res2.Body.Close()

		tl.Logf("404 Not Found test: originCalls=%d", originRequests.Load())
		if originRequests.Load() != 2 {
			return fmt.Errorf("404 response was stored in cache! originCalls=%d", originRequests.Load())
		}
		return nil
	})

	runTest("J1.7 Variation: Response with past Expires is immediately stale and not stored", func(tl *TestLog) error {
		var originRequests atomic.Int32
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count := originRequests.Add(1)
			w.Header().Set("Expires", "Wed, 10 Sep 2025 00:00:00 GMT")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "stale-v%d", count)
		}))
		defer origin.Close()

		proxyPort, _ := freePort()
		cmd := exec.Command("./bin/gleam")
		cmd.Env = append(os.Environ(),
			"ORIGIN_URL="+origin.URL,
			"PORT="+proxyPort,
			"TTL_MINUTES=5",
			"CACHE_TYPE=memory",
		)
		_ = cmd.Start()
		defer func() { _ = cmd.Process.Kill() }()
		time.Sleep(300 * time.Millisecond)
		proxyURL := "http://127.0.0.1:" + proxyPort

		res1, _ := http.Get(proxyURL + "/expired")
		res1.Body.Close()

		res2, _ := http.Get(proxyURL + "/expired")
		res2.Body.Close()

		tl.Logf("Past Expires test: originCalls=%d", originRequests.Load())
		if originRequests.Load() != 2 {
			return fmt.Errorf("response with past Expires was stored in cache! originCalls=%d", originRequests.Load())
		}
		return nil
	})

	// ----------------------------------------------------------------------
	// Journey 2: Conditional Requests and HTTP Validation
	// ----------------------------------------------------------------------
	tlog.Logf("\n========================================================")
	tlog.Logf("JOURNEY 2: Conditional Requests & Validation")
	tlog.Logf("========================================================")

	runTest("J2.1 Ordinary Path: If-None-Match with matching ETag returns 304", func(tl *TestLog) error {
		var originRequests atomic.Int32
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count := originRequests.Add(1)
			w.Header().Set("ETag", "\"v1.0.0\"")
			w.Header().Set("Cache-Control", "public, max-age=300")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "payload-%d", count)
		}))
		defer origin.Close()

		proxyPort, _ := freePort()
		cmd := exec.Command("./bin/gleam")
		cmd.Env = append(os.Environ(),
			"ORIGIN_URL="+origin.URL,
			"PORT="+proxyPort,
			"TTL_MINUTES=5",
			"CACHE_TYPE=memory",
		)
		_ = cmd.Start()
		defer func() { _ = cmd.Process.Kill() }()
		time.Sleep(300 * time.Millisecond)
		proxyURL := "http://127.0.0.1:" + proxyPort

		// Prime cache
		res1, _ := http.Get(proxyURL + "/etag-test")
		b1, _ := io.ReadAll(res1.Body)
		res1.Body.Close()

		// Conditional request with matching ETag
		req, _ := http.NewRequest(http.MethodGet, proxyURL+"/etag-test", nil)
		req.Header.Set("If-None-Match", "\"v1.0.0\"")
		res2, _ := http.DefaultClient.Do(req)
		b2, _ := io.ReadAll(res2.Body)
		res2.Body.Close()

		tl.Logf("Req 1 (Miss): status=%d, body=%q", res1.StatusCode, string(b1))
		tl.Logf("Req 2 (Conditional): status=%d, body=%q, Age=%q, originCalls=%d",
			res2.StatusCode, string(b2), res2.Header.Get("Age"), originRequests.Load())

		if res2.StatusCode != http.StatusNotModified {
			return fmt.Errorf("expected 304 Not Modified, got %d", res2.StatusCode)
		}
		if len(b2) != 0 {
			return fmt.Errorf("expected empty body on 304, got %q", string(b2))
		}
		if originRequests.Load() != 1 {
			return fmt.Errorf("expected origin not contacted on 304, originCalls=%d", originRequests.Load())
		}
		return nil
	})

	runTest("J2.2 Variation: Weak ETag (W/\"v1\") matches in If-None-Match", func(tl *TestLog) error {
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("ETag", "W/\"v1.0.0\"")
			w.Header().Set("Cache-Control", "public, max-age=300")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "weak-payload")
		}))
		defer origin.Close()

		proxyPort, _ := freePort()
		cmd := exec.Command("./bin/gleam")
		cmd.Env = append(os.Environ(),
			"ORIGIN_URL="+origin.URL,
			"PORT="+proxyPort,
			"TTL_MINUTES=5",
			"CACHE_TYPE=memory",
		)
		_ = cmd.Start()
		defer func() { _ = cmd.Process.Kill() }()
		time.Sleep(300 * time.Millisecond)
		proxyURL := "http://127.0.0.1:" + proxyPort

		res1, _ := http.Get(proxyURL + "/weak-test")
		res1.Body.Close()

		// Send non-weak tag against stored weak tag (weak comparison RFC 9110 §8.8.3.2)
		req, _ := http.NewRequest(http.MethodGet, proxyURL+"/weak-test", nil)
		req.Header.Set("If-None-Match", "\"v1.0.0\"")
		res2, _ := http.DefaultClient.Do(req)
		res2.Body.Close()

		tl.Logf("Weak ETag test: status=%d", res2.StatusCode)
		if res2.StatusCode != http.StatusNotModified {
			return fmt.Errorf("expected 304 for weak ETag comparison, got %d", res2.StatusCode)
		}
		return nil
	})

	runTest("J2.3 Variation: If-Modified-Since with matching date returns 304", func(tl *TestLog) error {
		lastMod := "Fri, 18 Sep 2026 12:00:00 GMT"
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Last-Modified", lastMod)
			w.Header().Set("Cache-Control", "public, max-age=300")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "dated-payload")
		}))
		defer origin.Close()

		proxyPort, _ := freePort()
		cmd := exec.Command("./bin/gleam")
		cmd.Env = append(os.Environ(),
			"ORIGIN_URL="+origin.URL,
			"PORT="+proxyPort,
			"TTL_MINUTES=5",
			"CACHE_TYPE=memory",
		)
		_ = cmd.Start()
		defer func() { _ = cmd.Process.Kill() }()
		time.Sleep(300 * time.Millisecond)
		proxyURL := "http://127.0.0.1:" + proxyPort

		res1, _ := http.Get(proxyURL + "/date-test")
		res1.Body.Close()

		// Conditional request with same date
		req, _ := http.NewRequest(http.MethodGet, proxyURL+"/date-test", nil)
		req.Header.Set("If-Modified-Since", lastMod)
		res2, _ := http.DefaultClient.Do(req)
		res2.Body.Close()

		tl.Logf("If-Modified-Since matching test: status=%d", res2.StatusCode)
		if res2.StatusCode != http.StatusNotModified {
			return fmt.Errorf("expected 304 for matching If-Modified-Since, got %d", res2.StatusCode)
		}
		return nil
	})

	runTest("J2.4 Variation: If-None-Match takes precedence over If-Modified-Since", func(tl *TestLog) error {
		lastMod := "Fri, 18 Sep 2026 12:00:00 GMT"
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("ETag", "\"tag-current\"")
			w.Header().Set("Last-Modified", lastMod)
			w.Header().Set("Cache-Control", "public, max-age=300")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "precedence-payload")
		}))
		defer origin.Close()

		proxyPort, _ := freePort()
		cmd := exec.Command("./bin/gleam")
		cmd.Env = append(os.Environ(),
			"ORIGIN_URL="+origin.URL,
			"PORT="+proxyPort,
			"TTL_MINUTES=5",
			"CACHE_TYPE=memory",
		)
		_ = cmd.Start()
		defer func() { _ = cmd.Process.Kill() }()
		time.Sleep(300 * time.Millisecond)
		proxyURL := "http://127.0.0.1:" + proxyPort

		res1, _ := http.Get(proxyURL + "/precedence-test")
		res1.Body.Close()

		// Mismatching ETag but matching Date -> must return 200 OK (RFC 9110 §13.2.2)
		req, _ := http.NewRequest(http.MethodGet, proxyURL+"/precedence-test", nil)
		req.Header.Set("If-None-Match", "\"tag-old\"")
		req.Header.Set("If-Modified-Since", lastMod)
		res2, _ := http.DefaultClient.Do(req)
		b2, _ := io.ReadAll(res2.Body)
		res2.Body.Close()

		tl.Logf("Precedence test: status=%d, body=%q", res2.StatusCode, string(b2))
		if res2.StatusCode != http.StatusOK {
			return fmt.Errorf("expected 200 OK when If-None-Match fails, got %d", res2.StatusCode)
		}
		return nil
	})

	// ----------------------------------------------------------------------
	// Journey 3: State Mutations, Invalidation, and Header Isolation
	// ----------------------------------------------------------------------
	tlog.Logf("\n========================================================")
	tlog.Logf("JOURNEY 3: Mutations, Invalidation, & Vary Isolation")
	tlog.Logf("========================================================")

	runTest("J3.1 Ordinary Path: POST invalidates cached GET response", func(tl *TestLog) error {
		var state atomic.Value
		state.Store("initial-data")
		var getCalls atomic.Int32

		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				getCalls.Add(1)
				w.Header().Set("Cache-Control", "public, max-age=300")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, state.Load().(string))
			case http.MethodPost:
				state.Store("updated-data")
				w.WriteHeader(http.StatusNoContent)
			}
		}))
		defer origin.Close()

		proxyPort, _ := freePort()
		cmd := exec.Command("./bin/gleam")
		cmd.Env = append(os.Environ(),
			"ORIGIN_URL="+origin.URL,
			"PORT="+proxyPort,
			"TTL_MINUTES=5",
			"CACHE_TYPE=memory",
		)
		_ = cmd.Start()
		defer func() { _ = cmd.Process.Kill() }()
		time.Sleep(300 * time.Millisecond)
		proxyURL := "http://127.0.0.1:" + proxyPort

		// 1. Prime cache
		res1, _ := http.Get(proxyURL + "/items")
		b1, _ := io.ReadAll(res1.Body)
		res1.Body.Close()

		// 2. Cache hit
		res2, _ := http.Get(proxyURL + "/items")
		b2, _ := io.ReadAll(res2.Body)
		res2.Body.Close()

		// 3. POST mutation
		postReq, _ := http.NewRequest(http.MethodPost, proxyURL+"/items", strings.NewReader("name=new"))
		resPost, _ := http.DefaultClient.Do(postReq)
		resPost.Body.Close()

		// 4. GET after mutation -> must fetch fresh data from origin
		res3, _ := http.Get(proxyURL + "/items")
		b3, _ := io.ReadAll(res3.Body)
		res3.Body.Close()

		tl.Logf("Req 1 (Miss): %q, Req 2 (Hit): %q, POST status: %d, Req 3 (Post-Invalidate): %q, originCalls=%d",
			string(b1), string(b2), resPost.StatusCode, string(b3), getCalls.Load())

		if string(b3) != "updated-data" {
			return fmt.Errorf("cache was not invalidated after POST! got stale body: %q", string(b3))
		}
		if getCalls.Load() != 2 {
			return fmt.Errorf("expected 2 origin GET calls, got %d", getCalls.Load())
		}
		return nil
	})

	runTest("J3.2 Variation: Failed unsafe request (500 Error) does NOT invalidate cache", func(tl *TestLog) error {
		var getCalls atomic.Int32
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				getCalls.Add(1)
				w.Header().Set("Cache-Control", "public, max-age=300")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, "stable-data")
			case http.MethodPost:
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, "server error")
			}
		}))
		defer origin.Close()

		proxyPort, _ := freePort()
		cmd := exec.Command("./bin/gleam")
		cmd.Env = append(os.Environ(),
			"ORIGIN_URL="+origin.URL,
			"PORT="+proxyPort,
			"TTL_MINUTES=5",
			"CACHE_TYPE=memory",
		)
		_ = cmd.Start()
		defer func() { _ = cmd.Process.Kill() }()
		time.Sleep(300 * time.Millisecond)
		proxyURL := "http://127.0.0.1:" + proxyPort

		// 1. Prime cache
		res1, _ := http.Get(proxyURL + "/items-fail")
		res1.Body.Close()

		// 2. Failed POST
		postReq, _ := http.NewRequest(http.MethodPost, proxyURL+"/items-fail", nil)
		resPost, _ := http.DefaultClient.Do(postReq)
		resPost.Body.Close()

		// 3. GET after failed POST -> must STILL be cached (RFC 9111 §4.4)
		res2, _ := http.Get(proxyURL + "/items-fail")
		b2, _ := io.ReadAll(res2.Body)
		res2.Body.Close()

		tl.Logf("Failed POST test: POST status=%d, subsequent GET body=%q, originCalls=%d",
			resPost.StatusCode, string(b2), getCalls.Load())

		if getCalls.Load() != 1 {
			return fmt.Errorf("failed POST erroneously invalidated cache! originCalls=%d", getCalls.Load())
		}
		return nil
	})

	runTest("J3.3 Variation: Safe non-GET method (HEAD) does not invalidate cache", func(tl *TestLog) error {
		var getCalls atomic.Int32
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				getCalls.Add(1)
				w.Header().Set("Cache-Control", "public, max-age=300")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, "cached-data")
			case http.MethodHead:
				w.Header().Set("Cache-Control", "public, max-age=300")
				w.WriteHeader(http.StatusOK)
			}
		}))
		defer origin.Close()

		proxyPort, _ := freePort()
		cmd := exec.Command("./bin/gleam")
		cmd.Env = append(os.Environ(),
			"ORIGIN_URL="+origin.URL,
			"PORT="+proxyPort,
			"TTL_MINUTES=5",
			"CACHE_TYPE=memory",
		)
		_ = cmd.Start()
		defer func() { _ = cmd.Process.Kill() }()
		time.Sleep(300 * time.Millisecond)
		proxyURL := "http://127.0.0.1:" + proxyPort

		res1, _ := http.Get(proxyURL + "/head-test")
		res1.Body.Close()

		headReq, _ := http.NewRequest(http.MethodHead, proxyURL+"/head-test", nil)
		resHead, _ := http.DefaultClient.Do(headReq)
		resHead.Body.Close()

		res2, _ := http.Get(proxyURL + "/head-test")
		b2, _ := io.ReadAll(res2.Body)
		res2.Body.Close()

		tl.Logf("HEAD test: originCalls=%d, body=%q", getCalls.Load(), string(b2))
		if getCalls.Load() != 1 {
			return fmt.Errorf("HEAD request invalidated cached GET! originCalls=%d", getCalls.Load())
		}
		return nil
	})

	runTest("J3.4 Variation: Vary Cookie isolates cache entries for different users", func(tl *TestLog) error {
		var originRequests atomic.Int32
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			originRequests.Add(1)
			cookie := r.Header.Get("Cookie")
			w.Header().Set("Cache-Control", "public, max-age=300")
			w.Header().Set("Vary", "Cookie")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "User-Session: %s", cookie)
		}))
		defer origin.Close()

		proxyPort, _ := freePort()
		cmd := exec.Command("./bin/gleam")
		cmd.Env = append(os.Environ(),
			"ORIGIN_URL="+origin.URL,
			"PORT="+proxyPort,
			"TTL_MINUTES=5",
			"CACHE_TYPE=memory",
		)
		_ = cmd.Start()
		defer func() { _ = cmd.Process.Kill() }()
		time.Sleep(300 * time.Millisecond)
		proxyURL := "http://127.0.0.1:" + proxyPort

		// User A
		reqA, _ := http.NewRequest(http.MethodGet, proxyURL+"/profile", nil)
		reqA.Header.Set("Cookie", "user=Alice")
		resA1, _ := http.DefaultClient.Do(reqA)
		bA1, _ := io.ReadAll(resA1.Body)
		resA1.Body.Close()

		// User B
		reqB, _ := http.NewRequest(http.MethodGet, proxyURL+"/profile", nil)
		reqB.Header.Set("Cookie", "user=Bob")
		resB1, _ := http.DefaultClient.Do(reqB)
		bB1, _ := io.ReadAll(resB1.Body)
		resB1.Body.Close()

		// User A again (must be cache hit for Alice)
		resA2, _ := http.DefaultClient.Do(reqA)
		bA2, _ := io.ReadAll(resA2.Body)
		resA2.Body.Close()

		tl.Logf("Alice 1: %q, Bob 1: %q, Alice 2: %q, originCalls=%d",
			string(bA1), string(bB1), string(bA2), originRequests.Load())

		if string(bA1) != "User-Session: user=Alice" || string(bA2) != "User-Session: user=Alice" {
			return fmt.Errorf("alice got wrong content: %s / %s", string(bA1), string(bA2))
		}
		if string(bB1) != "User-Session: user=Bob" {
			return fmt.Errorf("bob got wrong content: %s", string(bB1))
		}
		if originRequests.Load() != 2 {
			return fmt.Errorf("expected 2 origin calls (1 for Alice, 1 for Bob), got %d", originRequests.Load())
		}
		return nil
	})

	runTest("J3.5 Variation: Query string variation creates distinct cache keys", func(tl *TestLog) error {
		var originRequests atomic.Int32
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			originRequests.Add(1)
			w.Header().Set("Cache-Control", "public, max-age=300")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "Query: %s", r.URL.RawQuery)
		}))
		defer origin.Close()

		proxyPort, _ := freePort()
		cmd := exec.Command("./bin/gleam")
		cmd.Env = append(os.Environ(),
			"ORIGIN_URL="+origin.URL,
			"PORT="+proxyPort,
			"TTL_MINUTES=5",
			"CACHE_TYPE=memory",
		)
		_ = cmd.Start()
		defer func() { _ = cmd.Process.Kill() }()
		time.Sleep(300 * time.Millisecond)
		proxyURL := "http://127.0.0.1:" + proxyPort

		res1, _ := http.Get(proxyURL + "/search?q=foo")
		b1, _ := io.ReadAll(res1.Body)
		res1.Body.Close()

		res2, _ := http.Get(proxyURL + "/search?q=bar")
		b2, _ := io.ReadAll(res2.Body)
		res2.Body.Close()

		res3, _ := http.Get(proxyURL + "/search?q=foo")
		b3, _ := io.ReadAll(res3.Body)
		res3.Body.Close()

		tl.Logf("q=foo 1: %q, q=bar: %q, q=foo 2: %q, originCalls=%d",
			string(b1), string(b2), string(b3), originRequests.Load())

		if string(b1) != "Query: q=foo" || string(b3) != "Query: q=foo" {
			return fmt.Errorf("q=foo got wrong content: %s / %s", string(b1), string(b3))
		}
		if string(b2) != "Query: q=bar" {
			return fmt.Errorf("q=bar got wrong content: %s", string(b2))
		}
		if originRequests.Load() != 2 {
			return fmt.Errorf("expected 2 origin calls, got %d", originRequests.Load())
		}
		return nil
	})

	runTest("J1.8 Variation: Range request bypasses cache in both directions", func(tl *TestLog) error {
		var originRequests atomic.Int32
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			originRequests.Add(1)
			w.Header().Set("Cache-Control", "public, max-age=300")
			if r.Header.Get("Range") != "" {
				w.WriteHeader(http.StatusPartialContent)
				fmt.Fprint(w, "partial")
				return
			}
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "full content")
		}))
		defer origin.Close()

		proxyPort, _ := freePort()
		cmd := exec.Command("./bin/gleam")
		cmd.Env = append(os.Environ(),
			"ORIGIN_URL="+origin.URL,
			"PORT="+proxyPort,
			"TTL_MINUTES=5",
			"CACHE_TYPE=memory",
		)
		_ = cmd.Start()
		defer func() { _ = cmd.Process.Kill() }()
		time.Sleep(300 * time.Millisecond)
		proxyURL := "http://127.0.0.1:" + proxyPort

		// 1. Prime cache with full GET
		res1, _ := http.Get(proxyURL + "/range-test")
		b1, _ := io.ReadAll(res1.Body)
		res1.Body.Close()

		// 2. Request with Range header
		reqRange, _ := http.NewRequest(http.MethodGet, proxyURL+"/range-test", nil)
		reqRange.Header.Set("Range", "bytes=0-6")
		res2, _ := http.DefaultClient.Do(reqRange)
		b2, _ := io.ReadAll(res2.Body)
		res2.Body.Close()

		tl.Logf("Full GET body=%q, Range GET body=%q, originCalls=%d", string(b1), string(b2), originRequests.Load())
		if res2.StatusCode != http.StatusPartialContent || string(b2) != "partial" {
			return fmt.Errorf("unexpected range response: %d, body=%q", res2.StatusCode, string(b2))
		}
		if originRequests.Load() != 2 {
			return fmt.Errorf("expected 2 origin calls (range should bypass cache), got %d", originRequests.Load())
		}
		return nil
	})

	runTest("J1.9 Variation: Authenticated request without public directive is not reused across clients", func(tl *TestLog) error {
		var originRequests atomic.Int32
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			originRequests.Add(1)
			w.Header().Set("Cache-Control", "max-age=300") // No public or must-revalidate or s-maxage
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "auth-response-%d", originRequests.Load())
		}))
		defer origin.Close()

		proxyPort, _ := freePort()
		cmd := exec.Command("./bin/gleam")
		cmd.Env = append(os.Environ(),
			"ORIGIN_URL="+origin.URL,
			"PORT="+proxyPort,
			"TTL_MINUTES=5",
			"CACHE_TYPE=memory",
			"CACHE_VARY_HEADERS=", // Disable vary headers to test RFC 9111 §3.5 gate specifically
		)
		_ = cmd.Start()
		defer func() { _ = cmd.Process.Kill() }()
		time.Sleep(300 * time.Millisecond)
		proxyURL := "http://127.0.0.1:" + proxyPort

		req1, _ := http.NewRequest(http.MethodGet, proxyURL+"/auth-resource", nil)
		req1.Header.Set("Authorization", "Bearer user-token-1")
		res1, _ := http.DefaultClient.Do(req1)
		b1, _ := io.ReadAll(res1.Body)
		res1.Body.Close()

		req2, _ := http.NewRequest(http.MethodGet, proxyURL+"/auth-resource", nil)
		req2.Header.Set("Authorization", "Bearer user-token-2")
		res2, _ := http.DefaultClient.Do(req2)
		b2, _ := io.ReadAll(res2.Body)
		res2.Body.Close()

		tl.Logf("Auth req 1: %q, Auth req 2: %q, originCalls=%d", string(b1), string(b2), originRequests.Load())
		if originRequests.Load() != 2 {
			return fmt.Errorf("authenticated response was reused without explicit permission! originCalls=%d", originRequests.Load())
		}
		return nil
	})

	runTest("J2.5 Variation: Multiple comma-separated ETags in If-None-Match", func(tl *TestLog) error {
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("ETag", "\"tag-match\"")
			w.Header().Set("Cache-Control", "public, max-age=300")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "multi-tag-payload")
		}))
		defer origin.Close()

		proxyPort, _ := freePort()
		cmd := exec.Command("./bin/gleam")
		cmd.Env = append(os.Environ(),
			"ORIGIN_URL="+origin.URL,
			"PORT="+proxyPort,
			"TTL_MINUTES=5",
			"CACHE_TYPE=memory",
		)
		_ = cmd.Start()
		defer func() { _ = cmd.Process.Kill() }()
		time.Sleep(300 * time.Millisecond)
		proxyURL := "http://127.0.0.1:" + proxyPort

		res1, _ := http.Get(proxyURL + "/multi-etag")
		res1.Body.Close()

		req, _ := http.NewRequest(http.MethodGet, proxyURL+"/multi-etag", nil)
		req.Header.Set("If-None-Match", "\"other-1\", \"tag-match\", \"other-2\"")
		res2, _ := http.DefaultClient.Do(req)
		res2.Body.Close()

		tl.Logf("Multi-ETag test status: %d", res2.StatusCode)
		if res2.StatusCode != http.StatusNotModified {
			return fmt.Errorf("expected 304 when one of multiple ETags matches, got %d", res2.StatusCode)
		}
		return nil
	})

	runTest("J3.6 Variation: PUT, DELETE, and PATCH invalidate cached GET", func(tl *TestLog) error {
		for _, method := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch} {
			var state atomic.Value
			state.Store("initial")
			var getCalls atomic.Int32

			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					getCalls.Add(1)
					w.Header().Set("Cache-Control", "public, max-age=300")
					w.WriteHeader(http.StatusOK)
					fmt.Fprint(w, state.Load().(string))
					return
				}
				if r.Method == method {
					state.Store("mutated-by-" + method)
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}))

			proxyPort, _ := freePort()
			cmd := exec.Command("./bin/gleam")
			cmd.Env = append(os.Environ(),
				"ORIGIN_URL="+origin.URL,
				"PORT="+proxyPort,
				"TTL_MINUTES=5",
				"CACHE_TYPE=memory",
			)
			_ = cmd.Start()
			time.Sleep(200 * time.Millisecond)
			proxyURL := "http://127.0.0.1:" + proxyPort

			// Prime
			res1, _ := http.Get(proxyURL + "/mut")
			res1.Body.Close()

			// Hit
			res2, _ := http.Get(proxyURL + "/mut")
			res2.Body.Close()

			// Mutate
			mutReq, _ := http.NewRequest(method, proxyURL+"/mut", nil)
			resMut, _ := http.DefaultClient.Do(mutReq)
			resMut.Body.Close()

			// GET after mutation
			res3, _ := http.Get(proxyURL + "/mut")
			b3, _ := io.ReadAll(res3.Body)
			res3.Body.Close()

			_ = cmd.Process.Kill()
			origin.Close()

			tl.Logf("Method %s: subsequent GET body=%q, originCalls=%d", method, string(b3), getCalls.Load())
			if string(b3) != "mutated-by-"+method {
				return fmt.Errorf("%s did not invalidate cache! body=%q", method, string(b3))
			}
			if getCalls.Load() != 2 {
				return fmt.Errorf("expected 2 origin calls for %s, got %d", method, getCalls.Load())
			}
		}
		return nil
	})

	tlog.Logf("\n========================================================")
	tlog.Logf("SUMMARY OF RESULTS")
	tlog.Logf("Total tests: %d, Passed: %d, Failed: %d", passedCount+failedCount, passedCount, failedCount)
	tlog.Logf("========================================================")

	// Save test evidence log to file
	_ = os.MkdirAll("docs/exploratory-testing/evidence", 0755)
	evidenceFile := "docs/exploratory-testing/evidence/2026-09-19-run.log"
	if err := os.WriteFile(evidenceFile, []byte(tlog.String()), 0644); err != nil {
		log.Printf("Failed to write evidence log: %v", err)
	} else {
		fmt.Printf("\nEvidence written to %s\n", evidenceFile)
	}
}
