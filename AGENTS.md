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

_Add your build and test commands here_

For mutation tests, set `GOMAXPROCS=1` and pass `--workers=1` to `mutago` to keep the host responsive. This applies to local runs only; see `.github/workflows/mutation.yml` for the pinned Mutago version and its coverage, MSI, logger, timeout, and package arguments.

```bash
# Example:
# npm install
# npm test
```

## Architecture Overview

_Add a brief overview of your project architecture_

## Conventions & Patterns

_Add your project-specific conventions here_
