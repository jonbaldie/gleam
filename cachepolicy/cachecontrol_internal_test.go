package cachepolicy

import (
	"net/http"
	"testing"
	"time"
)

func TestCacheControlAgeTreatsBareFreshnessDirectivesAsInvalid(t *testing.T) {
	for _, name := range []string{"max-age", "s-maxage"} {
		t.Run(name, func(t *testing.T) {
			age, found := cacheControlAge(http.Header{"Cache-Control": []string{name}}, name)
			if !found || age != 0 {
				t.Fatalf("cacheControlAge(bare %s) = (%v, %v), want (0, true)", name, age, found)
			}
		})
	}
}

// A quoted field-name list must not hide a following freshness directive from
// the age parser.
func TestCacheControlAgeSkipsQuotedFieldNameLists(t *testing.T) {
	header := http.Header{"Cache-Control": []string{`private="X-A, X-B", max-age=60`}}
	age, found := cacheControlAge(header, "max-age")
	if !found || age != time.Minute {
		t.Fatalf("expected max-age of one minute, got %v (found=%v)", age, found)
	}
	if _, found := cacheControlAge(header, "s-maxage"); found {
		t.Fatal("expected no s-maxage directive")
	}
}

func TestSplitCacheControlValue(t *testing.T) {
	for _, testCase := range []struct {
		value string
		want  []string
	}{
		{value: "", want: nil},
		{value: " , ,", want: nil},
		{value: "max-age=60, no-transform", want: []string{"max-age=60", "no-transform"}},
		{value: `private="X-A, X-B"`, want: []string{`private="X-A, X-B"`}},
		{value: `private="X-A\", X-B", max-age=60`, want: []string{`private="X-A\", X-B"`, "max-age=60"}},
		{value: `private="unterminated, max-age=60`, want: []string{`private="unterminated, max-age=60`}},
	} {
		got := splitCacheControlValue(testCase.value)
		if len(got) != len(testCase.want) {
			t.Fatalf("%q: expected %q, got %q", testCase.value, testCase.want, got)
		}
		for i := range got {
			if got[i] != testCase.want[i] {
				t.Fatalf("%q: expected %q, got %q", testCase.value, testCase.want, got)
			}
		}
	}
}
