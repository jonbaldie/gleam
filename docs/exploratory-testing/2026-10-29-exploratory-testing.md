# Exploratory testing: 2026-10-29

- Build: `origin/main` at `39f61b72dc5485f7058154cb7d76127a47ba36c8`, Go 1.26.8 on Linux. Dependencies were downloaded into the worktree using `go build -mod=readonly` and `go.sum`.
- Interface: the Gleam binary, configured through environment variables and exercised over loopback HTTP against a temporary Python origin.
- Configuration: memory cache, default TTL, and an isolated origin with per-path request counts and a mutable value. Each run starts a fresh origin and proxy.
- Evidence: [replay script](evidence/2026-10-29/journeys.py) and [output](evidence/2026-10-29/output.txt). From the repository root, build with `go build -mod=readonly -o .scratch/gleam .` and run `python3 docs/exploratory-testing/evidence/2026-10-29/journeys.py`.

## Results

No bugs were confirmed and no issues were filed. There were no unresolved candidates. The [existing issue list](https://github.com/jonbaldie/gleam/issues?q=is%3Aissue) includes earlier cache and invalidation reports; this pass did not reproduce a defect requiring another report.

### Cache a GET and separate query strings

The goal was to reuse a response for an identical URL while keeping different query strings separate. Two GETs for `/item?color=red` returned the same body, and the origin counted one request. A GET for `/item?color=blue` reached the origin separately. The cache hit included `Age: 0`.

### Update an origin resource

The goal was to see a successful update after a cached GET. The first GET returned `value=initial`. A PUT returned 204 and changed the origin value. The next GET returned `value=updated` with origin count 2, and a second GET returned the same updated body from cache. This checks both the lasting origin change and invalidation of the prior representation.

### Conditional GET and forced refresh

The goal was to avoid sending an unchanged body and allow a client to request a fresh origin response. After a GET populated the cache, a matching `If-None-Match` returned 304 with an empty body. A request with `Cache-Control: no-cache` returned 200 and raised the origin count to 2.

## Limitations and cleanup

The origin was a local test server, not a deployed application. The pass did not exercise Redis, HTTPS, expiration over time, concurrency, or failure recovery. The assertions check observable HTTP responses and origin counts, not internal cache storage. There were no notable usability observations in these requests.

The replay script owns the origin thread and Gleam process and stops both in a `finally` block. The original output records process exit and origin shutdown, but did not record the ports. A [subsequent replay](evidence/2026-10-29/teardown-replay.txt) recorded origin port 42451, proxy port 38715, and Gleam PID 2441032. After shutdown, both connection checks returned `ECONNREFUSED`. The [independent checks](evidence/2026-10-29/teardown-checks.txt) show no listener on either port and no process for that PID. The original proxy PIDs 2353545 and 2365137 were also absent. The original origin threads ran inside their respective replay processes and joined before those processes exited. No containers, browser sessions, Docker images, or volumes were started.
