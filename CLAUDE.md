# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

RepoTrends (`gh-trends`) is a GitHub CLI extension written in Go. It analyzes a
repository's pull requests and GitHub Actions runs via the `gh` CLI / GraphQL
and reports PR lead-time, release cadence, review, and CI/CD metrics — as a
terminal table, a CSV export, or a local HTML dashboard.

## Commands

```bash
make build          # go build -ldflags="-s -w" -o gh-trends
make install         # build, stage under local/gh-trends, install as a local `gh` extension
make uninstall       # gh extension remove trends
make test            # go test ./...
make clean           # remove built binaries and *.csv output

go test ./...                        # run all tests
go test ./internal/stats/...         # run a single package's tests
go test ./cmd/... -run TestName      # run a single test by name
go vet ./...
```

There is no lint config beyond `go vet`; CI only runs the release workflow
(`.github/workflows/release.yml`), which fires on `v*` tags and uses
`cli/gh-extension-precompile` to build and publish binaries — it does not run
tests.

After `make install`, invoke the built extension with `gh trends ...` (not
`./gh-trends`), since it is installed as a `gh` extension.

## Architecture

- `main.go` → `cmd.Execute()`. Cobra commands live in `cmd/`:
  - `cmd/root.go` — the default `trends` command (PR analysis), the
    interactive prompt flow (`runInteractiveMode`), yearly/monthly trend and
    comparison logic, flexible date parsing (`ParseFlexibleDate`), and table
    rendering. All persistent flags (`--repo`, `--since`, `--until`,
    `--author`, `--label`, `--lang`, `--year`, `--compare-*`, etc.) are
    declared here as package-level vars and are read directly by `actions.go`
    and `serve.go` — there is no shared config struct, so new flags/state
    follow the same pattern.
  - `cmd/actions.go` — the `actions` subcommand (CI/CD analytics).
  - `cmd/serve.go` — the `serve` subcommand: starts a local HTTP server
    (127.0.0.1-only) with a single-file HTML/JS/CSS dashboard embedded as a Go
    string constant (`dashboardHTML`), polling `/api/status` for progress and
    rendering charts/heatmaps client-side with no external JS dependencies.
    Report data can also be downloaded as Markdown from `/report.md`.
- `internal/github` — shells out to `gh` (via `os/exec`) to fetch PRs
  (`FetchPullRequests`), review/comment timing, and reopen events; defines the
  `PullRequest` struct that flows through the rest of the app.
  `ValidateRepository` confirms the repo exists/is accessible before a full
  fetch.
  - PRs are fetched in parallel across date-range chunks with smart sampling
    for large repos; keep this in mind when changing fetch logic since
    results are merged/deduped afterward.
- `internal/stats` — pure functions over `[]github.PullRequest` that compute
  the `Stats` struct (lead time, review time, release cadence, reopen/hotfix
  rates, merge-type trend, etc.). This is where most metric definitions and
  their average/median calculations live.
- `internal/actions` — fetches and aggregates GitHub Actions workflow run
  data (success rate, duration, failure details) for the `actions` subcommand.
- `internal/csv` — writes processed PRs to CSV.
- `internal/git` — reads the local `.git` remote to auto-detect `owner/repo`.
- `internal/i18n` — minimal English/Japanese string lookup (`i18n.T`,
  `i18n.Sprintf`) driven by the `--lang`/`--jp` flags; all user-facing output
  strings should go through this rather than being hardcoded, to keep both
  languages in sync.
- `internal/animation` — terminal spinner used during long-running fetches;
  `SuppressSpinners` disables it while the `serve` dashboard is running so
  spinner output doesn't corrupt the HTTP server's stdout logging.

"Release" in this codebase has a specific meaning: a merge of a `release/*`
branch into `main`/`master` (Dependabot PRs excluded) — not a deploy or a
tagged version. Several metrics (`ReleaseCount`, `FeatureToReleaseCount`,
`HotfixMerges`, etc.) are built around this release-branch convention.

## Workflow rules

- Always ask for explicit confirmation immediately before running
  `git commit`, even if committing was implied by an earlier instruction in
  the same task. Approval of the overall change does not carry over to the
  commit step — ask again right before committing.
- When reviewing a diff against `main` (via `/codex:review` or otherwise),
  check beyond correctness: whether the implementation is idiomatic/
  appropriate for Go (not just "does it work"), and whether the change
  leaves behind anything unnecessary — dead code, unused fields, leftover
  helpers, stale translation keys, etc. Reusing the dead-code audit done
  earlier in this project (removed from `cmd/root.go`, `internal/github`,
  `internal/stats`, `internal/animation`, `internal/i18n`) as the bar for
  what "unnecessary" means here.
