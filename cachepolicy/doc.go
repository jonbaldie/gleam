// Package cachepolicy holds the HTTP caching rules a shared cache applies:
// which requests it may answer from storage, which responses it may store and
// for how long, how old a stored response is, when a stored response answers a
// conditional request with 304, and which responses invalidate storage
// (RFC 9110 and RFC 9111).
//
// Every function is stateless: it reads headers, statuses, and the times the
// caller supplies, so the rules can be tested without any HTTP machinery. The
// one exception is FreshnessLifetime, which falls back to the wall clock when
// given a zero receipt time and no usable Date header.
package cachepolicy
