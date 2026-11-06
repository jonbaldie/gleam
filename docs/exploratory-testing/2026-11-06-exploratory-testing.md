# Exploratory testing: 2026-11-06

- Build: `origin/main` at `088f451`, Go 1.26.8 on Linux. `go build -mod=readonly` and `go test -mod=readonly ./...` used `go.mod` and `go.sum` with caches inside the worktree.
- Interface: the Gleam binary over loopback HTTP, with a Python origin. The origin reports its request count and echoes the cookie. Each run starts a fresh memory cache and origin. The default TTL and vary headers apply.
- Evidence: [build details](evidence/2026-11-06/build.txt), [Go tests](evidence/2026-11-06/go-test.txt), [replay script](evidence/2026-11-06/journeys.py), [first run](evidence/2026-11-06/output.txt), and [replay](evidence/2026-11-06/replay.txt). From the repository root, run `go build -mod=readonly -o .scratch/gleam .` and `python3 docs/exploratory-testing/evidence/2026-11-06/journeys.py`. The script has no Python dependencies outside the standard library.

## Results

No bugs were confirmed, no candidates remain unresolved, and no issues were filed. Both runs produced the same HTTP results and origin counts. The first run preceded the addition of a SIGTERM cleanup handler to the script. The replay used the saved script.

### Authenticated requests

The goal was to avoid reusing authenticated responses without permission, while allowing an explicitly public response to be cached, as described in the [README](../../README.md#usage). Two requests with the same bearer token to an origin response without a cache permission returned counts 1 and 2. With `Cache-Control: public, max-age=60`, two requests returned count 1, and the second response had an `Age` header. The origin counts confirmed both outcomes. This checks the scenario in closed [issue #67](https://github.com/jonbaldie/gleam/issues/67).

### Cookie isolation

The goal was to retain separate responses for different cookies using the documented default `CACHE_VARY_HEADERS`. Two requests with `session=red` returned the red body from one origin request. Two with `session=blue` returned the blue body from a second origin request. Both repeat responses carried `Age: 0`. This checks default key isolation, not application session security. The README warns against caching dynamic session content.

### Cache-only requests

The goal was to avoid contacting the origin for `Cache-Control: only-if-cached`, whether the resource was missing or cached. The first request returned 504 and the origin saw no request. An ordinary GET then returned count 1. A second cache-only request returned the same body and the origin count remained 1. This checks the hit and miss cases in closed [issue #101](https://github.com/jonbaldie/gleam/issues/101).

## Limits and cleanup

The local origin is not a production service. Redis, TLS, expiry, concurrent requests, failures, and custom vary configuration were not exercised. The open [issue #142](https://github.com/jonbaldie/gleam/issues/142) concerns Redis conditional-hit performance and was not investigated. No usability observations arose from these requests.

The script owns the origin thread and proxy process, and closes them in `finally` on completion, SIGINT, or SIGTERM. It reports the process ID and ports, joins the origin thread, waits for the proxy, and checks that connections to both ports are refused. Both runs recorded successful teardown. No browsers, containers, Docker images, or volumes were started.
