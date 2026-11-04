# Fuzzing

Gleam is fuzzed with Go's native fuzzing (`go test -fuzz`). Two harnesses ship
in `fuzz_test.go` and run as part of the normal test suite (their seed corpora
execute under plain `go test`):

- `FuzzCacheKeyForRequest` — exercises cache-key generation with arbitrary
  paths and request headers.
- `FuzzParseOriginURL` — exercises origin URL parsing with arbitrary input.

Run a target for a fixed duration:

```bash
go test -run='^$' -fuzz=FuzzCacheKeyForRequest -fuzztime=30s
go test -run='^$' -fuzz=FuzzParseOriginURL    -fuzztime=30s
```

## Codec fuzzing (`encodeCacheItem` / `decodeCacheItem`)

The Redis cache codec is the most security-relevant surface: `decodeCacheItem`
parses bytes read back from Redis, which may be corrupt or maliciously crafted
(cache poisoning, a shared/multi-tenant Redis, a compromised cache host).

The harness is in `cache/redis_codec_fuzz_test.go` behind the `fuzz` build tag.
It stays out of the default mutation-testing gate because defensive error guards
on in-memory buffers produce equivalent mutants that cannot be killed. Run it
on demand:

```bash
go test -tags=fuzz ./cache -run='^$' -fuzz=FuzzDecodeCacheItem -fuzztime=30s
```

### Bug found: unbounded allocation in `decodeCacheItem` (fixed)

`FuzzDecodeCacheItem` surfaced a denial-of-service: the decoder trusted the
`uint32` length/count prefixes for the content, header count, and per-header
value count, pre-allocating directly from them.

| Payload (base64) | Effect before fix |
| --- | --- |
| `/////w==` (8 bytes) | content prefix `0xFFFFFFFF` → `make([]byte, ~4 GiB)` |
| `AAAAAMgAAAD/////` | header count `0xFFFFFFFF` → giant `http.Header` allocation |
| `AAAAAMgAAAABAAAAAAAAAP////8=` | value count `0xFFFFFFFF` → `make([]string, ~4e9)` (~128 GiB) |

A single small corrupt cache entry could OOM the proxy.

**Fix:** `readCount` now rejects any length/count prefix larger than the bytes
remaining in the buffer before allocating, and `readSized` uses `io.ReadFull`
(instead of `bytes.Reader.Read`, which may silently short-read) so truncated
entries are reported as errors rather than parsed into partial data.
