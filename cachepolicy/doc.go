// Package cachepolicy holds the HTTP caching rules a shared cache applies:
// which requests it may answer from storage, which responses it may store and
// for how long, how old a stored response is, when a stored response answers a
// conditional request with 304, and which responses invalidate storage
// (RFC 9110 and RFC 9111).
//
// Every function is pure: it reads headers, statuses, and times, and holds no
// state, so the rules can be tested without any HTTP machinery.
package cachepolicy
