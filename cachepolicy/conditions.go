package cachepolicy

import (
	"net/http"
	"strings"
)

// NotModified reports whether a request's conditional validators are
// satisfied by a stored response with the given status and header, so the
// cache may answer 304 Not Modified instead of the stored representation.
//
// RFC 9110 section 13.2.2: If-None-Match, when present, takes precedence and
// If-Modified-Since is ignored. A 304 stands in for a 200 (section 15.4.5), so
// neither validator yields 304 for a stored non-200.
func NotModified(reqHeader http.Header, storedStatus int, storedHeader http.Header) bool {
	if storedStatus != http.StatusOK {
		return false
	}
	if ifNoneMatch := reqHeader.Values("If-None-Match"); len(ifNoneMatch) > 0 {
		return ifNoneMatchMatches(ifNoneMatch, storedHeader.Get("ETag"))
	}
	return ifModifiedSinceSatisfied(reqHeader.Values("If-Modified-Since"), storedHeader.Values("Last-Modified"))
}

type entityTag struct {
	opaque string
}

func ifNoneMatchMatches(ifNoneMatch []string, etag string) bool {
	var tags []entityTag
	for _, value := range ifNoneMatch {
		star, valueTags := parseIfNoneMatchValue(value)
		if star {
			return true
		}
		tags = append(tags, valueTags...)
	}

	cached, ok := parseEntityTag(etag)
	if !ok {
		return false
	}
	for _, tag := range tags {
		if tag.opaque == cached.opaque {
			return true
		}
	}
	return false
}

// ifModifiedSinceSatisfied reports whether a stored representation with the
// given Last-Modified date has not been modified since the request's
// If-Modified-Since date, i.e. whether it can be answered with 304 locally.
// A condition that is absent, or whose date cannot be parsed as an HTTP date,
// is ignored rather than treated as a match. A field with more than one
// member is ignored entirely (RFC 9110 section 13.1.3), and so is a
// Last-Modified field with more than one member (section 8.8.2), which leaves
// no modification date to evaluate the condition against.
func ifModifiedSinceSatisfied(ifModifiedSince, lastModified []string) bool {
	if len(ifModifiedSince) != 1 || len(lastModified) != 1 {
		return false
	}
	stored, ok := parseHTTPDate(lastModified[0])
	if !ok {
		return false
	}
	condition, ok := parseHTTPDate(ifModifiedSince[0])
	if !ok {
		return false
	}
	return !stored.After(condition)
}

func parseEntityTag(raw string) (entityTag, bool) {
	raw = strings.TrimSpace(raw)
	tag, n, ok := scanEntityTag(raw)
	if !ok || strings.TrimSpace(raw[n:]) != "" {
		return entityTag{}, false
	}
	return tag, true
}

func parseIfNoneMatchValue(value string) (star bool, tags []entityTag) {
	rest := strings.TrimSpace(value)
	for rest != "" {
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			break
		}
		if isStarToken(rest) {
			return true, tags
		}
		tag, n, ok := scanEntityTag(rest)
		if !ok {
			rest = skipToNextListItem(rest)
			continue
		}
		tags = append(tags, tag)
		rest = consumeListSeparator(rest[n:])
	}
	return false, tags
}

func isStarToken(s string) bool {
	if s[0] != '*' {
		return false
	}
	after := strings.TrimLeft(s[1:], " \t")
	return after == "" || after[0] == ','
}

func skipToNextListItem(s string) string {
	comma := strings.IndexByte(s, ',')
	if comma < 0 {
		return ""
	}
	return s[comma+1:]
}

func consumeListSeparator(s string) string {
	s = strings.TrimLeft(s, " \t")
	if s == "" || s[0] != ',' {
		return ""
	}
	return s[1:]
}

func scanEntityTag(s string) (entityTag, int, bool) {
	i := 0
	end := len(s)
	if end >= 2 && s[0] == 'W' && s[1] == '/' {
		i = 2
	}
	if i >= end || s[i] != '"' {
		return entityTag{}, 0, false
	}
	j := i + 1
	for j < end && s[j] != '"' {
		j++
	}
	if j >= end {
		return entityTag{}, 0, false
	}
	return entityTag{opaque: s[i : j+1]}, j + 1, true
}
