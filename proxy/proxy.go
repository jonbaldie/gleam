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
	"strings"
	"time"

	"jonbaldie/gleam/cache"
)

func New(origin *url.URL, c cache.Cache, ttl time.Duration) http.Handler {
	return NewWithVaryHeaders(origin, c, ttl, defaultVaryHeaders)
}

func NewWithVaryHeaders(origin *url.URL, c cache.Cache, ttl time.Duration, varyHeaders []string) http.Handler {
	p := httputil.NewSingleHostReverseProxy(origin)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// Request no-store forbids both serving from and storing in
			// the cache (RFC 9111 section 5.2.1.5), so skip it entirely.
			noStore := requestForbidsStorage(r)
			if noStore || isUpgradeRequest(r) || requestRequiresRevalidation(r) || requestHasRange(r) {
				p.ServeHTTP(w, r)
				return
			}

			cacheKey := cacheKeyForRequestWithVaryHeaders(r, varyHeaders)
			if cachedItem, found := c.Get(cacheKey); found {
				writeCachedItem(w, cachedItem)
				return
			}

			crw := &cacheResponseWriter{ResponseWriter: w, buf: new(bytes.Buffer), status: http.StatusOK}
			p.ServeHTTP(crw, r)

			if responseIsCacheable(crw.status, crw.cachedHeader, varyHeaders) {
				c.Set(cacheKey, cache.CacheItem{
					Content: crw.buf.Bytes(),
					Header:  crw.cachedHeader,
					Trailer: crw.cachedTrailer(),
					Status:  crw.status,
				}, ttl)
			}
			return
		}

		p.ServeHTTP(w, r)
	})
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

type cacheResponseWriter struct {
	http.ResponseWriter
	buf          *bytes.Buffer
	status       int
	cachedHeader http.Header
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
	return w.ResponseWriter.Write(b)
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
	if len(trailers) == 0 {
		return nil
	}
	return trailers
}

func writeCachedItem(w http.ResponseWriter, item *cache.CacheItem) {
	for key, values := range item.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	for name := range item.Trailer {
		if !headerContainsToken(w.Header().Values("Trailer"), name) {
			w.Header().Add("Trailer", name)
		}
	}

	w.WriteHeader(item.Status)
	_, _ = w.Write(item.Content)

	for key, values := range item.Trailer {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
}

func isUpgradeRequest(r *http.Request) bool {
	return headerContainsToken(r.Header.Values("Connection"), "upgrade") && r.Header.Get("Upgrade") != ""
}

func requestRequiresRevalidation(r *http.Request) bool {
	return headerContainsToken(r.Header.Values("Cache-Control"), "no-cache")
}

func requestForbidsStorage(r *http.Request) bool {
	return headerContainsToken(r.Header.Values("Cache-Control"), "no-store")
}

// Range requests select a partial representation, so their responses are not
// interchangeable with a full GET's: bypass the cache in both directions
// rather than keying on the Range header.
func requestHasRange(r *http.Request) bool {
	return len(r.Header.Values("Range")) > 0
}

func responseIsCacheable(status int, header http.Header, varyHeaders []string) bool {
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return false
	}
	if status == http.StatusPartialContent {
		return false
	}
	if cacheControlForbidsSharedCacheStorage(header) {
		return false
	}
	// In a shared cache, cookie-setting responses are user-specific: storing
	// them risks replaying one client's cookies to another.
	if len(header.Values("Set-Cookie")) > 0 {
		return false
	}
	for _, token := range commaSeparatedHeaderValues(header.Values("Vary")) {
		if token == "*" || !containsHeaderName(varyHeaders, token) {
			return false
		}
	}
	return true
}

// In a shared cache, private responses are user-specific: storing them risks
// leaking one client's data to another.
func cacheControlForbidsSharedCacheStorage(header http.Header) bool {
	return headerContainsToken(header.Values("Cache-Control"), "no-store") ||
		headerContainsToken(header.Values("Cache-Control"), "no-cache") ||
		headerContainsToken(header.Values("Cache-Control"), "private")
}

func containsHeaderName(headers []string, target string) bool {
	for _, h := range headers {
		if strings.EqualFold(strings.TrimSpace(h), target) {
			return true
		}
	}
	return false
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

// Include configured request headers in the cache key so one caller's GET
// response is not served to another caller with different cache-relevant
// request metadata.
func cacheKeyForRequest(r *http.Request) string {
	return cacheKeyForRequestWithVaryHeaders(r, defaultVaryHeaders)
}

func cacheKeyForRequestWithVaryHeaders(r *http.Request, varyHeaders []string) string {
	base := r.Host + "#" + r.URL.String()
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
