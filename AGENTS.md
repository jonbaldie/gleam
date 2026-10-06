# Project Instructions for AI Agents

This file provides instructions and context for AI coding agents working on this project.

## Agent skills

### Issue tracker

Issues live in GitHub Issues for `jonbaldie/gleam` (via `gh`). See `docs/agents/issue-tracker.md`.

### Triage labels

Default five-role vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context layout — root `CONTEXT.md` plus `docs/adr/`. See `docs/agents/domain.md`.

## Git Worktrees

- Always work on git worktrees located in the `.worktrees/` directory.

## Build & Test

Go 1.25.13 (`go.mod`). Fetch modules with `go mod download`. Install the pinned linter with `./scripts/install-golangci-lint.sh` (binary lands in gitignored `./bin`).

```bash
go test ./...
GOTOOLCHAIN=go1.25.13 ./bin/golangci-lint run --build-tags=gms_pure_go ./...
go run gleam.go
```

`go run gleam.go` listens on `$PORT` (default 8080) and proxies `ORIGIN_URL` (default `https://httpbin.org`). The cache is in-memory unless `CACHE_TYPE=redis`, which uses `REDIS_URL` (default `redis://localhost:6379/0`). Further settings are in `README.md`.

For mutation tests, set `GOMAXPROCS=1` and pass `--workers=1` to `mutago` to keep the host responsive. This applies to local runs only; see `.github/workflows/mutation.yml` for the pinned Mutago version and its coverage, MSI, logger, timeout, and package arguments.

## Architecture Overview

`gleam.go` loads env config and either an in-memory or Redis cache, then serves `proxy`. `cachepolicy` decides storage and freshness. `codec` serializes entries stored in Redis.

## Conventions & Patterns

Shared-cache behavior follows RFC 9111. The lint gate uses `--build-tags=gms_pure_go` and `GOTOOLCHAIN=go1.25.13`, the same invocation as `.github/workflows/build-and-test.yml`.
