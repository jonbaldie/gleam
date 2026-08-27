package proxy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
			cacheKey := cacheKeyForRequestWithVaryHeaders(r, varyHeaders)
			if cachedItem, found := c.Get(cacheKey); found {
				for key, values := range cachedItem.Header {
					for _, value := range values {
						w.Header().Add(key, value)
					}
				}
				w.WriteHeader(cachedItem.Status)
				_, _ = w.Write(cachedItem.Content)
				return
			}

			crw := &cacheResponseWriter{ResponseWriter: w, buf: new(bytes.Buffer), status: http.StatusOK}
			p.ServeHTTP(crw, r)

			if crw.status >= http.StatusOK && crw.status < http.StatusMultipleChoices {
				c.Set(cacheKey, cache.CacheItem{Content: crw.buf.Bytes(), Header: crw.Header(), Status: crw.status}, ttl)
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
	buf    *bytes.Buffer
	status int
}

func (w *cacheResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *cacheResponseWriter) Write(b []byte) (int, error) {
	w.buf.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *cacheResponseWriter) Header() http.Header {
	return w.ResponseWriter.Header()
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
