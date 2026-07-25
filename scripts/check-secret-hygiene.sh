#!/usr/bin/env bash
#
# check-secret-hygiene.sh — repository hygiene gate for the two secret-boundary
# contracts this repo relies on. Both are easy to break silently, and neither is
# covered by `go test` or `golangci-lint`.
#
#   PART 1 (git)    every path the documentation tells an operator to put live
#                   credentials, local config, captures or checkpoints in MUST be
#                   matched by .gitignore — and the committed non-secret
#                   example/template files MUST stay trackable.
#
#   PART 2 (docker) .gitignore is NOT a Docker build-context boundary. Only
#                   .dockerignore is. This part plants disposable sentinel files
#                   at every sensitive path and proves, from inside the builder,
#                   that none of them reached the build context (and therefore no
#                   layer or build cache record), while the files the build
#                   genuinely needs are all still there.
#
# Usage:
#   scripts/check-secret-hygiene.sh          # both parts (docker part SKIPs if no docker)
#   scripts/check-secret-hygiene.sh git      # part 1 only
#   scripts/check-secret-hygiene.sh docker   # part 2 only
#
# Env:
#   HYGIENE_REQUIRE_DOCKER=1   turn the "docker unavailable" SKIP into a failure
#                              (set this in CI, where docker is always present).
#
# Safety contract: this script NEVER reads, copies or transmits the content of a
# real local secret or capture. It creates sentinel files only at paths that do
# not already exist, and the in-builder probe reports only the sensitive path
# PREFIXES that leaked plus the paths of its own sentinel files.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 1

MODE="${1:-all}"
rc=0
fail() { printf 'FAIL: %s\n' "$*" >&2; rc=1; }
ok()   { printf 'ok:   %s\n' "$*"; }

# A marker no real file would ever contain. Sentinel files are filled with it so
# the probe can prove content-level absence, not just path-level absence.
MARKER='TS2OTEL-HYGIENE-SENTINEL-9f3c1a-NOT-A-REAL-SECRET'

# ---------------------------------------------------------------------------
# The shared path inventory. Everything below is a path the repository's own
# docs, compose file, chart or CLAUDE.md tells someone to create locally.
# ---------------------------------------------------------------------------

# MUST be matched by .gitignore.
SECRET_PATHS=(
  # Compose credential file. deploy/.env is the canonical location: Compose
  # loads the env file from the PROJECT directory (the directory holding the
  # compose file), not the shell's cwd.
  'deploy/.env'
  # Root equivalent, for `docker run --env-file .env` and stray tooling.
  '.env'
  '.env.local'
  'deploy/.env.local'
  '.env.production'
  'grafana.env'
  'deploy/prod.env'
  'lab.local.env'
  # Credential directory.
  '.secrets/lab.env'
  'deploy/.secrets/lab.env'
  # Local YAML configs (never committed — they carry live tailnets/tokens).
  'config.local.yaml'
  'deploy/config.local.yaml'
  'config.smoke.yaml'
  'config.lowlog.yaml'
  '.smoke.yaml'
  # Checkpoint state (window cursors; the compose file suggests a ./checkpoints
  # bind-mount next to itself, i.e. deploy/checkpoints/).
  'checkpoints.json'
  'deploy/checkpoints.json'
  'checkpoints/state.json'
  'deploy/checkpoints/state.json'
  # Captured real-tailnet fixtures.
  '.capture/devices.json'
  'deploy/.capture/devices.json'
  # Local-only working artifacts.
  'todos.txt'
  'docs/superpowers/plan.md'
  'tailscale2otel-security-findings/00-TRIAGE-SUMMARY.md'
  'THIRD_PARTY_NOTICES.md'
)

# MUST NOT be matched by .gitignore — committed examples/templates and the
# vendored spec baseline stay trackable.
TRACKABLE_PATHS=(
  'config.example.yaml'
  '.env.example'
  '.env.sample'
  'deploy/.env.example'
  'deploy/docker-compose.yaml'
  'deploy/helm/tailscale2otel/values.yaml'
  'docs/installation.md'
  'spec/tailscale-api.json'
)

# Sensitive path PREFIXES that must be absent from the Docker build context.
CONTEXT_DENY=(
  '.env'
  '.env.local'
  'deploy/.env'
  '.secrets'
  'deploy/.secrets'
  '.capture'
  'deploy/.capture'
  'config.local.yaml'
  'config.smoke.yaml'
  'config.lowlog.yaml'
  'checkpoints.json'
  'checkpoints'
  'deploy/checkpoints'
  '.git'
  'todos.txt'
  'tailscale2otel-security-findings'
  'docs'
)

# Paths the image build genuinely needs — over-denying is as bad as under-denying.
CONTEXT_REQUIRE=(
  'go.mod'
  'go.sum'
  'cmd/tailscale2otel/main.go'
  'internal/app'
  'internal/portservice/service-names-port-numbers.csv'
  'internal/app/statushtml/page.html.tmpl'
  'scripts/notices.sh'
  'scripts/notices.tsv.tmpl'
  'LICENSE'
  'config.example.yaml'
)

# ---------------------------------------------------------------------------
# PART 1 — .gitignore coverage
# ---------------------------------------------------------------------------
# rule_for <path> — the .gitignore rule that decided this path, for reporting only.
# NEVER use `git check-ignore -v`'s exit status as the verdict: it exits 0 whenever
# ANY pattern matched, including a `!` negation, so a path that is explicitly
# UN-ignored also exits 0. Only the quiet form answers "is this ignored?".
rule_for() { git check-ignore -v --no-index -- "$1" 2>/dev/null | cut -f1; }
is_ignored() { git check-ignore -q --no-index -- "$1"; }

part_git() {
  echo "== part 1: .gitignore covers every documentation-designated secret path =="
  local p
  for p in "${SECRET_PATHS[@]}"; do
    if is_ignored "$p"; then
      ok "ignored   $p   [$(rule_for "$p")]"
    else
      fail "NOT ignored: $p  (git check-ignore --no-index found no matching rule)"
    fi
  done

  echo "== part 1b: committed examples/templates remain trackable =="
  for p in "${TRACKABLE_PATHS[@]}"; do
    if is_ignored "$p"; then
      fail "wrongly ignored: $p  [$(rule_for "$p")] — an example/template must stay trackable"
    else
      ok "trackable $p   [$(rule_for "$p" || true)]"
    fi
  done

  echo "== part 1c: no already-tracked file is shadowed by an ignore rule =="
  local shadowed
  shadowed="$(git ls-files --cached --ignored --exclude-standard 2>/dev/null)"
  if [ -n "$shadowed" ]; then
    fail "these tracked files are now matched by an ignore rule:"$'\n'"$shadowed"
  else
    ok "no tracked file is shadowed by an ignore rule"
  fi
}

# ---------------------------------------------------------------------------
# PART 2 — Docker build context
# ---------------------------------------------------------------------------

SENTINELS=()          # files this script created (removed on exit)
SENTINEL_DIRS=()      # dirs this script created (removed on exit)

# shellcheck disable=SC2329  # invoked indirectly, via the EXIT trap below.
cleanup_sentinels() {
  local f d
  for f in "${SENTINELS[@]:-}"; do [ -n "$f" ] && rm -f -- "$f"; done
  # Deepest-first so nested dirs unwind cleanly. rmdir only — never rm -rf, so a
  # directory that turned out to hold real data can never be destroyed here.
  for d in $(printf '%s\n' "${SENTINEL_DIRS[@]:-}" | awk 'NF' | awk '{print length"\t"$0}' | sort -rn | cut -f2-); do
    rmdir -- "$d" 2>/dev/null || true
  done
}
trap cleanup_sentinels EXIT

# plant <path> — create a disposable sentinel, but ONLY if nothing is there. If a
# real file/dir already occupies the path we back off and leave it untouched: the
# probe's path-prefix assertion covers it without us ever touching real data.
plant() {
  local path="$1" dir
  if [ -e "$path" ]; then
    printf 'note: %s already exists — not planting a sentinel, relying on the path-prefix assertion\n' "$path"
    return 0
  fi
  dir="$(dirname -- "$path")"
  if [ "$dir" != "." ] && [ ! -d "$dir" ]; then
    mkdir -p -- "$dir"
    # Record every level we may have created, innermost first.
    local d="$dir"
    while [ "$d" != "." ] && [ "$d" != "/" ]; do
      SENTINEL_DIRS+=("$d")
      d="$(dirname -- "$d")"
    done
  fi
  printf '%s\n' "$MARKER" > "$path"
  SENTINELS+=("$path")
}

part_docker() {
  echo "== part 2: sensitive paths never enter the Docker build context =="

  if ! docker version >/dev/null 2>&1; then
    if [ "${HYGIENE_REQUIRE_DOCKER:-0}" = "1" ]; then
      fail "docker is unavailable and HYGIENE_REQUIRE_DOCKER=1 — cannot verify the build context"
    else
      echo "SKIP: docker is unavailable — the build-context assertions were NOT run." >&2
      echo "SKIP: set HYGIENE_REQUIRE_DOCKER=1 to make this a hard failure (CI does)." >&2
    fi
    return 0
  fi

  local p
  for p in "${SECRET_PATHS[@]}"; do plant "$p"; done
  # Also plant inside the capture/secret dirs if they do not already exist.
  plant '.secrets/sentinel.env'
  plant '.capture/sentinel.ndjson'

  # Reuse the exact builder image deploy/Dockerfile pins, so this script adds no
  # second image reference for Renovate to track and no unpinned tag.
  local base
  base="$(sed -n 's/^FROM \(golang:[^ ]*\) AS build$/\1/p' deploy/Dockerfile | head -1)"
  if [ -z "$base" ]; then
    fail "could not read the pinned builder image out of deploy/Dockerfile"
    return 0
  fi

  local probe
  probe="$(mktemp -t ts2otel-hygiene-probe.XXXXXX)"
  # The single-quoted printf formats below are deliberate: $DENY/$REQ/$MARK/$p/$bad/
  # $hits must reach the CONTAINER's shell unexpanded (they are build args resolved
  # inside the build), so they must not expand here.
  # shellcheck disable=SC2016
  {
    printf 'FROM %s AS probe\n' "$base"
    printf 'ARG DENY\nARG REQ\nARG MARK\n'
    printf 'COPY . /ctx\n'
    # One short RUN: BuildKit echoes the whole command on failure, so keeping it
    # to a loop (rather than one `if` per path) keeps the failure log readable.
    printf 'RUN set -u; bad=0; \\\n'
    printf '    for p in $DENY;  do [ -e "/ctx/$p" ] && { echo "LEAK: $p is in the build context"; bad=1; }; done; \\\n'
    printf '    for p in $REQ;   do [ -e "/ctx/$p" ] || { echo "MISSING: $p is needed by the build but was excluded"; bad=1; }; done; \\\n'
    printf '    hits=$(grep -rl "$MARK" /ctx 2>/dev/null); \\\n'
    printf '    [ -n "$hits" ] && { echo "LEAK: sentinel marker reached the context:"; echo "$hits"; bad=1; }; \\\n'
    printf '    [ "$bad" -eq 0 ] || exit 1; echo "OK: build context is clean"\n'
  } > "$probe"

  local out
  if out="$(docker build --no-cache --progress=plain \
      --build-arg "DENY=${CONTEXT_DENY[*]}" \
      --build-arg "REQ=${CONTEXT_REQUIRE[*]}" \
      --build-arg "MARK=$MARKER" \
      -f "$probe" -o type=cacheonly . 2>&1)"; then
    ok "build context is clean (no sensitive path or sentinel reached the builder; all required paths present)"
  else
    fail "the Docker build-context probe found problems:"
    printf '%s\n' "$out" | grep -E '(LEAK|MISSING):' | sed -E 's/^#[0-9]+ [0-9.]+ //' | sort -u
  fi
  rm -f "$probe"
}

case "$MODE" in
  git)    part_git ;;
  docker) part_docker ;;
  all)    part_git; echo; part_docker ;;
  *)      echo "usage: $0 [git|docker|all]" >&2; exit 2 ;;
esac

echo
if [ "$rc" -eq 0 ]; then
  echo "check-secret-hygiene: PASS"
else
  echo "check-secret-hygiene: FAIL" >&2
fi
exit "$rc"
