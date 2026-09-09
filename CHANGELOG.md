# Changelog

All notable changes to this project will be documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/). This project uses [Semantic Versioning](https://semver.org/).

## [v0.1.4] — 2026-09-10

### Fixed
- Evict expired entries from SimpleCache on access so they do not accumulate indefinitely (#39, #49).
- Preserve unannounced HTTP trailers on cached responses (#40, #51).
- Return 304 Not Modified for cached GET when If-None-Match matches the cached ETag (#41, #52).

### Changed
- Pin the module, Docker image, and lint runtime to patched Go 1.25.13 (#42, #53).

## [v0.1.3] — 2026-09-09

### Fixed
- Honor request `Cache-Control: no-store` by skipping cache lookups and storage (#38, #48).
- Do not store responses with `Cache-Control: no-cache` without revalidation (#37, #47).
- Do not store responses with `Cache-Control: private` or `Set-Cookie` in shared cache (#36, #46).
- Bypass cache for `Range` requests and never cache `206 Partial Content` responses (#35, #45).
- Do not cache origin responses with unconfigured `Vary` headers (#34, #44).

### Changed
- Make `CLAUDE.md` a symlink to `AGENTS.md` (#43).
- Pin `mutago` to v2.10.1 in the mutation testing CI gate (#48).

## [v0.1.2] — 2026-08-27

### Fixed
- Preserve HTTP trailers on cached responses instead of promoting trailer values to ordinary headers (#27, #33).
- Allow GET protocol upgrade requests to pass through the reverse proxy path instead of failing with `502 Bad Gateway` (#28, #33).
- Do not store responses marked `Cache-Control: no-store` (#29, #33).
- Send request `Cache-Control: no-cache` traffic to origin instead of serving it from cache (#30, #33).
- Do not reuse responses marked `Vary: *` (#31, #33).

## [v0.1.1] — 2026-08-27

### Fixed
- Bound cache-key header normalization to configured vary headers instead of all incoming request headers (#32).

### Added
- Add `CACHE_VARY_HEADERS` environment variable with default `Authorization,Cookie` (#32).

## [v0.1.0] — 2026-06-25

### Added
- Extract Codec seam for RedisCache serialization (#9).

[v0.1.0]: https://github.com/jonbaldie/gleam/releases/tag/v0.1.0
[v0.1.1]: https://github.com/jonbaldie/gleam/compare/v0.1.0...v0.1.1
[v0.1.2]: https://github.com/jonbaldie/gleam/compare/v0.1.1...v0.1.2
[v0.1.3]: https://github.com/jonbaldie/gleam/compare/v0.1.2...v0.1.3
[v0.1.4]: https://github.com/jonbaldie/gleam/compare/v0.1.3...v0.1.4
