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
)

func New(origin *url.URL, c cache.Cache, ttl time.Duration) http.Handler {
	return NewWithVaryHeaders(origin, c, ttl, defaultVaryHeaders)
}

func NewWithVaryHeaders(origin *url.URL, c cache.Cache, ttl time.Duration, varyHeaders []string) http.Handler {
	p := httputil.NewSingleHostReverseProxy(origin)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			serveGet(p, c, w, r, varyHeaders, ttl)
			return
		}

		serveNonGet(p, c, w, r)
	})
}

// serveGet answers a GET from the cache when possible, and otherwise forwards
// it to the origin, storing the response when it is both storable and copied
// through to the client in full.
func serveGet(p *httputil.ReverseProxy, c cache.Cache, w http.ResponseWriter, r *http.Request, varyHeaders []string, ttl time.Duration) {
	if requestBypassesCache(r) {
		p.ServeHTTP(w, r)
		return
	}

	// RFC 9111 section 3.5: a shared cache may only reuse — or store — a
	// response to an authenticated request when the response says so
	// explicitly, whatever the request headers the cache key covers.
	authenticated := requestHasAuthorization(r)

	cacheKey := cacheKeyForRequestWithVaryHeaders(r, varyHeaders)
	if cachedItem, found := reusableCachedItem(c, cacheKey, authenticated); found {
		serveCachedItem(w, r, cachedItem)
		return
	}

	crw := &cacheResponseWriter{ResponseWriter: w, buf: new(bytes.Buffer), status: http.StatusOK}
	p.ServeHTTP(crw, r)
	// Reaching here means the reverse proxy returned normally; an abnormal
	// unwind (http.ErrAbortHandler on a copy error) skips this, and so skips
	// storage too.
	crw.proxyReturned = true

	storeResponse(c, cacheKey, crw, varyHeaders, ttl, authenticated)
}

// requestBypassesCache reports whether a GET must go straight to the origin
// without either a cache lookup or storage. Request no-store forbids both
// (RFC 9111 section 5.2.1.5); no-cache demands revalidation this cache does
// not perform; upgrades and ranges are not interchangeable with a full GET.
func requestBypassesCache(r *http.Request) bool {
	return requestForbidsStorage(r) || isUpgradeRequest(r) ||
		requestRequiresRevalidation(r) || requestHasRange(r)
}

// reusableCachedItem returns the stored entry for a key when this cache may
// answer the request from it.
func reusableCachedItem(c cache.Cache, cacheKey string, authenticated bool) (*cache.CacheItem, bool) {
	cachedItem, found := c.Get(cacheKey)
	if !found {
		return nil, false
	}
	if authenticated && !responsePermitsSharedCacheReuseOfAuthorized(cachedItem.Header) {
		return nil, false
	}
	return cachedItem, true
}

// storeResponse caches the origin's response when it is a complete, storable
// representation this shared cache is allowed to reuse.
func storeResponse(c cache.Cache, cacheKey string, crw *cacheResponseWriter, varyHeaders []string, ttl time.Duration, authenticated bool) {
	if !crw.copyComplete() || !responseIsCacheable(crw.status, crw.cachedHeader, varyHeaders) {
		return
	}
	if authenticated && !responsePermitsSharedCacheReuseOfAuthorized(crw.cachedHeader) {
		return
	}

	receivedAt := time.Now()
	responseTTL, shouldStore := cacheTTLForResponse(crw.cachedHeader, ttl, receivedAt)
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

// serveNonGet forwards a non-GET request to the origin and, when the method
// is unsafe and the response is non-error, invalidates every stored entry
// for the target URI (RFC 9111 section 4.4).
func serveNonGet(p *httputil.ReverseProxy, c cache.Cache, w http.ResponseWriter, r *http.Request) {
	srw := &statusResponseWriter{ResponseWriter: w, status: http.StatusOK}
	p.ServeHTTP(srw, r)

	if isUnsafeMethod(r.Method) && isNonErrorResponse(srw.status) {
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

// RFC 9110 section 9.2.1: GET, HEAD, OPTIONS, and TRACE are safe; other
// methods, including those whose safety is unknown, are unsafe (RFC 9111
// section 4.4).
func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	default:
		return true
	}
}

// A non-error response — 2xx or 3xx — to an unsafe request invalidates stored
// responses for the target URI (RFC 9111 section 4.4).
func isNonErrorResponse(status int) bool {
	return status >= http.StatusOK && status < http.StatusBadRequest
}

type cacheResponseWriter struct {
	http.ResponseWriter
	buf          *bytes.Buffer
	status       int
	cachedHeader http.Header
	// proxyReturned records that the reverse proxy finished serving without
	// unwinding, and writeErr the failure of any forwarded body write. A
	// response is only a complete representation when both agree the copy ran
	// to completion; storing anything less would poison the cache with a
	// truncated body (RFC 9111 section 3).
	proxyReturned bool
	writeErr      error
}

func (w *cacheResponseWriter) copyComplete() bool {
	return w.proxyReturned && w.writeErr == nil
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
	// RFC 9110 section 13.2.2: If-None-Match, when present, takes precedence
	// and If-Modified-Since is ignored.
	if len(r.Header.Values("If-None-Match")) > 0 {
		if ifNoneMatchMatches(r.Header.Values("If-None-Match"), item.Header.Get("ETag")) {
			writeCachedNotModified(w, item)
			return
		}
	} else if ifModifiedSinceSatisfied(r.Header.Values("If-Modified-Since"), item.Header.Get("Last-Modified")) {
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
	w.Header().Set("Age", strconv.FormatUint(currentAgeSeconds(item, time.Now()), 10))
}

// currentAgeSeconds is RFC 9111 section 4.2.3's current_age for a stored
// response: the age the origin's copy already had when this cache received it,
// plus the time it has been resident here since.
func currentAgeSeconds(item *cache.CacheItem, now time.Time) uint64 {
	correctedInitialAge := correctedInitialAge(item.Header, item.StoredAt)

	var residentTime time.Duration
	if !item.StoredAt.IsZero() {
		residentTime = now.Sub(item.StoredAt)
	}

	return nonNegativeSeconds(correctedInitialAge + residentTime)
}

// correctedInitialAge is the greater of the age the origin claimed and the age
// apparent from the response's Date, measured at the moment this cache
// received the response.
func correctedInitialAge(header http.Header, receivedAt time.Time) time.Duration {
	var apparentAge time.Duration
	if date, ok := parseHTTPDate(header.Get("Date")); ok && !receivedAt.IsZero() {
		apparentAge = receivedAt.Sub(date)
	}
	if apparentAge < 0 {
		apparentAge = 0
	}

	ageValue, ok := parseAgeValue(header.Get("Age"))
	if ok && ageValue > apparentAge {
		return ageValue
	}
	return apparentAge
}

// parseAgeValue reads an Age field value: a non-negative number of seconds.
// A value that does not parse is ignored (RFC 9111 section 5.1).
func parseAgeValue(raw string) (time.Duration, bool) {
	seconds, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, false
	}
	return saturatingSeconds(seconds), true
}

// saturatingSeconds converts a seconds count to a Duration, pinning values too
// large to represent at the maximum rather than overflowing.
func saturatingSeconds(seconds uint64) time.Duration {
	const maxDuration = time.Duration(1<<63 - 1)
	if seconds > uint64(maxDuration/time.Second) {
		return maxDuration
	}
	return time.Duration(seconds) * time.Second
}

func nonNegativeSeconds(d time.Duration) uint64 {
	if d <= 0 {
		return 0
	}
	return uint64(d / time.Second)
}

func announceCachedTrailers(w http.ResponseWriter, item *cache.CacheItem) {
	for name := range item.Trailer {
		if !headerContainsToken(w.Header().Values("Trailer"), name) {
			w.Header().Add("Trailer", name)
		}
	}
}

type entityTag struct {
	opaque string
}

func ifNoneMatchMatches(ifNoneMatch []string, etag string) bool {
	if len(ifNoneMatch) == 0 {
		return false
	}
	var tags []entityTag
	for _, value := range ifNoneMatch {
		star, valueTags := parseIfNoneMatchValue(value)
		if star {
			return true
		}
		tags = append(tags, valueTags...)
	}

	cached, ok := parseEntityTag(etag)
	if !ok {
		return false
	}
	for _, tag := range tags {
		if tag.opaque == cached.opaque {
			return true
		}
	}
	return false
}

// ifModifiedSinceSatisfied reports whether a stored representation with the
// given Last-Modified date has not been modified since the request's
// If-Modified-Since date, i.e. whether it can be answered with 304 locally.
// A condition that is absent, or whose date cannot be parsed as an HTTP date,
// is ignored rather than treated as a match (RFC 9110 section 13.1.3).
func ifModifiedSinceSatisfied(ifModifiedSince []string, lastModified string) bool {
	if len(ifModifiedSince) == 0 {
		return false
	}
	stored, ok := parseHTTPDate(lastModified)
	if !ok {
		return false
	}
	for _, value := range ifModifiedSince {
		condition, ok := parseHTTPDate(value)
		if !ok {
			continue
		}
		if !stored.After(condition) {
			return true
		}
	}
	return false
}

func parseHTTPDate(raw string) (time.Time, bool) {
	t, err := http.ParseTime(strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func parseEntityTag(raw string) (entityTag, bool) {
	raw = strings.TrimSpace(raw)
	tag, n, ok := scanEntityTag(raw)
	if !ok || strings.TrimSpace(raw[n:]) != "" {
		return entityTag{}, false
	}
	return tag, true
}

func parseIfNoneMatchValue(value string) (star bool, tags []entityTag) {
	rest := strings.TrimSpace(value)
	for rest != "" {
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			break
		}
		if isStarToken(rest) {
			return true, tags
		}
		tag, n, ok := scanEntityTag(rest)
		if !ok {
			rest = skipToNextListItem(rest)
			continue
		}
		tags = append(tags, tag)
		rest = consumeListSeparator(rest[n:])
	}
	return false, tags
}

func isStarToken(s string) bool {
	if s[0] != '*' {
		return false
	}
	after := strings.TrimLeft(s[1:], " \t")
	return after == "" || after[0] == ','
}

func skipToNextListItem(s string) string {
	comma := strings.IndexByte(s, ',')
	if comma < 0 {
		return ""
	}
	return s[comma+1:]
}

func consumeListSeparator(s string) string {
	s = strings.TrimLeft(s, " \t")
	if s == "" || s[0] != ',' {
		return ""
	}
	return s[1:]
}

func scanEntityTag(s string) (entityTag, int, bool) {
	i := 0
	end := len(s)
	if end >= 2 && s[0] == 'W' && s[1] == '/' {
		i = 2
	}
	if i >= end || s[i] != '"' {
		return entityTag{}, 0, false
	}
	j := i + 1
	for j < end && s[j] != '"' {
		j++
	}
	if j >= end {
		return entityTag{}, 0, false
	}
	return entityTag{opaque: s[i : j+1]}, j + 1, true
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

func requestHasAuthorization(r *http.Request) bool {
	for _, value := range r.Header.Values("Authorization") {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

// responsePermitsSharedCacheReuseOfAuthorized reports whether a response
// carries one of the directives RFC 9111 section 3.5 accepts as explicit
// permission for a shared cache to reuse it for a request bearing
// Authorization.
func responsePermitsSharedCacheReuseOfAuthorized(header http.Header) bool {
	if headerContainsToken(header.Values("Cache-Control"), "public") ||
		headerContainsToken(header.Values("Cache-Control"), "must-revalidate") {
		return true
	}
	_, hasSMaxAge := cacheControlAge(header, "s-maxage")
	return hasSMaxAge
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

// cacheTTLForResponse caps the configured cache retention at the freshness
// the origin's response has left: its advertised freshness lifetime less the
// age the response already carried when it arrived here (RFC 9111 sections
// 4.2 and 4.2.3). Responses with no usable freshness directive retain the
// configured TTL; responses that are already stale are not stored because
// cache hits are served without revalidation.
func cacheTTLForResponse(header http.Header, configuredTTL time.Duration, receivedAt time.Time) (time.Duration, bool) {
	freshnessLifetime, hasFreshness := responseFreshnessLifetime(header, receivedAt)
	if !hasFreshness {
		return configuredTTL, true
	}
	remainingFreshness := freshnessLifetime - correctedInitialAge(header, receivedAt)
	if remainingFreshness <= 0 {
		return 0, false
	}
	if remainingFreshness < configuredTTL {
		return remainingFreshness, true
	}
	return configuredTTL, true
}

func responseFreshnessLifetime(header http.Header, receivedAt time.Time) (time.Duration, bool) {
	if freshness, found := cacheControlAge(header, "s-maxage"); found {
		return freshness, true
	}
	if freshness, found := cacheControlAge(header, "max-age"); found {
		return freshness, true
	}

	if len(header.Values("Expires")) > 0 {
		referenceTime := receivedAt
		if date, dateOK := parseHTTPDate(header.Get("Date")); dateOK {
			referenceTime = date
		} else if referenceTime.IsZero() {
			referenceTime = time.Now()
		}

		expires, expiresOK := parseHTTPDate(header.Get("Expires"))
		if !expiresOK || !expires.After(referenceTime) {
			// RFC 9111 section 5.3: A cache recipient MUST interpret invalid
			// date formats, especially the value "0", as representing a time in
			// the past (i.e., "already expired").
			return 0, true
		}
		return expires.Sub(referenceTime), true
	}
	return 0, false
}

func cacheControlAge(header http.Header, target string) (time.Duration, bool) {
	for _, value := range header.Values("Cache-Control") {
		for _, directive := range strings.Split(value, ",") {
			name, argument, hasArgument := strings.Cut(directive, "=")
			if !hasArgument || !strings.EqualFold(strings.TrimSpace(name), target) {
				continue
			}
			if age, ok := parseCacheControlAge(argument); ok {
				return age, true
			}
		}
	}
	return 0, false
}

func parseCacheControlAge(raw string) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		raw = strings.TrimSpace(raw[1 : len(raw)-1])
	}
	seconds, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, false
	}

	maxDuration := time.Duration(1<<63 - 1)
	if seconds > uint64(maxDuration/time.Second) {
		return maxDuration, true
	}
	return time.Duration(seconds) * time.Second, true
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

// cacheBaseKey is the host+URI portion shared by a URI's exact cache key and
// every vary-variant key derived from it.
func cacheBaseKey(r *http.Request) string {
	return r.Host + "#" + r.URL.String()
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
