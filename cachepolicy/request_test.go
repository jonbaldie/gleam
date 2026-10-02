package cachepolicy_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"jonbaldie/gleam/cachepolicy"
)

func newGET(header http.Header) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/resource", nil)
	r.Header = header
	return r
}

func TestShouldBypass(t *testing.T) {
	tests := []struct {
		name   string
		header http.Header
		want   bool
	}{
		{name: "plain GET", header: http.Header{}, want: false},
		{name: "no-store", header: http.Header{"Cache-Control": {"no-store"}}, want: true},
		{name: "no-cache", header: http.Header{"Cache-Control": {"no-cache"}}, want: true},
		{name: "qualified no-cache", header: http.Header{"Cache-Control": {`no-cache="X-Secret"`}}, want: true},
		{name: "directive case-insensitive", header: http.Header{"Cache-Control": {"No-Store"}}, want: true},
		{name: "directive in second field", header: http.Header{"Cache-Control": {"max-age=0", "no-cache"}}, want: true},
		{name: "unrelated directives", header: http.Header{"Cache-Control": {"max-age=60, only-if-cached"}}, want: false},
		{name: "directive inside quoted argument", header: http.Header{"Cache-Control": {`private="no-store, x"`}}, want: false},
		{name: "range", header: http.Header{"Range": {"bytes=0-1"}}, want: true},
		{name: "empty range field", header: http.Header{"Range": {""}}, want: true},
		{name: "upgrade", header: http.Header{"Connection": {"keep-alive, Upgrade"}, "Upgrade": {"websocket"}}, want: true},
		{name: "upgrade without Connection token", header: http.Header{"Upgrade": {"websocket"}}, want: false},
		{name: "Connection upgrade without Upgrade", header: http.Header{"Connection": {"upgrade"}}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cachepolicy.ShouldBypass(newGET(tt.header)); got != tt.want {
				t.Fatalf("ShouldBypass(%v) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}

func TestOnlyIfCached(t *testing.T) {
	tests := []struct {
		name   string
		header http.Header
		want   bool
	}{
		{name: "absent", header: http.Header{}, want: false},
		{name: "present", header: http.Header{"Cache-Control": {"only-if-cached"}}, want: true},
		{name: "among others", header: http.Header{"Cache-Control": {"max-stale, ONLY-IF-CACHED"}}, want: true},
		{name: "other directive", header: http.Header{"Cache-Control": {"no-cache"}}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cachepolicy.OnlyIfCached(newGET(tt.header)); got != tt.want {
				t.Fatalf("OnlyIfCached(%v) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}

func TestIsAuthenticated(t *testing.T) {
	tests := []struct {
		name   string
		header http.Header
		want   bool
	}{
		{name: "absent", header: http.Header{}, want: false},
		{name: "credentials", header: http.Header{"Authorization": {"Bearer x"}}, want: true},
		{name: "blank value", header: http.Header{"Authorization": {"  "}}, want: false},
		{name: "blank then credentials", header: http.Header{"Authorization": {"", "Basic y"}}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cachepolicy.IsAuthenticated(newGET(tt.header)); got != tt.want {
				t.Fatalf("IsAuthenticated(%v) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}

func TestInvalidatesStoredResponses(t *testing.T) {
	tests := []struct {
		method string
		status int
		want   bool
	}{
		{method: http.MethodPost, status: http.StatusOK, want: true},
		{method: http.MethodPut, status: http.StatusNoContent, want: true},
		{method: http.MethodDelete, status: http.StatusSeeOther, want: true},
		{method: http.MethodPatch, status: 399, want: true},
		{method: "PURGE", status: http.StatusOK, want: true},
		{method: http.MethodPost, status: 199, want: false},
		{method: http.MethodPost, status: http.StatusBadRequest, want: false},
		{method: http.MethodPost, status: http.StatusInternalServerError, want: false},
		{method: http.MethodGet, status: http.StatusOK, want: false},
		{method: http.MethodHead, status: http.StatusOK, want: false},
		{method: http.MethodOptions, status: http.StatusOK, want: false},
		{method: http.MethodTrace, status: http.StatusOK, want: false},
	}
	for _, tt := range tests {
		if got := cachepolicy.InvalidatesStoredResponses(tt.method, tt.status); got != tt.want {
			t.Errorf("InvalidatesStoredResponses(%s, %d) = %v, want %v", tt.method, tt.status, got, tt.want)
		}
	}
}
