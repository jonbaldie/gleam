------------------------------ MODULE CacheInvalidation ------------------------------
\* One URI, one MemoryCache key, one GET, and one unsafe request.
\*
\* Each action is one indivisible step of the running code. MemoryCache holds
\* mu for the whole Get, Set, or InvalidatePrefix (cache/memory.go), so each
\* of those calls is one step. The gaps between them are not locked.
\*
\* ReusableCachedItem
\*   proxy/proxy.go serveGet calls reusableCachedItem, which calls Cache.Get.
\*   A hit answers the request and does not store. A miss falls through.
\* ServeGetUpstream
\*   proxy/proxy.go serveGet calls upstream.ServeHTTP. The handler reads the
\*   origin representation it will return. An unsafe request can run while
\*   this call is still in progress.
\* StoreResponse
\*   proxy/proxy.go storeResponse calls Cache.Set with that representation.
\*   Nothing in storeResponse checks that InvalidatePrefix ran after the read.
\* ServeNonGetUpstream
\*   proxy/proxy.go serveNonGet calls upstream.ServeHTTP for an unsafe method.
\*   The origin commits the new representation before the call returns.
\*   The model takes the non-error branch, so the next step is InvalidatePrefix
\*   (cachepolicy.InvalidatesStoredResponses).
\* InvalidatePrefix
\*   proxy/proxy.go serveNonGet calls Cache.InvalidatePrefix, and
\*   MemoryCache.InvalidatePrefix deletes the key under one lock.
\* Done
\*   Both calls have returned. It changes nothing.

EXTENDS Integers

VARIABLES
  originVersion,    \* representation the origin has committed
  storedVersion,    \* CacheItem from the last Set, or -1 when Get misses
  getPhase,         \* lookup | upstream | store | done
  observedVersion,  \* representation ServeGetUpstream read
  postPhase,        \* upstream | invalidate | done
  committedVersion  \* originVersion the unsafe request committed

vars == <<originVersion, storedVersion, getPhase, observedVersion, postPhase, committedVersion>>

Init ==
  /\ originVersion = 0
  /\ storedVersion = -1
  /\ getPhase = "lookup"
  /\ observedVersion = -1
  /\ postPhase = "upstream"
  /\ committedVersion = -1

ReusableCachedItem ==
  /\ getPhase = "lookup"
  /\ IF storedVersion >= 0
       THEN /\ getPhase' = "done"
            /\ observedVersion' = storedVersion
       ELSE /\ getPhase' = "upstream"
            /\ UNCHANGED observedVersion
  /\ UNCHANGED <<originVersion, storedVersion, postPhase, committedVersion>>

ServeGetUpstream ==
  /\ getPhase = "upstream"
  /\ observedVersion' = originVersion
  /\ getPhase' = "store"
  /\ UNCHANGED <<originVersion, storedVersion, postPhase, committedVersion>>

StoreResponse ==
  /\ getPhase = "store"
  /\ storedVersion' = observedVersion
  /\ getPhase' = "done"
  /\ UNCHANGED <<originVersion, observedVersion, postPhase, committedVersion>>

ServeNonGetUpstream ==
  /\ postPhase = "upstream"
  /\ originVersion' = originVersion + 1
  /\ committedVersion' = originVersion + 1
  /\ postPhase' = "invalidate"
  /\ UNCHANGED <<storedVersion, getPhase, observedVersion>>

InvalidatePrefix ==
  /\ postPhase = "invalidate"
  /\ storedVersion' = -1
  /\ postPhase' = "done"
  /\ UNCHANGED <<originVersion, getPhase, observedVersion, committedVersion>>

Done ==
  /\ getPhase = "done"
  /\ postPhase = "done"
  /\ UNCHANGED vars

Next ==
  \/ ReusableCachedItem
  \/ ServeGetUpstream
  \/ StoreResponse
  \/ ServeNonGetUpstream
  \/ InvalidatePrefix
  \/ Done

\* After InvalidatePrefix, a stored CacheItem must be a representation the
\* origin committed at or after the unsafe request. storedVersion = -1 is a
\* Get miss. A later reusableCachedItem returns whatever is stored here.
NoStaleStoreAfterInvalidation ==
  \/ postPhase /= "done"
  \/ storedVersion = -1
  \/ storedVersion >= committedVersion

Spec == Init /\ [][Next]_vars

================================================================================
