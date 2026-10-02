package cachepolicy_test

import (
	"net/http"
	"testing"

	"jonbaldie/gleam/cachepolicy"
)

func TestCanStoreResponse(t *testing.T) {
	varyConfig := []string{"Authorization", "Cookie"}

	tests := []struct {
		name        string
		status      int
		header      http.Header
		varyHeaders []string
		want        bool
	}{
		{
			name:        "200 OK without Vary",
			status:      http.StatusOK,
			header:      http.Header{},
			varyHeaders: varyConfig,
			want:        true,
		},
		{
			name:        "201 Created without explicit cacheability",
			status:      http.StatusCreated,
			header:      http.Header{},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "201 Created with public",
			status:      http.StatusCreated,
			header:      http.Header{"Cache-Control": []string{"public"}},
			varyHeaders: varyConfig,
			want:        true,
		},
		{
			name:        "201 Created with s-maxage",
			status:      http.StatusCreated,
			header:      http.Header{"Cache-Control": []string{"s-maxage=60"}},
			varyHeaders: varyConfig,
			want:        true,
		},
		{
			name:        "202 Accepted without explicit cacheability",
			status:      http.StatusAccepted,
			header:      http.Header{},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "202 Accepted with max-age",
			status:      http.StatusAccepted,
			header:      http.Header{"Cache-Control": []string{"max-age=60"}},
			varyHeaders: varyConfig,
			want:        true,
		},
		{
			name:        "202 Accepted with Expires",
			status:      http.StatusAccepted,
			header:      http.Header{"Expires": []string{"Thu, 10 Sep 2026 00:01:00 GMT"}},
			varyHeaders: varyConfig,
			want:        true,
		},
		{
			name:        "204 No Content without Vary",
			status:      http.StatusNoContent,
			header:      http.Header{},
			varyHeaders: varyConfig,
			want:        true,
		},
		{
			name:        "206 Partial Content",
			status:      http.StatusPartialContent,
			header:      http.Header{},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "206 Partial Content with public",
			status:      http.StatusPartialContent,
			header:      http.Header{"Cache-Control": []string{"public"}},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "299 boundary status",
			status:      299,
			header:      http.Header{},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "203 Non-Authoritative Info without Vary",
			status:      http.StatusNonAuthoritativeInfo,
			header:      http.Header{},
			varyHeaders: varyConfig,
			want:        true,
		},
		{
			name:        "205 Reset Content without explicit cacheability",
			status:      http.StatusResetContent,
			header:      http.Header{},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "199 status below OK",
			status:      199,
			header:      http.Header{},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "300 Multiple Choices",
			status:      http.StatusMultipleChoices,
			header:      http.Header{},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "404 Not Found",
			status:      http.StatusNotFound,
			header:      http.Header{},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "500 Internal Server Error",
			status:      http.StatusInternalServerError,
			header:      http.Header{},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "Cache-Control no-store",
			status:      http.StatusOK,
			header:      http.Header{"Cache-Control": []string{"no-store"}},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "Cache-Control no-cache",
			status:      http.StatusOK,
			header:      http.Header{"Cache-Control": []string{"no-cache"}},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "Cache-Control no-cache with max-age",
			status:      http.StatusOK,
			header:      http.Header{"Cache-Control": []string{"no-cache, max-age=60"}},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "Cache-Control no-cache uppercase",
			status:      http.StatusOK,
			header:      http.Header{"Cache-Control": []string{"NO-CACHE"}},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "Cache-Control no-cache in comma list",
			status:      http.StatusOK,
			header:      http.Header{"Cache-Control": []string{"public, no-cache, max-age=60"}},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "Cache-Control private",
			status:      http.StatusOK,
			header:      http.Header{"Cache-Control": []string{"private"}},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "Cache-Control private with max-age",
			status:      http.StatusOK,
			header:      http.Header{"Cache-Control": []string{"private, max-age=3600"}},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "Cache-Control private uppercase",
			status:      http.StatusOK,
			header:      http.Header{"Cache-Control": []string{"PRIVATE"}},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "Set-Cookie header",
			status:      http.StatusOK,
			header:      http.Header{"Set-Cookie": []string{"session=1; Path=/"}},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "multiple Set-Cookie headers",
			status:      http.StatusOK,
			header:      http.Header{"Set-Cookie": []string{"a=1; Path=/", "b=2; Path=/"}},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "public response with max-age still cacheable",
			status:      http.StatusOK,
			header:      http.Header{"Cache-Control": []string{"public, max-age=60"}},
			varyHeaders: varyConfig,
			want:        true,
		},
		{
			name:        "Cache-Control no-store in comma list",
			status:      http.StatusOK,
			header:      http.Header{"Cache-Control": []string{"private, no-store, max-age=0"}},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "Vary empty string",
			status:      http.StatusOK,
			header:      http.Header{"Vary": []string{""}},
			varyHeaders: varyConfig,
			want:        true,
		},
		{
			name:        "Vary whitespace and commas only",
			status:      http.StatusOK,
			header:      http.Header{"Vary": []string{"  ,  "}},
			varyHeaders: varyConfig,
			want:        true,
		},
		{
			name:        "Vary star",
			status:      http.StatusOK,
			header:      http.Header{"Vary": []string{"*"}},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "Vary star with whitespace",
			status:      http.StatusOK,
			header:      http.Header{"Vary": []string{"  *  "}},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "Vary star in list",
			status:      http.StatusOK,
			header:      http.Header{"Vary": []string{"Cookie, *"}},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "Vary single configured header",
			status:      http.StatusOK,
			header:      http.Header{"Vary": []string{"Cookie"}},
			varyHeaders: varyConfig,
			want:        true,
		},
		{
			name:        "Vary all configured headers",
			status:      http.StatusOK,
			header:      http.Header{"Vary": []string{"Cookie, Authorization"}},
			varyHeaders: varyConfig,
			want:        true,
		},
		{
			name:        "Vary case-insensitive lowercase",
			status:      http.StatusOK,
			header:      http.Header{"Vary": []string{"cookie, authorization"}},
			varyHeaders: varyConfig,
			want:        true,
		},
		{
			name:        "Vary case-insensitive uppercase",
			status:      http.StatusOK,
			header:      http.Header{"Vary": []string{"COOKIE"}},
			varyHeaders: varyConfig,
			want:        true,
		},
		{
			name:        "Vary unconfigured header",
			status:      http.StatusOK,
			header:      http.Header{"Vary": []string{"X-Custom"}},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "Vary mix of configured and unconfigured",
			status:      http.StatusOK,
			header:      http.Header{"Vary": []string{"Cookie, X-Custom"}},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "Vary with empty configured headers",
			status:      http.StatusOK,
			header:      http.Header{"Vary": []string{"Cookie"}},
			varyHeaders: []string{},
			want:        false,
		},
		{
			name:        "No Vary with empty configured headers",
			status:      http.StatusOK,
			header:      http.Header{},
			varyHeaders: []string{},
			want:        true,
		},
		{
			name:        "Configured headers with whitespace",
			status:      http.StatusOK,
			header:      http.Header{"Vary": []string{"Cookie"}},
			varyHeaders: []string{"  Cookie  "},
			want:        true,
		},
		{
			name:        "Vary star is not storable even when configured",
			status:      http.StatusOK,
			header:      http.Header{"Vary": []string{"*"}},
			varyHeaders: []string{"*"},
			want:        false,
		},
		{
			name:        "1xx with explicit cacheability",
			status:      http.StatusContinue,
			header:      http.Header{"Cache-Control": []string{"public, max-age=60"}},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "300 with explicit cacheability",
			status:      http.StatusMultipleChoices,
			header:      http.Header{"Cache-Control": []string{"public, max-age=60"}},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "404 with explicit cacheability",
			status:      http.StatusNotFound,
			header:      http.Header{"Cache-Control": []string{"max-age=60"}},
			varyHeaders: varyConfig,
			want:        false,
		},
		{
			name:        "206 with explicit cacheability",
			status:      http.StatusPartialContent,
			header:      http.Header{"Cache-Control": []string{"public, max-age=60"}},
			varyHeaders: varyConfig,
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cachepolicy.CanStoreResponse(tt.status, tt.header, tt.varyHeaders, false)
			if got != tt.want {
				t.Errorf("CanStoreResponse() = %v, want %v", got, tt.want)
			}
		})
	}
}

// RFC 9111 section 3.5: a response to an authenticated request is only
// storable, and a stored response only reusable for one, when the response
// explicitly permits it.
func TestAuthenticatedStorageAndReuse(t *testing.T) {
	tests := []struct {
		name         string
		cacheControl []string
		want         bool
	}{
		{name: "no directives", cacheControl: nil, want: false},
		{name: "max-age alone", cacheControl: []string{"max-age=60"}, want: false},
		{name: "public", cacheControl: []string{"public, max-age=60"}, want: true},
		{name: "must-revalidate", cacheControl: []string{"must-revalidate, max-age=60"}, want: true},
		{name: "s-maxage", cacheControl: []string{"s-maxage=60"}, want: true},
		{name: "invalid s-maxage still explicit", cacheControl: []string{"s-maxage=bad"}, want: true},
		{name: "case-insensitive", cacheControl: []string{"PUBLIC"}, want: true},
		{name: "second field", cacheControl: []string{"max-age=60", "public"}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := http.Header{}
			for _, value := range tt.cacheControl {
				header.Add("Cache-Control", value)
			}
			if got := cachepolicy.CanStoreResponse(http.StatusOK, header, nil, true); got != tt.want {
				t.Errorf("CanStoreResponse(authenticated) = %v, want %v", got, tt.want)
			}
			if got := cachepolicy.CanReuse(header, true); got != tt.want {
				t.Errorf("CanReuse(authenticated) = %v, want %v", got, tt.want)
			}
			if !cachepolicy.CanReuse(header, false) {
				t.Error("CanReuse(unauthenticated) = false, want true")
			}
		})
	}
}

func TestAuthenticatedStorageStillAppliesStorageRules(t *testing.T) {
	header := http.Header{"Cache-Control": {"public, no-store"}}
	if cachepolicy.CanStoreResponse(http.StatusOK, header, nil, true) {
		t.Fatal("CanStoreResponse() = true for no-store, want false")
	}
}
