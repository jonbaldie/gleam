package cachepolicy

import (
	"net/http"
	"strings"
)

// ShouldBypass reports whether a GET must go straight to the origin without
// either a cache lookup or storage. Request no-store forbids both (RFC 9111
// section 5.2.1.5); no-cache demands revalidation this cache does not perform;
// upgrades and ranges are not interchangeable with a full GET.
func ShouldBypass(r *http.Request) bool {
	return cacheControlHasDirective(r.Header, "no-store") || isUpgradeRequest(r) ||
		cacheControlHasDirective(r.Header, "no-cache") || requestHasRange(r)
}

// OnlyIfCached reports whether the request asks to be answered from storage or
// not at all (RFC 9111 section 5.2.1.7).
func OnlyIfCached(r *http.Request) bool {
	return cacheControlHasDirective(r.Header, "only-if-cached")
}

// IsAuthenticated reports whether the request carries Authorization
// credentials, which restrict what a shared cache may store and reuse for it
// (RFC 9111 section 3.5).
func IsAuthenticated(r *http.Request) bool {
	for _, value := range r.Header.Values("Authorization") {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

// InvalidatesStoredResponses reports whether a response with the given status
// to a request with the given method invalidates the stored responses for its
// target URI: a non-error (2xx or 3xx) response to an unsafe method (RFC 9111
// section 4.4).
func InvalidatesStoredResponses(method string, status int) bool {
	return isUnsafeMethod(method) && isNonErrorResponse(status)
}

func isUpgradeRequest(r *http.Request) bool {
	return headerContainsToken(r.Header.Values("Connection"), "upgrade") && r.Header.Get("Upgrade") != ""
}

// Range requests select a partial representation, so their responses are not
// interchangeable with a full GET's: bypass the cache in both directions
// rather than keying on the Range header.
func requestHasRange(r *http.Request) bool {
	return len(r.Header.Values("Range")) > 0
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

func isNonErrorResponse(status int) bool {
	return status >= http.StatusOK && status < http.StatusBadRequest
}
