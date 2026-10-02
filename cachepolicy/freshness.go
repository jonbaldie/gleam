package cachepolicy

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// FreshnessLifetime returns how long a response received at receivedAt may be
// stored and served without revalidation, capped at defaultTTL: its advertised
// freshness lifetime less the age it already carried on arrival (RFC 9111
// sections 4.2 and 4.2.3). A response with no usable freshness directive is
// heuristically fresh for defaultTTL, likewise reduced by its initial age.
// cacheable is false when the response is already stale, since stored
// responses are served without revalidation.
func FreshnessLifetime(header http.Header, receivedAt time.Time, defaultTTL time.Duration) (ttl time.Duration, cacheable bool) {
	freshnessLifetime, hasFreshness := responseFreshnessLifetime(header, receivedAt)
	if !hasFreshness {
		freshnessLifetime = defaultTTL
	}
	remainingFreshness := freshnessLifetime - correctedInitialAge(header, receivedAt)
	if remainingFreshness <= 0 {
		return 0, false
	}
	if remainingFreshness < defaultTTL {
		return remainingFreshness, true
	}
	return defaultTTL, true
}

// CurrentAge is RFC 9111 section 4.2.3's current_age, in whole seconds, for a
// response with the given header stored at storedAt: the age the origin's copy
// already had when this cache received it, plus the time it has been resident
// here since.
func CurrentAge(header http.Header, storedAt time.Time, now time.Time) uint64 {
	correctedInitialAge := correctedInitialAge(header, storedAt)

	var residentTime time.Duration
	if !storedAt.IsZero() {
		residentTime = now.Sub(storedAt)
	}

	return nonNegativeSeconds(correctedInitialAge + residentTime)
}

func responseFreshnessLifetime(header http.Header, receivedAt time.Time) (time.Duration, bool) {
	if freshness, found := cacheControlAge(header, "s-maxage"); found {
		return freshness, true
	}
	if freshness, found := cacheControlAge(header, "max-age"); found {
		return freshness, true
	}

	if len(header.Values("Expires")) > 0 {
		if len(header.Values("Expires")) > 1 {
			// RFC 9111 section 5.3: a response with more than one Expires
			// header field has an invalid date format and MUST be treated as
			// already expired.
			return 0, true
		}

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

func parseHTTPDate(raw string) (time.Time, bool) {
	t, err := http.ParseTime(strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
