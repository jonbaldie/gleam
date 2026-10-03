# Exploratory testing: 2026-10-03

- **Build:** `origin/main` at `6a84eb5`, built with `go build -o gleam .` (Go 1.26.3, darwin/arm64).
- **Interface:** the `gleam` binary, configured through environment variables and driven with `curl` over HTTP.
- **Origins:** Python mock origin supporting multi-threading, REST methods (GET, PUT, POST, OPTIONS), and RFC 9110/9111 header combinations ([`origin.py`](evidence/2026-10-03/origin.py)), supplemented by Go programmatic reproducer tests ([`reproducers.go`](evidence/2026-10-03/reproducers.go)).
- **Redis:** local `redis-server` on port 16379, with persistence off.
- **Evidence:** [`evidence/2026-10-03/`](evidence/2026-10-03/)
- **Scope:** journeys focusing on REST API semantics, dynamic URL and query routing, Redis serialization with initial Age offsets, and cache directive / validator edge cases following the recent RFC 9110/9111 cachepolicy refactor (#95, #127).

## Confirmed bugs

| Issue | Summary |
|---|---|
| [#128](https://github.com/jonbaldie/gleam/issues/128) | Cache uses configured TTL when Cache-Control max-age or s-maxage has no argument |
| [#129](https://github.com/jonbaldie/gleam/issues/129) | If-Modified-Since evaluates against first Last-Modified header when response has multiple Last-Modified headers |

### #128: Cache uses configured TTL when Cache-Control max-age or s-maxage has no argument

- **Impact:** Origin servers that return bare `max-age` or `s-maxage` (without `=delta-seconds`) have their responses stored and served from cache for the full default TTL (default 5 minutes), rather than treating the directive as invalid/expired. Furthermore, in directive lists such as `s-maxage, max-age=30`, a bare `s-maxage` is ignored and `max-age=30` is evaluated instead of giving precedence to the first directive.
- **Start:** a fresh Gleam instance pointing to an origin returning bare cache directives (`ORIGIN_URL=http://127.0.0.1:19800 PORT=19901 CACHE_TYPE=redis REDIS_URL=redis://localhost:16379/0 ./gleam`).
- **Replay:** run `curl -i http://localhost:19901/bare-max-age`, then check Redis key TTL with `redis-cli -p 16379 ttl "127.0.0.1:19901#/bare-max-age"`, and run `curl -i http://localhost:19901/bare-max-age` again.
- **Expected:** Gleam does not cache the response, or treats it as having 0 freshness lifetime under RFC 9111 §4.2.1.
- **Actual:** Gleam stores the item in Redis with 300 seconds TTL (`defaultTTL`), and subsequent requests hit the cache with `Age: 244` ([`bug-128-bare-max-age.txt`](evidence/2026-10-03/bug-128-bare-max-age.txt)).
- **Note:** In `cachepolicy/cachecontrol.go`, `cacheControlAge` uses `strings.Cut(directive, "=")` and executes `if !hasArgument { continue }`. This skips bare directives without setting `store = false` or returning 0 lifetime, causing `FreshnessLifetime` to fall back to `defaultTTL`.

### #129: If-Modified-Since evaluates against first Last-Modified header when response has multiple Last-Modified headers

- **Impact:** When an origin returns multiple `Last-Modified` headers, clients sending conditional requests with `If-Modified-Since` can receive erroneous `304 Not Modified` responses even when the resource was modified after their conditional timestamp, risking serving stale or inconsistent data.
- **Start:** a fresh Gleam instance caching an origin resource with two `Last-Modified` headers: `01:00:00 GMT` and `03:00:00 GMT`.
- **Replay:** send a conditional request with `If-Modified-Since: Sat, 03 Oct 2026 02:00:00 GMT` (`curl -i -H 'If-Modified-Since: Sat, 03 Oct 2026 02:00:00 GMT' http://localhost:19901/multi-last-modified`).
- **Expected:** Gleam ignores `Last-Modified` per RFC 9110 §8.8.2 ("A recipient that parses a timestamp value in a Last-Modified header field MUST ignore the field if that field has more than one member"), which causes `If-Modified-Since` to be ignored per RFC 9110 §13.1.3, returning `200 OK` with the full representation body.
- **Actual:** Gleam returns `HTTP/1.1 304 Not Modified` because `storedHeader.Get("Last-Modified")` extracts only the first header value (`01:00:00 GMT`), which is before `02:00:00 GMT` ([`bug-129-multi-last-modified.txt`](evidence/2026-10-03/bug-129-multi-last-modified.txt)).

## Journeys

### J1: REST API and Dynamic Proxy Semantics

**Goal:** verify reverse proxy behavior for RESTful APIs, HTTP verbs (`GET`, `PUT`, `POST`, `OPTIONS`), query parameter isolation, URL path encodings, and invalidation.

- **GET and PUT Invalidation:** initial `GET /items/1` resulted in a cache miss, followed by a cache hit with `Age: 2`. An unsafe `PUT /items/1` updated the resource and invalidated the cached entry in Redis. The subsequent `GET /items/1` was forwarded to the origin as a cache miss ([`j1-rest-invalidation.txt`](evidence/2026-10-03/j1-rest-invalidation.txt)).
- **Query Parameter Isolation:** `GET /items/1?fields=name` cached independently from `/items/1`. Requests with differing query strings do not collide in the cache.
- **URL Encoding:** paths with encoded spaces (`/path%20with%20spaces`) and encoded slashes (`/path%2Fwith%2Fslashes`) were correctly proxied, cached, and retrieved under distinct cache keys.
- **Path Normalization:** requests containing double slashes (`/path//double//slashes`) were redirected with `307 Temporary Redirect` by Go's standard `http.ServeMux` path cleaner before reaching proxy logic.
- **Server-Wide OPTIONS:** `OPTIONS *` sent with `--request-target '*'` was intercepted by Go's `http.ServeMux` and answered with `200 OK Content-Length: 0` without reaching the origin handler.

### J2: Shared Redis Caching and Transports

**Goal:** test Redis backend handling of edge HTTP response structures, including empty-body status codes, chunked transfer encoding, and initial Age deductions.

- **204 No Content:** origin responses with status 204 and `Cache-Control: public, max-age=60` were successfully stored as `CacheItem` in Redis and replayed on cache hit with `204 No Content` and `Age: 0` ([`j2-redis-encoding-age.txt`](evidence/2026-10-03/j2-redis-encoding-age.txt)).
- **Chunked Transfer Encoding:** origin responses using `Transfer-Encoding: chunked` without a `Content-Length` header were fully buffered and stored in Redis. Subsequent cache hits were served with a computed `Content-Length: 20` header and stripped `Transfer-Encoding`.
- **Initial Age Header Deduction:** when an upstream origin responded with `Age: 50` and `max-age: 60`, Gleam correctly computed the remaining freshness lifetime as 10 seconds. The Redis key was assigned a TTL of 10 seconds. After 10 seconds elapsed, Redis automatically evicted the expired key (`ttl` returned `-2`), and the next request correctly missed and fetched fresh data from the origin.

### J3: Cache Directives and Conditional Requests

**Goal:** evaluate RFC 9111 freshness directive precedence and RFC 9110 validator evaluation under varying client conditions.

- **`s-maxage` vs `max-age`:** when an origin provided both `s-maxage=30` and `max-age=300`, Gleam prioritized `s-maxage`, storing the item with a 30-second TTL rather than 300 seconds ([`j3-validators-directives.txt`](evidence/2026-10-03/j3-validators-directives.txt)).
- **ETag Matching:**
  - Strong match: `If-None-Match: "abc"` matched stored `ETag: "abc"` -> `304 Not Modified`.
  - Weak comparison: `If-None-Match: W/"abc"` matched stored `ETag: "abc"` -> `304 Not Modified` per RFC 9110 §13.1.2 weak comparison rules.
  - Wildcard match: `If-None-Match: *` returned `304 Not Modified` against representation with ETag.
- **Edge cases:** evaluating bare directives and multiple `Last-Modified` headers uncovered bugs #128 and #129.

## Rejected and unresolved candidates

- **Rejected: `OPTIONS *` answered by proxy instead of origin.** Go's standard library `http.ServeMux` handles `*` request targets internally by replying with `200 OK` before routing to custom handlers. This is Go standard library behavior rather than a Gleam defect.
- **Rejected: Double slashes redirecting with 307.** Go's `http.ServeMux` automatically cleans URL paths with 307 redirects. This conforms to standard Go HTTP server semantics.
- **Rejected: Redirects (301, 308) are not cached.** Although RFC 9111 allows caching 301/308 responses with cache headers, Gleam's storage layer intentionally restricts caching to status codes in the range `[200, 300)` as confirmed by existing test suites in `storage_test.go`.
- **Unresolved: none.**

## Usability observations

- **Observation:** `http.ServeMux` intercepts `OPTIONS *` directly and returns `200 OK` with `Content-Length: 0`, preventing origin server discovery headers (e.g. `Allow`) from reaching clients querying proxy root targets.
- **Observation:** Malformed cache directives (such as missing arguments in `max-age` or `s-maxage`) silently fall back to `defaultTTL` (5 minutes) without logging warnings or marking responses stale.

## Limitations

- Testing was performed on a local macOS host (`darwin/arm64`) using loopback origins and an isolated Redis instance on port 16379.
- Python mock origins ran using `socketserver.ThreadingMixIn` to ensure HTTP/1.1 persistent connections did not block serial or concurrent requests.
- All background test processes (Gleam, Redis server, Python origin) and scratch artifacts were terminated and cleaned up at the conclusion of testing.
