#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-$root}/gleam-consumer.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/proxy/github.com/jonbaldie/gleam/@v" "$tmp/consumer"

ROOT="$root" PROXY_DIR="$tmp/proxy/github.com/jonbaldie/gleam/@v" python3 - <<'PY'
import os
import pathlib
import subprocess
import zipfile

root = pathlib.Path(os.environ['ROOT'])
proxy = pathlib.Path(os.environ['PROXY_DIR'])
version = 'v0.0.0'
prefix = f'github.com/jonbaldie/gleam@{version}/'
files = subprocess.check_output(
    ['git', '-C', str(root), 'ls-files', '-z', '--', '*.go', 'go.mod', 'go.sum', 'LICENSE']
).decode().split('\0')
with zipfile.ZipFile(proxy / f'{version}.zip', 'w', zipfile.ZIP_DEFLATED) as archive:
    for name in filter(None, files):
        archive.write(root / name, prefix + name)
(proxy / f'{version}.mod').write_bytes((root / 'go.mod').read_bytes())
(proxy / f'{version}.info').write_text('{"Version":"v0.0.0","Time":"2026-01-01T00:00:00Z"}')
PY

cd "$tmp/consumer"
export GOWORK=off GOPROXY="file://$tmp/proxy,https://proxy.golang.org" GONOSUMDB=github.com/jonbaldie/gleam
go mod init example.com/gleam-consumer
GOFLAGS=-mod=mod go get github.com/jonbaldie/gleam@v0.0.0
cat > main.go <<'GO'
package main

import (
    "fmt"
    "net/http"
    "net/http/httptest"
    "time"

    "github.com/jonbaldie/gleam/cache"
    "github.com/jonbaldie/gleam/proxy"
)

func main() {
    c := cache.NewMemoryCache()
    defer c.Close()
    calls := 0
    origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        calls++
        w.Header().Set("Cache-Control", "public, max-age=60")
        fmt.Fprint(w, "origin response")
    })
    handler := proxy.NewHandler(origin, c, time.Minute, nil)
    for i := 0; i < 2; i++ {
        response := httptest.NewRecorder()
        handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://example.com/item", nil))
        if response.Code != http.StatusOK || response.Body.String() != "origin response" {
            panic("unexpected response")
        }
    }
    if calls != 1 {
        panic(fmt.Sprintf("origin calls = %d, want 1", calls))
    }
    fmt.Println("external consumer: two responses, one origin call")
}
GO
GOFLAGS=-mod=readonly go run .
