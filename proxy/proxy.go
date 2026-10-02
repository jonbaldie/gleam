package proxy

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"jonbaldie/gleam/cache"
	"jonbaldie/gleam/cachepolicy"
)

func New(origin *url.URL, c cache.Cache, ttl time.Duration) http.Handler {
	return NewWithVaryHeaders(origin, c, ttl, defaultVaryHeaders)
}

func NewWithVaryHeaders(origin *url.URL, c cache.Cache, ttl time.Duration, varyHeaders []string) http.Handler {
	return NewHandler(newOriginReverseProxy(origin), c, ttl, varyHeaders)
}

// NewHandler wraps an upstream handler with this shared cache. The upstream
// may be any http.Handler: a reverse proxy to a remote origin, or an
// in-process handler. Hijack and Flush on the writers it receives delegate to
// the client's writer.
func NewHandler(upstream http.Handler, c cache.Cache, ttl time.Duration, varyHeaders []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			serveGet(upstream, c, w, r, varyHeaders, ttl)
			return
		}

		serveNonGet(upstream, c, w, r)
	})
}

// newOriginReverseProxy is the standard upstream adapter: a reverse proxy to
// a single remote origin.
func newOriginReverseProxy(origin *url.URL) *httputil.ReverseProxy {
	p := httputil.NewSingleHostReverseProxy(origin)
	director := p.Director
	p.Director = func(req *http.Request) {
		director(req)
		// NewSingleHostReverseProxy preserves Host. Set it on the forwarded
		// copy so cache keys and invalidation keep using the inbound Host.
		req.Host = origin.Host
	}
	return p
}

// serveGet answers a GET from the cache when possible, and otherwise forwards
// it upstream, storing the response when it is both storable and copied
// through to the client in full. An only-if-cached request gets a 504 when no
// reusable response can satisfy it instead of being forwarded.
func serveGet(upstream http.Handler, c cache.Cache, w http.ResponseWriter, r *http.Request, varyHeaders []string, ttl time.Duration) {
	onlyIfCached := cachepolicy.OnlyIfCached(r)
	if cachepolicy.ShouldBypass(r) {
		if onlyIfCached {
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		upstream.ServeHTTP(w, r)
		return
	}

	// RFC 9111 section 3.5: a shared cache may only reuse — or store — a
	// response to an authenticated request when the response says so
	// explicitly, whatever the request headers the cache key covers.
	authenticated := cachepolicy.IsAuthenticated(r)

	cacheKey := cacheKeyForRequestWithVaryHeaders(r, varyHeaders)
	if cachedItem, found := reusableCachedItem(c, cacheKey, authenticated); found {
		serveCachedItem(w, r, cachedItem)
		return
	}
	if onlyIfCached {
		w.WriteHeader(http.StatusGatewayTimeout)
		return
	}

	crw := &cacheResponseWriter{ResponseWriter: w, buf: new(bytes.Buffer), status: http.StatusOK}
	upstream.ServeHTTP(crw, r)
	// Reaching here means the upstream returned normally; an abnormal unwind
	// (such as the reverse proxy's http.ErrAbortHandler on a copy error)
	// skips this, and so skips storage too.
	crw.upstreamReturned = true
	if crw.cachedHeader == nil {
		crw.cachedHeader = cloneHeader(crw.ResponseWriter.Header())
	}

	storeResponse(c, cacheKey, crw, varyHeaders, ttl, authenticated)
}

// reusableCachedItem returns the stored entry for a key when this cache may
// answer the request from it.
func reusableCachedItem(c cache.Cache, cacheKey string, authenticated bool) (*cache.CacheItem, bool) {
	cachedItem, found := c.Get(cacheKey)
	if !found {
		return nil, false
	}
	if !cachepolicy.CanReuse(cachedItem.Header, authenticated) {
		return nil, false
	}
	return cachedItem, true
}

// storeResponse caches the origin's response when it is a complete, storable
// representation this shared cache is allowed to reuse.
func storeResponse(c cache.Cache, cacheKey string, crw *cacheResponseWriter, varyHeaders []string, ttl time.Duration, authenticated bool) {
	if !crw.copyComplete() || !cachepolicy.CanStoreResponse(crw.status, crw.cachedHeader, varyHeaders, authenticated) {
		return
	}

	receivedAt := time.Now()
	responseTTL, shouldStore := cachepolicy.FreshnessLifetime(crw.cachedHeader, receivedAt, ttl)
	if !shouldStore {
		return
	}
	c.Set(cacheKey, cache.CacheItem{
		Content:  crw.buf.Bytes(),
		Header:   crw.cachedHeader,
		Trailer:  crw.cachedTrailer(),
		Status:   crw.status,
		StoredAt: receivedAt,
	}, responseTTL)
}

// serveNonGet forwards a non-GET request upstream and, when the method
// is unsafe and the response is non-error, invalidates every stored entry
// for the target URI (RFC 9111 section 4.4).
func serveNonGet(upstream http.Handler, c cache.Cache, w http.ResponseWriter, r *http.Request) {
	srw := &statusResponseWriter{ResponseWriter: w, status: http.StatusOK}
	upstream.ServeHTTP(srw, r)

	if cachepolicy.InvalidatesStoredResponses(r.Method, srw.status) {
		c.InvalidatePrefix(cacheBaseKey(r))
	}
}

var defaultVaryHeaders = []string{"Authorization", "Cookie"}

func DefaultVaryHeaders() []string {
	return append([]string(nil), defaultVaryHeaders...)
}

func ParseVaryHeaders(raw string) []string {
	parts := strings.Split(raw, ",")
	headers := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		name = http.CanonicalHeaderKey(name)
		if _, found := seen[name]; found {
			continue
		}
		seen[name] = struct{}{}
		headers = append(headers, name)
	}
	return headers
}

// statusResponseWriter observes the origin's response status while streaming
// the response through untouched, so the non-GET path can invalidate the
// cache without buffering the body. Upgrade and flush paths stay live.
type statusResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not implement http.Hijacker")
	}
	return hijacker.Hijack()
}

func (w *statusResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

type cacheResponseWriter struct {
	http.ResponseWriter
	buf          *bytes.Buffer
	status       int
	cachedHeader http.Header
	// upstreamReturned records that the upstream finished serving without
	// unwinding, and writeErr the failure of any forwarded body write. A
	// response is only a complete representation when both agree the copy ran
	// to completion; storing anything less would poison the cache with a
	// truncated body (RFC 9111 section 3).
	upstreamReturned bool
	writeErr         error
}

func (w *cacheResponseWriter) copyComplete() bool {
	return w.upstreamReturned && w.writeErr == nil
}

func (w *cacheResponseWriter) WriteHeader(status int) {
	w.status = status
	w.cachedHeader = cloneHeader(w.ResponseWriter.Header())
	w.ResponseWriter.WriteHeader(status)
}

func (w *cacheResponseWriter) Write(b []byte) (int, error) {
	if w.cachedHeader == nil {
		w.WriteHeader(w.status)
	}
	w.buf.Write(b)
	n, err := w.ResponseWriter.Write(b)
	if err != nil && w.writeErr == nil {
		w.writeErr = err
	}
	return n, err
}

func (w *cacheResponseWriter) Header() http.Header {
	return w.ResponseWriter.Header()
}

func (w *cacheResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not implement http.Hijacker")
	}
	return hijacker.Hijack()
}

func (w *cacheResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *cacheResponseWriter) cachedTrailer() http.Header {
	if w.cachedHeader == nil {
		return nil
	}

	trailers := http.Header{}
	for _, name := range commaSeparatedHeaderValues(w.cachedHeader.Values("Trailer")) {
		for _, value := range w.ResponseWriter.Header().Values(name) {
			trailers.Add(name, value)
		}
	}
	for key, values := range w.ResponseWriter.Header() {
		if !strings.HasPrefix(key, http.TrailerPrefix) {
			continue
		}
		name := strings.TrimPrefix(key, http.TrailerPrefix)
		if name == "" {
			continue
		}
		for _, value := range values {
			trailers.Add(name, value)
		}
	}
	if len(trailers) == 0 {
		return nil
	}
	return trailers
}

// serveCachedItem answers a GET from a cache entry, evaluating the request's
// conditional validators against the stored representation and responding 304
// when they are satisfied.
func serveCachedItem(w http.ResponseWriter, r *http.Request, item *cache.CacheItem) {
	if cachepolicy.NotModified(r.Header, item.Status, item.Header) {
		writeCachedNotModified(w, item)
		return
	}
	writeCachedItem(w, item)
}

func writeCachedItem(w http.ResponseWriter, item *cache.CacheItem) {
	copyCachedHeaders(w, item)
	announceCachedTrailers(w, item)

	w.WriteHeader(item.Status)
	_, _ = w.Write(item.Content)

	for key, values := range item.Trailer {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
}

func writeCachedNotModified(w http.ResponseWriter, item *cache.CacheItem) {
	copyCachedHeaders(w, item)
	w.WriteHeader(http.StatusNotModified)
}

func copyCachedHeaders(w http.ResponseWriter, item *cache.CacheItem) {
	for key, values := range item.Header {
		if http.CanonicalHeaderKey(key) == "Age" {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	// Reuse without revalidation must report how long the response has been
	// held here, rather than replaying the origin's Age (RFC 9111 section 4.2.3).
	w.Header().Set("Age", strconv.FormatUint(cachepolicy.CurrentAge(item.Header, item.StoredAt, time.Now()), 10))
}

func announceCachedTrailers(w http.ResponseWriter, item *cache.CacheItem) {
	for name := range item.Trailer {
		if !headerContainsToken(w.Header().Values("Trailer"), name) {
			w.Header().Add("Trailer", name)
		}
	}
}

func headerContainsToken(values []string, token string) bool {
	for _, value := range commaSeparatedHeaderValues(values) {
		if strings.EqualFold(value, token) {
			return true
		}
	}
	return false
}

func commaSeparatedHeaderValues(values []string) []string {
	var parts []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				parts = append(parts, part)
			}
		}
	}
	return parts
}

func cloneHeader(header http.Header) http.Header {
	if len(header) == 0 {
		return nil
	}
	clone := make(http.Header, len(header))
	for key, values := range header {
		clone[key] = append([]string(nil), values...)
	}
	return clone
}

// cacheBaseKey is the host+URI portion shared by a URI's exact cache key and
// every vary-variant key derived from it. RequestURI keeps origin-form and
// absolute-form targets for the same effective request URI on the same key.
// RFC 9110 section 4.2.3 requires the host component of the target URI
// authority to be normalized to lowercase.
func cacheBaseKey(r *http.Request) string {
	return strings.ToLower(r.Host) + "#" + r.URL.RequestURI()
}

// Include configured request headers in the cache key so one caller's GET
// response is not served to another caller with different cache-relevant
// request metadata.
func cacheKeyForRequest(r *http.Request) string {
	return cacheKeyForRequestWithVaryHeaders(r, defaultVaryHeaders)
}

func cacheKeyForRequestWithVaryHeaders(r *http.Request, varyHeaders []string) string {
	base := cacheBaseKey(r)
	if len(r.Header) == 0 || len(varyHeaders) == 0 {
		return base
	}

	var signature strings.Builder
	for _, name := range varyHeaders {
		values := r.Header.Values(name)
		if len(values) == 0 {
			continue
		}
		values = append([]string(nil), values...)
		sort.Strings(values)
		safeName := strings.ReplaceAll(name, "\n", "\\n")
		signature.WriteString(safeName)
		signature.WriteByte(':')
		escapedValues := make([]string, len(values))
		for i, val := range values {
			escaped := strings.ReplaceAll(val, "\n", "\\n")
			escapedValues[i] = strings.ReplaceAll(escaped, "\x00", "\\0")
		}
		signature.WriteString(strings.Join(escapedValues, "\x00"))
		signature.WriteByte('\n')
	}
	if signature.Len() == 0 {
		return base
	}

	sum := sha256.Sum256([]byte(signature.String()))
	return base + "#h=" + hex.EncodeToString(sum[:])
}
