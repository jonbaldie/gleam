---
name: verify-cache-invalidation
description: "Check the serveGet store-after-InvalidatePrefix race on MemoryCache. Use when a change touches serveGet, storeResponse, reusableCachedItem, serveNonGet, MemoryCache Get, Set, or InvalidatePrefix, or when a cache hit follows a successful unsafe request."
---

# Cache invalidation

**Property.** After `serveNonGet` has run `InvalidatePrefix` for a URI, `MemoryCache` must not hold a representation that this GET's `upstream.ServeHTTP` observed before that unsafe request committed.

**Harm.** The next `reusableCachedItem` hit serves that pre-update body, so the client sees content the successful write already replaced.

At the sync commit the code breaks this property. The known finding is that break.

The check is TLC on `verification/cache-invalidation/CacheInvalidation.tla`. It explores every interleaving of one GET and one unsafe request. The script pins the jar. Read every TLC result with the pstack `/statechart` skill before you call the run a bug, a modeling error, or a pass. `/statechart` is how you read the path; the script is the verdict.

## Coverage

Sync commit: `3eadd95acde24ed04806ca5687590bdf630ae1e2`

Move the sync commit forward only after confirming the verification describes the code at the new commit.

| Action | Code |
| --- | --- |
| `ReusableCachedItem` | `serveGet` calls `reusableCachedItem`, which calls `MemoryCache.Get` |
| `ServeGetUpstream` | `serveGet` calls `upstream.ServeHTTP` and that call reads the origin |
| `StoreResponse` | `storeResponse` calls `MemoryCache.Set` with the body that call read |
| `ServeNonGetUpstream` | `serveNonGet` calls `upstream.ServeHTTP` and the origin commits |
| `InvalidatePrefix` | `serveNonGet` calls `MemoryCache.InvalidatePrefix` |

Files: `proxy/proxy.go`, `cache/memory.go`.

A diff also touches this check when it edits a guard the model assumes, or adds another call to `Cache.Get`, `Cache.Set`, or `Cache.InvalidatePrefix` on a request path. The assumed guards are `ShouldBypass`, `OnlyIfCached`, `IsAuthenticated`, `CanReuse`, `copyComplete`, `storable`, `CanStoreResponse`, `FreshnessLifetime`, and `InvalidatesStoredResponses`.

## Bounds

One URI, one cache key, one in-flight GET, one unsafe request. The origin applies one request at a time. The GET stores: `copyComplete`, `storable`, and `FreshnessLifetime` all say store. The unsafe response is a non-error, so `InvalidatesStoredResponses` is true. `Get`, `Set`, and `InvalidatePrefix` are each one `MemoryCache` lock. No expiry during the race. `storedVersion` stands for the origin representation in `CacheItem.Content`: `0` was committed before the unsafe request, `1` is that request's commit, `-1` is a `Get` miss.

## Run

From the repository root:

```sh
verification/cache-invalidation/check.sh
```

Done when the process has exited. A zero exit means `NoStaleStoreAfterInvalidation` held for this model. This jar exits 12 when that invariant is violated; the trace names the actions. Any other non-zero exit means the jar, Java, or the script failed. Fix that and run again. The actions stay as they are.

`verification/cache-invalidation/repro` replays the known finding on `proxy.NewHandler`. It is how you confirm that finding on the handler. It is not a second check.

```sh
go run ./verification/cache-invalidation/repro
```

## Steps

1. **Touch.** List every hunk that changes a covered function, changes an assumed guard, or adds a `Get`, `Set`, or `InvalidatePrefix` on a request path. An empty list means stop. Done when that list is written, or you have stopped.

2. **Faithful.** For each hunk, edit the action whose comment names that function so the action is still that function's one indivisible step. Write the edit from the function body. A guard hunk updates the matching assumption in Bounds, or the action the guard controls. Done when each hunk has an edit or a written reason the model already describes it.

3. **Run.** Run the command in Run. Done when it has exited.

4. **Read.** Read the result with `/statechart`. Done when you have the shortest violating path in plain words, or the chart of a run whose invariant holds.

5. **Verdict.** Match the path to one label below. Done when the label is chosen.

6. **Sync.** Set the sync commit to the new SHA only after you have re-read the covered functions at that SHA and every action still matches them. A failing property still gets that SHA when the model describes the code you read. Leave the SHA where it is while any action still describes an older body.

## Verdicts

**Known finding.** The script's trace is this action order, and the state that breaks the invariant has `storedVersion = 0` and `committedVersion = 1`. `/statechart` is this same order when its path is a miss, a read of the old representation, the unsafe commit, a drop, then a store of that old representation:

1. `ReusableCachedItem` misses.
2. `ServeGetUpstream` observes origin version `0`.
3. `ServeNonGetUpstream` commits origin version `1`.
4. `InvalidatePrefix` leaves the key absent.
5. `StoreResponse` stores version `0`.

In the handler's words: `serveGet` misses, its `upstream.ServeHTTP` reads the pre-update body, `serveNonGet`'s `upstream.ServeHTTP` commits and returns a non-error, `InvalidatePrefix` deletes nothing that is still stored, then `storeResponse` writes that pre-update body. The next GET is a hit. The repro prints `later GET body="pre-update" originGETs=1` for this finding. Keep the invariant. Report the path. Change the invariant only when the promise itself changed on purpose, and cite the comment, test, or commit that says so.

**New bug.** The actions match the functions you just read, and the path is not the known finding. Report that path in the handler's words. Keep the invariant.

**Modeling error.** The path names a call the function no longer makes, a step the code can take is missing and adding it removes the violation, or a phase changes in a way `serveGet` and `serveNonGet` cannot. Repair the model from the functions, then run again.

**Pass.** The invariant holds for this model and these bounds. It says the same about the code only when step 2 was just done against the current functions. It does not cover the rest of the proxy.

## Left out

`RedisCache.InvalidatePrefix` scans, then deletes. A `Set` can land in that gap; this model does not include it. Also left out: more than one in-flight GET, vary variants, expiry and the idle sweeper, conditional validators, authenticated reuse, `ShouldBypass` requests, and an origin that commits the GET and the unsafe request as one concurrent step.
