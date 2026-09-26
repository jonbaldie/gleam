# Exploratory testing: 2026-09-26

- **Build:** `origin/main` at `4d013af`, built with `go build -o gleam .` (Go 1.26.3, darwin/arm64).
- **Interface:** the `gleam` binary, configured through environment variables and driven with `curl` over HTTP. This is how the README tells operators to use it.
- **Origins:** real public sites, plus small Python origins that count their hits ([`origin.py`](evidence/2026-09-26/origin.py), [`origin3.py`](evidence/2026-09-26/origin3.py), [`origin4.py`](evidence/2026-09-26/origin4.py)).
- **Redis:** local `redis-server` 8 on port 16379, with persistence off.
- **Evidence:** [`evidence/2026-09-26/`](evidence/2026-09-26/)
- **Scope:** journeys not covered by the [2026-09-19 pass](https://github.com/jonbaldie/gleam/pull/92). That pass tested cache directives, conditional requests and invalidation against the memory backend.

## Confirmed bugs

| Issue | Summary |
|---|---|
| [#112](https://github.com/jonbaldie/gleam/issues/112) | Gleam forwards the client's `Host` header to the origin, so virtual-hosted origins reject every request |
| [#113](https://github.com/jonbaldie/gleam/issues/113) | The request log prints the decoded path without escaping, so `%0A` in a URL forges log entries |
| [#114](https://github.com/jonbaldie/gleam/issues/114) | The memory cache never frees expired entries for URLs that are not requested again |

### #112: the client's Host header reaches the origin

- **Impact:** Pointing `ORIGIN_URL` at most HTTPS sites fails. Only origins that ignore `Host` work, such as httpbin or Fly.io's SNI routing.
- **Start:** a fresh process with `ORIGIN_URL=https://example.com PORT=18100 ./gleam`.
- **Replay:** run `curl http://localhost:18100/`, then repeat with `-H 'Host: example.com'`.
- **Expected:** the same response as requesting the origin directly (200).
- **Actual:** 403 through Gleam, and 200 with the Host override. This happened in 3 of 3 fresh processes ([`j1-example-replay.txt`](evidence/2026-09-26/j1-example-replay.txt)). Four other origins fail the same way with 404, 404, 400 and 421 ([`j1-vhost-matrix.txt`](evidence/2026-09-26/j1-vhost-matrix.txt)). The local origin logged `host=localhost:18303` ([`j3-validators-head-prefix.txt`](evidence/2026-09-26/j3-validators-head-prefix.txt)).
- **Note:** the README's Docker example origin, `www.jonbaldie.com`, works only because Fly routes by SNI ([`j1-readme-origin-response.txt`](evidence/2026-09-26/j1-readme-origin-response.txt)).

### #113: log forging through an encoded newline

- **Impact:** any client can add made-up lines, with made-up timestamps, to the operator's request log.
- **Start:** a fresh process against any origin.
- **Replay:** run `curl 'http://localhost:18310/ok%0Aforged-line%0D%0Aanother'`.
- **Expected:** one log line for the request.
- **Actual:** three lines, one of them ending in a raw `\r`. This happened in 2 of 2 fresh processes, plus the first observation, which forged a `DELETE /admin` line ([`j3-log-injection-replay.txt`](evidence/2026-09-26/j3-log-injection-replay.txt), [`j3-log-injection.txt`](evidence/2026-09-26/j3-log-injection.txt)).

### #114: expired memory-cache entries stay in memory

- **Impact:** memory grows with the number of distinct URLs ever cached, not with the number of live entries. Varying a query string is enough to cause this.
- **Start:** a fresh process with `GODEBUG=gctrace=1`. This instrumentation only reports the live heap after Go's forced GC every 2 minutes. The origin returns 4 MiB bodies with `max-age=2`.
- **Replay:** request `/asset?v=1..N` once each, then wait for the forced GC.
- **Expected:** the live heap falls back to about 0 once every entry has expired. The control run does this.
- **Actual:** the live heap stays at 225 MB with 50 entries ([`j3-retention-gctrace.log`](evidence/2026-09-26/j3-retention-gctrace.log)) and at 87 MB with 20 entries in the replay ([`j3-retention-replay-gctrace.log`](evidence/2026-09-26/j3-retention-replay-gctrace.log)). In the control run, each expired URL was read once with `only-if-cached`, and the live heap fell to 0 MB ([`j3-retention-control-gctrace.log`](evidence/2026-09-26/j3-retention-control-gctrace.log)).

## Journeys

### J1: Start Gleam in front of an origin

**Goal:** follow the README and reach the origin through Gleam, with repeat requests served from the cache.

- **Default origin (httpbin):** the first request missed and the second hit with `Age: 0`. httpbin's echo showed the origin received `Host: localhost`, which led to #112.
- **README Docker origin (`www.jonbaldie.com`):** 200 with the same 7323 bytes as a direct request.
- **Other public origins:** failed. See #112.
- **Variation: bad configuration.** Gleam exits with code 1 and a clear message when `TTL_MINUTES` is non-numeric, 0 or negative, when `CACHE_TYPE` is `Redis` (it is case-sensitive), when `ORIGIN_URL` has no scheme or host, when `PORT` is invalid, and when `REDIS_URL` is malformed ([`j1-config-errors.txt`](evidence/2026-09-26/j1-config-errors.txt)).

### J2: Share a cache between instances through Redis

**Goal:** two Gleam instances share one Redis cache. Invalidation reaches both, and the cache survives restarts and outages.

- **Ordinary path:** instance A missed and then hit. Instance B hit A's entry when it was sent the same `Host`. A `POST` through B removed A's entry, and A's next `GET` went to the origin ([`j2-shared.txt`](evidence/2026-09-26/j2-shared.txt)).
- **Restart:** after A was restarted, its first request was a cache hit with `Age: 9`.
- **Redis outage:** `GET` and `POST` still passed through to the origin with 200 and 204 in about 150 ms. Gleam logged each failed write and each failed invalidation. Once Redis was back, caching resumed with a miss followed by a hit ([`j2-restart-outage.txt`](evidence/2026-09-26/j2-restart-outage.txt), [`j2-gleamA-restart.log`](evidence/2026-09-26/j2-gleamA-restart.log)).
- **Not measured:** the cost of the full-keyspace `SCAN` on each unsafe request. `DEBUG POPULATE` is disabled in this Redis build. The problem is already tracked in [#96](https://github.com/jonbaldie/gleam/issues/96).

### J3: Serve a site to browser-like clients

**Goal:** serve compressed pages, validators and large downloads correctly to clients that behave like browsers.

- **Compression:** by default, an origin response with `Vary: Accept-Encoding` is never cached. With `CACHE_VARY_HEADERS=Accept-Encoding,Authorization,Cookie`, gzip clients and plain clients get separate entries, and each receives the correct encoding ([`j3-compression.txt`](evidence/2026-09-26/j3-compression.txt)).
- **Validators:** a conditional request on a cold cache is forwarded, and the origin's 304 reaches the client without being stored. On a warm cache, `If-None-Match` gets a 304 from the cache with an `Age` header.
- **HEAD:** a `HEAD` request is forwarded to the origin, even when the cache already holds the `GET` response.
- **Path prefix:** with `ORIGIN_URL=http://…/api`, a request for `/page` reaches `/api/page` on the origin ([`j3-validators-head-prefix.txt`](evidence/2026-09-26/j3-validators-head-prefix.txt)).
- **Large body:** a 64 MiB response was cached and served back in 22 ms. RSS went from 8 MB to 177 MB ([`j3-big.txt`](evidence/2026-09-26/j3-big.txt)).
- **Variation: abort, then retry.** A download aborted after 1 MB was not cached. The retry fetched the whole body from the origin, and the next request hit ([`j3-abort-retry.txt`](evidence/2026-09-26/j3-abort-retry.txt)).
- **Log check:** this check led to #113. The retention check led to #114.

## Rejected and unresolved candidates

- **Rejected: invalidation lost during a Redis outage.** A `POST` made while Redis is unreachable cannot remove the stored entry. In this run Redis restarted empty, so no stale entry could survive. If Redis stays up but Gleam cannot reach it, a stale entry could survive until its TTL expires. RFC 9111 §4.4 does not cover backend failures, so I have not filed this. It is worth deciding on as policy.
- **Rejected: `Vary: Accept-Encoding` responses are not cached by default.** This is deliberate (#34), and `CACHE_VARY_HEADERS` is documented. The usability cost is noted below.
- **Rejected: `HEAD` does not use the cache.** RFC 9111 allows this but does not require it.
- **Unresolved: none.**

## Usability observations

- **Observation:** most compressing origins send `Vary: Accept-Encoding`, for example nginx with `gzip_vary on`. With the default `CACHE_VARY_HEADERS`, Gleam passes these responses through and caches nothing, without telling the operator.
  - **Suggestion:** mention `Accept-Encoding` in the README, or log a line when a response is skipped because of `Vary`.
- **Observation:** Gleam logs `Gleam started …` before it validates `PORT` and `REDIS_URL`, so a failed start first claims to have started.
- **Observation:** `http.ListenAndServe` runs with no read or header timeouts, and each GET body is buffered whole before it is stored. The second point is tracked in [#106](https://github.com/jonbaldie/gleam/issues/106).
- **Observation:** `CACHE_TYPE` is case-sensitive, and the error message states the allowed values.

## Limitations

- I ran everything on one macOS host, using loopback origins and a few public sites. I did not test with Docker.
- The only instrumentation was `GODEBUG=gctrace=1`, used for #114. It does not change what Gleam does.
- The public-origin results depend on how those sites behave today.
- I cleaned up the scratch state in `/tmp/gleam-et-20260926`: the processes, Redis and the binary. This directory keeps only the evidence.
