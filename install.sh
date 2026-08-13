#!/usr/bin/env bash

set -euo pipefail

if ! command -v gh >/dev/null 2>&1; then
    echo "Error: GitHub CLI (gh) is required: https://cli.github.com/" >&2
    exit 1
fi

if ! command -v go >/dev/null 2>&1; then
    echo "Error: Go is required for local source installation: https://go.dev/dl/" >&2
    exit 1
fi

go build -ldflags="-s -w" -o gh-trends .
mkdir -p local/gh-trends
cp gh-trends local/gh-trends/gh-trends
git -C local/gh-trends init -q
gh extension remove reporhythm >/dev/null 2>&1 || true
gh extension remove trends >/dev/null 2>&1 || true
(
    cd local/gh-trends
    gh extension install .
)

echo "RepoTrends installed. Run: gh trends --help"
