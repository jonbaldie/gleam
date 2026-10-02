package cachepolicy

import (
	"net/http"
	"strings"
)

// CanStoreResponse reports whether a shared cache may store a response with
// the given status and header. varyHeaders are the request headers the cache
// key covers; a response that varies on anything else is not stored. A
// response to an authenticated request is only stored when it explicitly
// permits shared reuse (RFC 9111 section 3.5).
func CanStoreResponse(status int, header http.Header, varyHeaders []string, authenticated bool) bool {
	return responseIsCacheable(status, header, varyHeaders) && CanReuse(header, authenticated)
}

// CanReuse reports whether a stored response with the given header may answer
// a request. An authenticated request may only reuse a response carrying one
// of the directives RFC 9111 section 3.5 accepts as explicit permission for a
// shared cache: public, must-revalidate, or s-maxage.
func CanReuse(header http.Header, authenticated bool) bool {
	if !authenticated {
		return true
	}
	if headerContainsToken(header.Values("Cache-Control"), "public") ||
		headerContainsToken(header.Values("Cache-Control"), "must-revalidate") {
		return true
	}
	_, hasSMaxAge := cacheControlAge(header, "s-maxage")
	return hasSMaxAge
}

func responseIsCacheable(status int, header http.Header, varyHeaders []string) bool {
	if !responseStatusAllowsStorage(status, header) {
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

func responseStatusAllowsStorage(status int, header http.Header) bool {
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return false
	}
	if status == http.StatusPartialContent {
		return false
	}
	// RFC 9111 section 3 permits storing a response only when it has an
	// explicit cacheability directive or a heuristically cacheable status.
	return isHeuristicallyCacheableStatus(status) || responseHasExplicitCacheability(header)
}

func isHeuristicallyCacheableStatus(status int) bool {
	switch status {
	case http.StatusOK, http.StatusNonAuthoritativeInfo, http.StatusNoContent:
		return true
	default:
		return false
	}
}

func responseHasExplicitCacheability(header http.Header) bool {
	if headerContainsToken(header.Values("Cache-Control"), "public") {
		return true
	}
	if _, found := cacheControlAge(header, "s-maxage"); found {
		return true
	}
	if _, found := cacheControlAge(header, "max-age"); found {
		return true
	}
	return len(header.Values("Expires")) > 0
}

// In a shared cache, private responses are user-specific: storing them risks
// leaking one client's data to another.
//
// The qualified forms `private="X-User-Id"` and `no-cache="X-Secret"` (RFC 9111
// sections 5.2.2.7 and 5.2.2.4) name fields a shared cache must not store or
// must revalidate before reuse. Gleam serves hits without revalidation and
// stores whole responses, so it takes the conservative fallback both sections
// permit and declines to store the response at all.
func cacheControlForbidsSharedCacheStorage(header http.Header) bool {
	return cacheControlHasDirective(header, "no-store") ||
		cacheControlHasDirective(header, "no-cache") ||
		cacheControlHasDirective(header, "private")
}

func containsHeaderName(headers []string, target string) bool {
	for _, h := range headers {
		if strings.EqualFold(strings.TrimSpace(h), target) {
			return true
		}
	}
	return false
}
