package cachepolicy_test

import (
	"net/http"
	"testing"
	"time"

	"jonbaldie/gleam/cachepolicy"
)

func TestCurrentAge(t *testing.T) {
	storedAt := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	now := storedAt.Add(30 * time.Second)

	tests := []struct {
		name     string
		header   http.Header
		storedAt time.Time
		now      time.Time
		want     uint64
	}{
		{name: "resident time only", header: http.Header{}, storedAt: storedAt, now: now, want: 30},
		{name: "partial seconds truncate", header: http.Header{}, storedAt: storedAt, now: storedAt.Add(1999 * time.Millisecond), want: 1},
		{name: "origin Age adds to resident time", header: http.Header{"Age": {"100"}}, storedAt: storedAt, now: now, want: 130},
		{name: "Age with surrounding whitespace", header: http.Header{"Age": {" 7 "}}, storedAt: storedAt, now: now, want: 37},
		{name: "unparsable Age ignored", header: http.Header{"Age": {"soon"}}, storedAt: storedAt, now: now, want: 30},
		{name: "negative Age ignored", header: http.Header{"Age": {"-5"}}, storedAt: storedAt, now: now, want: 30},
		{
			name:     "apparent age from Date",
			header:   http.Header{"Date": {storedAt.Add(-20 * time.Second).Format(http.TimeFormat)}},
			storedAt: storedAt, now: now, want: 50,
		},
		{
			name:     "greater of Age and apparent age",
			header:   http.Header{"Age": {"5"}, "Date": {storedAt.Add(-20 * time.Second).Format(http.TimeFormat)}},
			storedAt: storedAt, now: now, want: 50,
		},
		{
			name:     "Age greater than apparent age",
			header:   http.Header{"Age": {"40"}, "Date": {storedAt.Add(-20 * time.Second).Format(http.TimeFormat)}},
			storedAt: storedAt, now: now, want: 70,
		},
		{
			name:     "Date after receipt clamps apparent age to zero",
			header:   http.Header{"Date": {storedAt.Add(time.Hour).Format(http.TimeFormat)}},
			storedAt: storedAt, now: now, want: 30,
		},
		{
			name:     "zero storedAt has no resident or apparent age",
			header:   http.Header{"Age": {"9"}, "Date": {storedAt.Add(-20 * time.Second).Format(http.TimeFormat)}},
			storedAt: time.Time{}, now: now, want: 9,
		},
		{name: "clock behind storedAt", header: http.Header{}, storedAt: storedAt, now: storedAt.Add(-time.Minute), want: 0},
		{name: "huge Age saturates", header: http.Header{"Age": {"18446744073709551615"}}, storedAt: storedAt, now: storedAt, want: uint64(time.Duration(1<<63-1) / time.Second)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cachepolicy.CurrentAge(tt.header, tt.storedAt, tt.now); got != tt.want {
				t.Fatalf("CurrentAge() = %d, want %d", got, tt.want)
			}
		})
	}
}
