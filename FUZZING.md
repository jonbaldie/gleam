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

This harness is intentionally **not** committed as a normal test file. The
codec's defensive error guards — `binary.Write` to an in-memory `bytes.Buffer`,
and an `io.ReadFull` that only runs after the length has already been validated
— cannot fail in practice. Covering them under the mutation-testing gate would
only ever produce *equivalent mutants* that can never be killed, which would
distort the covered-MSI metric. Keep the harness out of the default suite and
run it on demand by dropping the file below into the package and invoking it:

```go
package main

import (
	"net/http"
	"testing"
	"time"
)

func FuzzDecodeCacheItem(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("not-base64!!!"))
	// Length prefixes of 0xFFFFFFFF that drove multi-gigabyte allocations
	// before readCount bounded them against the remaining input.
	f.Add([]byte("/////w=="))
	f.Add([]byte("AAAAAMgAAAD/////"))
	f.Add([]byte("AAAAAMgAAAABAAAAAAAAAP////8="))
	hdr := http.Header{"Content-Type": {"text/plain"}}
	codec := &BinaryCodec{}
	if enc, err := codec.Encode(CacheItem{content: []byte("hi"), header: hdr, status: 200, expiration: time.Unix(0, 0)}); err == nil {
		f.Add(enc)
	}
	f.Fuzz(func(t *testing.T, data []byte) { 
		codec := &BinaryCodec{}
		_, _ = codec.Decode(data) 
	})
}

func FuzzEncodeDecodeRoundTrip(f *testing.F) {
	f.Add("body", "Content-Type", "text/plain", 200)
	f.Fuzz(func(t *testing.T, body, hkey, hval string, status int) {
		if status < 0 {
			status = -status
		}
		status %= 1000
		hdr := http.Header{}
		if hkey != "" {
			hdr.Add(hkey, hval)
		}
		in := CacheItem{content: []byte(body), header: hdr, status: status, expiration: time.Now().Truncate(0)}
		codec := &BinaryCodec{}
		enc, err := codec.Encode(in)
		if err != nil {
			return
		}
		out, err := codec.Decode(enc)
		if err != nil {
			t.Fatalf("decode of freshly encoded item failed: %v", err)
		}
		if string(out.content) != body || out.status != status {
			t.Fatalf("round-trip mismatch")
		}
	})
}
```

```bash
go test -run='^$' -fuzz=FuzzDecodeCacheItem -fuzztime=30s
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
