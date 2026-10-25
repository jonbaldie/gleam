# Exploratory Testing Report: Gleam Reverse Proxy

- **Date:** 2026-09-19
- **Target:** Gleam HTTP Reverse Proxy (`jonbaldie/gleam`)
- **Baseline Commit:** [`9e2019e`](file:///Users/jonathanbaldie/Code-2/github.com/jonbaldie/gleam/commit/9e2019e0a8f18df1a7f998b50b36af8b4547fc27) (`main`)
- **Evidence Directory:** [`docs/exploratory-testing/evidence/`](file:///Users/jonathanbaldie/Code-2/github.com/jonbaldie/gleam/docs/exploratory-testing/evidence/)
- **Harness:** [`scripts/exploratory_harness.go`](file:///Users/jonathanbaldie/Code-2/github.com/jonbaldie/gleam/scripts/exploratory_harness.go)
- **Status:** Complete (20/20 test scenarios passed, 0 failures)

---

## 1. Scope & Starting Conditions

An exploratory testing pass was conducted to evaluate Gleam across real user journeys through its primary supported interface: a standalone HTTP reverse proxy daemon configured via environment variables and serving real HTTP traffic from origin servers to HTTP clients.

### Configuration & Environment
- **Runtime:** Go 1.25.13 on Darwin (macOS)
- **Binary:** Compiled standalone binary from [`gleam.go`](file:///Users/jonathanbaldie/Code-2/github.com/jonbaldie/gleam/gleam.go)
- **Proxy Configuration:**
  - `ORIGIN_URL`: Dynamic loopback origin test server (`http://127.0.0.1:<port>`)
  - `PORT`: Dynamic loopback proxy port (`http://127.0.0.1:<port>`)
  - `TTL_MINUTES`: 5
  - `CACHE_TYPE`: `memory`
  - `CACHE_VARY_HEADERS`: `Authorization,Cookie` (and custom variations)
- **Evidence Log:** [`docs/exploratory-testing/evidence/2026-09-19-run.log`](file:///Users/jonathanbaldie/Code-2/github.com/jonbaldie/gleam/docs/exploratory-testing/evidence/2026-09-19-run.log)

---

## 2. Journeys Explored

### Journey 1: Caching Lifecycle, Freshness, & Cache-Control Directives
**User Goal:** As an operator deploying Gleam, cache cacheable GET responses from the origin server, reuse stored representations with accurate `Age` headers on cache hits, respect origin and client `Cache-Control` directives, and avoid caching non-cacheable or user-private data in a shared cache.

#### Scenarios & Observable Outcomes:
1. **J1.1 Ordinary Path: Miss -> Store -> Hit with Age Header**
   - *Action:* Send initial `GET /resource` for a response with `Cache-Control: public, max-age=60`. Wait 1s, then send subsequent `GET /resource`.
   - *Observed:* First request reached origin (`originCalls=1`, status 200, no `Age` header). Second request served from memory cache without contacting origin (`originCalls=1`, status 200, `Age: 2`).
   - *Result:* **PASS**
2. **J1.2 Variation: Response `Cache-Control: no-store`**
   - *Action:* Origin responds with `Cache-Control: no-store`. Send two consecutive GET requests.
   - *Observed:* Both requests reached the origin (`originCalls=2`); response was never stored.
   - *Result:* **PASS** (RFC 9111 §5.2.2.5)
3. **J1.3 Variation: Response `Cache-Control: private`**
   - *Action:* Origin responds with `Cache-Control: private, max-age=300`. Send two consecutive GET requests.
   - *Observed:* As a shared cache, Gleam declined to store the private response (`originCalls=2`).
   - *Result:* **PASS** (RFC 9111 §5.2.2.7)
4. **J1.4 Variation: Response with `Set-Cookie`**
   - *Action:* Origin responds with `Set-Cookie: session=xyz; Path=/` and `Cache-Control: public, max-age=300`.
   - *Observed:* Gleam declined to store the response, preventing cookie leakage across clients (`originCalls=2`).
   - *Result:* **PASS** (RFC 9111 §3)
5. **J1.5 Variation: Client request `Cache-Control: no-cache`**
   - *Action:* Prime cache with a storable GET response. Send subsequent request with `Cache-Control: no-cache`.
   - *Observed:* Gleam bypassed the cache and forwarded the request to origin (`originCalls=2`).
   - *Result:* **PASS** (RFC 9111 §5.2.1.4)
6. **J1.6 Variation: Uncacheable Status Codes (404, 500)**
   - *Action:* Send repeated requests for a resource that returns 404 Not Found.
   - *Observed:* 404 responses are forwarded to the client but not stored in cache (`originCalls=2`).
   - *Result:* **PASS**
7. **J1.7 Variation: Response with past `Expires` header**
   - *Action:* Origin returns `Expires: Wed, 10 Sep 2025 00:00:00 GMT` (past timestamp).
   - *Observed:* Response is recognized as immediately stale and not cached (`originCalls=2`).
   - *Result:* **PASS** (RFC 9111 §5.3)
8. **J1.8 Variation: Client `Range` request**
   - *Action:* Prime cache with full GET, then send `Range: bytes=0-6`.
   - *Observed:* Range request bypassed cache, reached origin, and returned 206 Partial Content (`originCalls=2`).
   - *Result:* **PASS** (RFC 9111 §3.3)
9. **J1.9 Variation: Authenticated request without explicit shared cache directive**
   - *Action:* Send requests with `Authorization: Bearer <token>` to a response with `Cache-Control: max-age=300` (without `public`, `must-revalidate`, or `s-maxage`).
   - *Observed:* Responses were not stored/reused across requests (`originCalls=2`).
   - *Result:* **PASS** (RFC 9111 §3.5)

---

### Journey 2: Conditional Requests & HTTP Validation
**User Goal:** As an HTTP client communicating through Gleam with a cached representation, send conditional requests (`If-None-Match`, `If-Modified-Since`) and receive `304 Not Modified` when content has not changed, avoiding unnecessary data transfer.

#### Scenarios & Observable Outcomes:
1. **J2.1 Ordinary Path: `If-None-Match` with matching strong ETag**
   - *Action:* Cache response with `ETag: "v1.0.0"`. Send conditional GET with `If-None-Match: "v1.0.0"`.
   - *Observed:* Proxy returned `304 Not Modified` with empty body, `Age` header, without contacting origin (`originCalls=1`).
   - *Result:* **PASS** (RFC 9110 §13.1.2, RFC 9111 §4.3.2)
2. **J2.2 Variation: Weak ETag (`W/"v1.0.0"`) matching**
   - *Action:* Cache response with `ETag: W/"v1.0.0"`. Request with `If-None-Match: "v1.0.0"`.
   - *Observed:* Proxy performed weak comparison per RFC 9110 §8.8.3.2 and returned `304 Not Modified`.
   - *Result:* **PASS**
3. **J2.3 Variation: `If-Modified-Since` with matching date**
   - *Action:* Cache response with `Last-Modified: Fri, 18 Sep 2026 12:00:00 GMT`. Send conditional GET with identical `If-Modified-Since`.
   - *Observed:* Proxy returned `304 Not Modified` locally (`originCalls=1`).
   - *Result:* **PASS** (RFC 9110 §13.1.3, RFC 9111 §4.3.2)
4. **J2.4 Variation: `If-None-Match` precedence over `If-Modified-Since`**
   - *Action:* Send request containing both a mismatched `If-None-Match: "tag-old"` and a matching `If-Modified-Since`.
   - *Observed:* Per RFC 9110 §13.2.2, `If-None-Match` takes precedence; proxy served the full cached representation with `200 OK`.
   - *Result:* **PASS**
5. **J2.5 Variation: Multiple comma-separated ETags in `If-None-Match`**
   - *Action:* Send `If-None-Match: "other-1", "tag-match", "other-2"` where `"tag-match"` matches stored ETag.
   - *Observed:* Proxy matched `"tag-match"` in the list and returned `304 Not Modified`.
   - *Result:* **PASS**

---

### Journey 3: State Mutations, Invalidation, & Vary Isolation
**User Goal:** Ensure that unsafe HTTP requests (state mutations) invalidate cached GET responses so subsequent requests observe fresh data, while ensuring that multi-tenant/multi-user headers (`Cookie`, `Authorization`) keep cache entries isolated.

#### Scenarios & Observable Outcomes:
1. **J3.1 Ordinary Path: POST invalidates cached GET response**
   - *Action:* Cache `GET /items` ("initial-data"). Send `POST /items` returning `204 No Content`. Send subsequent `GET /items`.
   - *Observed:* After POST, subsequent GET reached origin (`originCalls=2`) and returned `"updated-data"`.
   - *Result:* **PASS** (RFC 9111 §4.4)
2. **J3.2 Variation: Failed unsafe request (500 Error) does NOT invalidate cache**
   - *Action:* Cache `GET /items-fail`. Send `POST /items-fail` returning `500 Internal Server Error`. Send subsequent `GET /items-fail`.
   - *Observed:* Cached entry remained valid; subsequent GET was served from cache without contacting origin (`originCalls=1`).
   - *Result:* **PASS** (RFC 9111 §4.4 explicitly restricts invalidation to non-error 2xx/3xx responses)
3. **J3.3 Variation: Safe method (HEAD) does not invalidate cache**
   - *Action:* Cache `GET /head-test`. Send `HEAD /head-test`. Send subsequent `GET /head-test`.
   - *Observed:* Subsequent GET served from cache (`originCalls=1`).
   - *Result:* **PASS** (RFC 9110 §9.2.1, RFC 9111 §4.4)
4. **J3.4 Variation: `Vary: Cookie` isolates user sessions**
   - *Action:* Request `/profile` with `Cookie: user=Alice`, then `Cookie: user=Bob`, then `Cookie: user=Alice` again.
   - *Observed:* Alice and Bob received separate responses; Alice's second request was a cache hit (`originCalls=2`).
   - *Result:* **PASS** (RFC 9111 §4.1)
5. **J3.5 Variation: Query string parameter isolation**
   - *Action:* Send `GET /search?q=foo`, then `GET /search?q=bar`, then `GET /search?q=foo`.
   - *Observed:* Different query strings produced separate cache entries; third request was a cache hit (`originCalls=2`).
   - *Result:* **PASS**
6. **J3.6 Variation: PUT, DELETE, and PATCH invalidate cached GET**
   - *Action:* Test `PUT`, `DELETE`, and `PATCH` methods against previously cached resources.
   - *Observed:* All three unsafe methods successfully invalidated the target URI cache entries; subsequent GET requests reached origin.
   - *Result:* **PASS** (RFC 9111 §4.4)

---

## 3. Candidate Investigation & Classifications

During the pass, four candidate behaviors and edge cases were analyzed against primary RFC specifications:

### Candidate A: Client Request `Cache-Control: max-age`
- **Observation:** When a client sends `Cache-Control: max-age=0` (such as on a browser page refresh), Gleam serves an existing cached response from memory instead of forwarding the request to the origin server.
- **Specification Check:** RFC 9111 §5.2.1 ("Request Directives"):
  > *"This section defines cache request directives. They are advisory; caches MAY implement them, but are not required to."*
  
  And RFC 9111 §4.2:
  > *"Clients can send the max-age or min-fresh request directives (Section 5.2.1) to suggest limits on the freshness calculations for the corresponding response. However, caches are not required to honor them."*
- **Classification:** **Rejected with evidence.** While client request `max-age` is advisory and optional for shared caches under RFC 9111, client `no-cache` (which forces revalidation) is implemented and verified in J1.5.

### Candidate B: `Location` and `Content-Location` Header Invalidation on Unsafe Requests
- **Observation:** When an unsafe request (such as `POST /items`) succeeds and returns a `Location: /items/123` header, Gleam invalidates the request target URI (`/items`), but does not invalidate `/items/123`.
- **Specification Check:** In RFC 7234, invalidating `Location` and `Content-Location` URIs was mandatory. However, in RFC 9111 §4.4:
  > *"A cache MAY invalidate other URIs when it receives a non-error status code in response to an unsafe request method... In particular, the URI(s) in the Location and Content-Location response header fields (if present) are candidates for invalidation..."*
- **Classification:** **Rejected with evidence.** RFC 9111 updated this from mandatory to discretionary (`MAY`). Mandatory invalidation applies only to the target URI, which Gleam implements.

### Candidate C: Evaluation of `If-Match` on GET Requests
- **Observation:** An `If-Match` header on a GET request does not prevent Gleam from serving a cached response.
- **Specification Check:** RFC 9111 §4.3.2 ("Handling a Received Validation Request"):
  > *"In summary, the If-Match and If-Unmodified-Since conditional header fields are not applicable to a cache..."*
- **Classification:** **Rejected with evidence.** Caches must only evaluate `If-None-Match` and `If-Modified-Since`.

### Candidate D: Cached Non-200 Representations with `If-Modified-Since`
- **Observation:** When a non-200 response (e.g. 203 or 204) is cached with a `Last-Modified` header, date-based validation previously returned `304 Not Modified` rather than ignoring the condition per RFC 9110 §13.1.3.
- **Status:** Already documented and filed in GitHub Issues as **[Issue #87](https://github.com/jonbaldie/gleam/issues/87)** ("Cached non-200 GET responses evaluate If-Modified-Since instead of ignoring it"). An active fix is in review in **[PR #91](https://github.com/jonbaldie/gleam/pull/91)**.

---

## 4. Usability & Operational Observations

1. **Configurability:** Environment variable configuration (`ORIGIN_URL`, `PORT`, `TTL_MINUTES`, `CACHE_TYPE`, `CACHE_VARY_HEADERS`) is straightforward and works cleanly out of the box with sensible defaults.
2. **Deterministic Age Tracking:** The `Age` header calculation correctly reflects residency time on cache hits without replaying or resetting unexpectedly.
3. **Safe Fallbacks for Shared Caching:** Gleam's policy of declining to cache `Set-Cookie`, `private`, and unhandled `Vary` values ensures strong security guarantees against multi-user data leakage.

---

## 5. Artifacts & Evidence Files

- Test Harness: [`scripts/exploratory_harness.go`](file:///Users/jonathanbaldie/Code-2/github.com/jonbaldie/gleam/scripts/exploratory_harness.go)
- Captured Log: [`docs/exploratory-testing/evidence/2026-09-19-run.log`](file:///Users/jonathanbaldie/Code-2/github.com/jonbaldie/gleam/docs/exploratory-testing/evidence/2026-09-19-run.log)
- Primary RFC References: RFC 9110 (HTTP Semantics), RFC 9111 (HTTP Caching)
