#!/usr/bin/env bash
# wait-for-module-proxy.sh — block until the release tag is fetchable from the Go module
# proxy and verifiable against the checksum database.
#
# WHY THIS EXISTS. .goreleaser.yaml sets `gomod.proxy: true`, so GoReleaser does NOT build
# the checked-out tree: it re-downloads the module from proxy.golang.org and verifies it
# against sum.golang.org, then builds that. Keeping that property is the point — the
# released binaries are built from exactly what the ecosystem serves — but it makes the
# build depend on infrastructure that has never seen the tag before.
#
# release-please creates the tag and the `binaries` job starts in the same second of the
# same workflow run, so every release races sumdb ingestion. v3.0.0 lost that race:
#
#   go: github.com/rknightion/tailscale2otel/v4@v3.0.0: verifying module:
#       reading https://sum.golang.org/lookup/...@v3.0.0: 500 Internal Server Error
#
# A plain re-run minutes later went green, unchanged. A fixed `sleep` would be the wrong
# fix: ingestion latency is variable, so a constant is either wasted wall-clock on the
# common path or still too short on the bad one. Poll the real precondition instead — the
# same `go mod download` GoReleaser is about to perform — and continue the moment it holds.
#
# Usage:
#   scripts/wait-for-module-proxy.sh [version]
#
# The version defaults to $TAG, then to the tag pointing at HEAD (the release job checks
# out the tag itself). If no version can be determined this WARNS and exits 0: the guard
# must never be the thing that blocks a release.
#
# Env:
#   TAG               version to wait for, e.g. v3.0.0 (overridden by the positional arg)
#   WAIT_TIMEOUT      total seconds to keep polling (default: 300)
#   WAIT_INTERVAL     seconds between attempts (default: 10)
set -euo pipefail

version="${1:-${TAG:-}}"
timeout="${WAIT_TIMEOUT:-300}"
interval="${WAIT_INTERVAL:-10}"

if [ -z "$version" ]; then
  version="$(git describe --tags --exact-match HEAD 2>/dev/null || true)"
fi

if [ -z "$version" ]; then
  echo "wait-for-module-proxy: no version given and HEAD is not tagged — skipping the proxy wait" >&2
  exit 0
fi

module="$(go list -m)"

# Poll from a scratch module so this exercises the proxy + sumdb path without touching the
# real module graph. GOFLAGS is cleared because an inherited -mod=vendor/-mod=readonly makes
# `go mod download <mod>@<ver>` fail for reasons that have nothing to do with availability.
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
(cd "$scratch" && go mod init proxycheck >/dev/null 2>&1)

echo "wait-for-module-proxy: waiting for $module@$version (timeout ${timeout}s, every ${interval}s)"

attempt=0
waited=0
while :; do
  attempt=$((attempt + 1))
  if err="$(cd "$scratch" && GOFLAGS='' go mod download "$module@$version" 2>&1)"; then
    echo "wait-for-module-proxy: $module@$version resolved after ${waited}s (attempt $attempt)"
    exit 0
  fi

  if [ "$waited" -ge "$timeout" ]; then
    echo "wait-for-module-proxy: $module@$version still unresolvable after ${waited}s — giving up" >&2
    echo "$err" >&2
    echo "wait-for-module-proxy: if the tag exists on GitHub, proxy.golang.org/sum.golang.org" >&2
    echo "wait-for-module-proxy: simply have not ingested it — re-run this job, no code change" >&2
    echo "wait-for-module-proxy: is needed. If the error above says 'unknown revision', the tag" >&2
    echo "wait-for-module-proxy: itself is missing and re-running will not help." >&2
    exit 1
  fi

  echo "wait-for-module-proxy: not available yet (attempt $attempt, ${waited}s elapsed), retrying..."
  sleep "$interval"
  waited=$((waited + interval))
done
