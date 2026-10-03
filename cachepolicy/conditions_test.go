package cachepolicy_test

import (
	"net/http"
	"testing"

	"jonbaldie/gleam/cachepolicy"
)

// notModified evaluates a request's validators against a stored 200.
func notModified(reqHeader, storedHeader http.Header) bool {
	return cachepolicy.NotModified(reqHeader, http.StatusOK, storedHeader)
}

func TestNotModifiedIfNoneMatch(t *testing.T) {
	tests := []struct {
		name        string
		ifNoneMatch []string
		etag        string
		want        bool
	}{
		{name: "empty validators", ifNoneMatch: nil, etag: `"a"`, want: false},
		{name: "empty etag", ifNoneMatch: []string{`"a"`}, etag: "", want: false},
		{name: "exact strong match", ifNoneMatch: []string{`"a"`}, etag: `"a"`, want: true},
		{name: "weak comparison ignores weakness", ifNoneMatch: []string{`W/"a"`}, etag: `"a"`, want: true},
		{name: "quoted comma stays one tag", ifNoneMatch: []string{`"a,b"`}, etag: `"a,b"`, want: true},
		{name: "quoted comma does not split", ifNoneMatch: []string{`"a,b"`}, etag: `"a"`, want: false},
		{name: "multiple header values", ifNoneMatch: []string{`"x"`, `"a"`}, etag: `"a"`, want: true},
		{name: "star", ifNoneMatch: []string{"*"}, etag: `"a"`, want: true},
		{name: "star without etag", ifNoneMatch: []string{"*"}, etag: "", want: true},
		{name: "malformed etag", ifNoneMatch: []string{`"a"`}, etag: "a", want: false},
		{name: "mismatch", ifNoneMatch: []string{`"b"`}, etag: `"a"`, want: false},
		{name: "surrounding whitespace on etag", ifNoneMatch: []string{`"a"`}, etag: `  "a"  `, want: true},
		{name: "star after another tag", ifNoneMatch: []string{`"x", *`}, etag: `"a"`, want: true},
		{name: "star with trailing comma", ifNoneMatch: []string{"*,"}, etag: `"a"`, want: true},
		{name: "star with trailing space", ifNoneMatch: []string{"* "}, etag: `"a"`, want: true},
		{name: "skips malformed then matches", ifNoneMatch: []string{`foo, "a"`}, etag: `"a"`, want: true},
		{name: "etag with extra tokens", ifNoneMatch: []string{`"a"`}, etag: `"a" extra`, want: false},
		{name: "no space after comma", ifNoneMatch: []string{`"x","a"`}, etag: `"a"`, want: true},
		{name: "space before comma", ifNoneMatch: []string{`"x" , "a"`}, etag: `"a"`, want: true},
		{name: "leading empty member", ifNoneMatch: []string{`, "a"`}, etag: `"a"`, want: true},
		{name: "malformed member without space", ifNoneMatch: []string{`foo,"a"`}, etag: `"a"`, want: true},
		{name: "junk after tag ends the list", ifNoneMatch: []string{`"x"x,"a"`}, etag: `"a"`, want: false},
		{name: "bare token is not star", ifNoneMatch: []string{"x"}, etag: `"a"`, want: false},
		{name: "bare token before tag is not star", ifNoneMatch: []string{`x, "b"`}, etag: `"a"`, want: false},
		{name: "lone W", ifNoneMatch: []string{"W"}, etag: `"a"`, want: false},
		{name: "W without slash", ifNoneMatch: []string{`Wx"a"`}, etag: `"a"`, want: false},
		{name: "slash without W", ifNoneMatch: []string{`X/"a"`}, etag: `"a"`, want: false},
		{name: "unterminated tag", ifNoneMatch: []string{`"abc`}, etag: `"abc"`, want: false},
		{name: "unterminated stored etag", ifNoneMatch: []string{`"abc"`}, etag: `"abc`, want: false},
		{name: "empty opaque tag", ifNoneMatch: []string{`""`}, etag: `""`, want: true},
		{name: "weak stored etag", ifNoneMatch: []string{`"a"`}, etag: `W/"a"`, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := notModified(http.Header{"If-None-Match": tt.ifNoneMatch}, http.Header{"Etag": {tt.etag}}); got != tt.want {
				t.Fatalf("NotModified(If-None-Match %q, %q) = %v, want %v", tt.ifNoneMatch, tt.etag, got, tt.want)
			}
		})
	}
}

func TestNotModifiedIfModifiedSince(t *testing.T) {
	const rfc1123 = "Thu, 10 Sep 2026 03:58:45 GMT"

	tests := []struct {
		name            string
		ifModifiedSince []string
		lastModified    string
		want            bool
	}{
		{name: "no condition", ifModifiedSince: nil, lastModified: rfc1123, want: false},
		{name: "no stored last-modified", ifModifiedSince: []string{rfc1123}, lastModified: "", want: false},
		{name: "equal dates", ifModifiedSince: []string{rfc1123}, lastModified: rfc1123, want: true},
		{name: "condition after last-modified", ifModifiedSince: []string{"Fri, 11 Sep 2026 00:00:00 GMT"}, lastModified: rfc1123, want: true},
		{name: "condition before last-modified", ifModifiedSince: []string{"Wed, 09 Sep 2026 00:00:00 GMT"}, lastModified: rfc1123, want: false},
		{name: "unparseable condition", ifModifiedSince: []string{"not a date"}, lastModified: rfc1123, want: false},
		{name: "unparseable last-modified", ifModifiedSince: []string{rfc1123}, lastModified: "yesterday", want: false},
		{name: "surrounding whitespace", ifModifiedSince: []string{"  " + rfc1123 + "  "}, lastModified: rfc1123, want: true},
		{name: "rfc850 format", ifModifiedSince: []string{"Thursday, 10-Sep-26 03:58:45 GMT"}, lastModified: rfc1123, want: true},
		{name: "asctime format", ifModifiedSince: []string{"Thu Sep 10 03:58:45 2026"}, lastModified: rfc1123, want: true},
		{name: "two valid values ignored", ifModifiedSince: []string{rfc1123, "Fri, 11 Sep 2026 00:00:00 GMT"}, lastModified: rfc1123, want: false},
		{name: "two values matching stored date ignored", ifModifiedSince: []string{rfc1123, rfc1123}, lastModified: rfc1123, want: false},
		{name: "one unparseable then a valid value ignored", ifModifiedSince: []string{"garbage", rfc1123}, lastModified: rfc1123, want: false},
		{name: "two header values ignored", ifModifiedSince: []string{"garbage", rfc1123}, lastModified: rfc1123, want: false},
		{name: "two valid values with non-matching dates", ifModifiedSince: []string{"Wed, 09 Sep 2026 00:00:00 GMT", "Wed, 09 Sep 2026 00:00:00 GMT"}, lastModified: rfc1123, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := notModified(http.Header{"If-Modified-Since": tt.ifModifiedSince}, http.Header{"Last-Modified": {tt.lastModified}}); got != tt.want {
				t.Fatalf("NotModified(If-Modified-Since %q, %q) = %v, want %v", tt.ifModifiedSince, tt.lastModified, got, tt.want)
			}
		})
	}
}

// RFC 9110 section 8.8.2: a Last-Modified field with more than one member is
// ignored, so the resource has no modification date and If-Modified-Since is
// ignored too (section 13.1.3).
func TestNotModifiedIfModifiedSinceMultipleLastModified(t *testing.T) {
	tests := []struct {
		name         string
		lastModified []string
	}{
		{name: "later value after condition", lastModified: []string{"Sat, 03 Oct 2026 01:00:00 GMT", "Sat, 03 Oct 2026 03:00:00 GMT"}},
		{name: "both values before condition", lastModified: []string{"Sat, 03 Oct 2026 01:00:00 GMT", "Sat, 03 Oct 2026 01:30:00 GMT"}},
		{name: "identical values", lastModified: []string{"Sat, 03 Oct 2026 01:00:00 GMT", "Sat, 03 Oct 2026 01:00:00 GMT"}},
		{name: "unparseable then valid value", lastModified: []string{"garbage", "Sat, 03 Oct 2026 01:00:00 GMT"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqHeader := http.Header{"If-Modified-Since": {"Sat, 03 Oct 2026 02:00:00 GMT"}}
			if notModified(reqHeader, http.Header{"Last-Modified": tt.lastModified}) {
				t.Fatalf("NotModified with Last-Modified %q = true, want false", tt.lastModified)
			}
		})
	}
}

func TestNotModifiedPrecedenceAndStatus(t *testing.T) {
	const lastModified = "Thu, 10 Sep 2026 03:58:45 GMT"
	stored := http.Header{"Etag": {`"a"`}, "Last-Modified": {lastModified}}

	tests := []struct {
		name         string
		reqHeader    http.Header
		storedStatus int
		want         bool
	}{
		{name: "no validators", reqHeader: http.Header{}, storedStatus: http.StatusOK, want: false},
		{
			name:         "If-None-Match mismatch overrides satisfied If-Modified-Since",
			reqHeader:    http.Header{"If-None-Match": {`"b"`}, "If-Modified-Since": {lastModified}},
			storedStatus: http.StatusOK,
			want:         false,
		},
		{
			name:         "If-None-Match match with unsatisfied If-Modified-Since",
			reqHeader:    http.Header{"If-None-Match": {`"a"`}, "If-Modified-Since": {"Wed, 09 Sep 2026 00:00:00 GMT"}},
			storedStatus: http.StatusOK,
			want:         true,
		},
		{
			name:         "matching If-None-Match on stored non-200",
			reqHeader:    http.Header{"If-None-Match": {`"a"`}},
			storedStatus: http.StatusNonAuthoritativeInfo,
			want:         false,
		},
		{
			name:         "satisfied If-Modified-Since on stored non-200",
			reqHeader:    http.Header{"If-Modified-Since": {lastModified}},
			storedStatus: http.StatusNoContent,
			want:         false,
		},
		{
			name:         "star on stored non-200",
			reqHeader:    http.Header{"If-None-Match": {"*"}},
			storedStatus: http.StatusNoContent,
			want:         false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cachepolicy.NotModified(tt.reqHeader, tt.storedStatus, stored); got != tt.want {
				t.Fatalf("NotModified() = %v, want %v", got, tt.want)
			}
		})
	}
}
