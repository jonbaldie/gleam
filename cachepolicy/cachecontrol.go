package cachepolicy

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// cacheControlAge reports the first occurrence of the target delta-seconds
// directive. An invalid argument, such as a negative or non-integer value,
// reports a zero age so the response is treated as stale (RFC 9111 section
// 4.2.1).
func cacheControlAge(header http.Header, target string) (time.Duration, bool) {
	for _, directive := range cacheControlDirectives(header) {
		name, argument, hasArgument := strings.Cut(directive, "=")
		if !hasArgument || !strings.EqualFold(strings.TrimSpace(name), target) {
			continue
		}
		age, ok := parseCacheControlAge(argument)
		if !ok {
			return 0, true
		}
		return age, true
	}
	return 0, false
}

func parseCacheControlAge(raw string) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		raw = strings.TrimSpace(raw[1 : len(raw)-1])
	}
	seconds, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return saturatingSeconds(seconds), true
}

// cacheControlHasDirective reports whether the Cache-Control header carries
// the named directive, whether bare (`private`) or qualified with an argument
// (`private="X-User-Id"`).
func cacheControlHasDirective(header http.Header, name string) bool {
	for _, directive := range cacheControlDirectives(header) {
		directiveName, _, _ := strings.Cut(directive, "=")
		if strings.EqualFold(strings.TrimSpace(directiveName), name) {
			return true
		}
	}
	return false
}

// cacheControlDirectives splits the Cache-Control header into its directives.
func cacheControlDirectives(header http.Header) []string {
	var directives []string
	for _, value := range header.Values("Cache-Control") {
		directives = append(directives, splitCacheControlValue(value)...)
	}
	return directives
}

// splitCacheControlValue splits one Cache-Control field value on the commas
// that separate directives. Unlike a plain comma split it leaves the commas
// inside a quoted-string argument in place, so the field-name list in
// `private="X-A, X-B"` stays with its directive (RFC 9111 section 5.2).
func splitCacheControlValue(value string) []string {
	var directives []string
	start := 0
	inQuotes := false
	escaped := false
	for i, c := range value {
		switch {
		case escaped:
			escaped = false
		case c == '\\' && inQuotes:
			escaped = true
		case c == '"':
			inQuotes = !inQuotes
		case c == ',' && !inQuotes:
			directives = appendDirective(directives, value[start:i])
			start = i + 1
		}
	}
	return appendDirective(directives, value[start:])
}

func appendDirective(directives []string, raw string) []string {
	if directive := strings.TrimSpace(raw); directive != "" {
		return append(directives, directive)
	}
	return directives
}

func headerContainsToken(values []string, token string) bool {
	for _, value := range commaSeparatedHeaderValues(values) {
		if strings.EqualFold(value, token) {
			return true
		}
	}
	return false
}

func commaSeparatedHeaderValues(values []string) []string {
	var parts []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				parts = append(parts, part)
			}
		}
	}
	return parts
}
