#!/usr/bin/env bash
# Mirror CI's per-module verification locally, for all four Go modules.
#
# Why this exists: there is no go.work, ON PURPOSE — each tool module is a
# separate go.mod so it never affects the main module's `go build ./...`. The
# consequence is that NOTHING run from the repository root reaches them, so
# `go test -race ./...` at the root is not "the test suite", it is the root
# module's test suite. This script is the whole thing (#437).
#
# It runs the same legs as ci.yml's `build-test` + `govulncheck` (root) and
# `module-verify` (tool modules), in the same order, so a green run here means a
# green run there.
#
# Usage:
#   scripts/verify-modules.sh            # every module
#   scripts/verify-modules.sh .          # just the root module
#   scripts/verify-modules.sh tools/apidrift tools/configcheck
#
# golangci-lint is included when it is on PATH and SKIPPED LOUDLY when it is not,
# rather than silently passing — a skipped lint that looks like a pass is how a
# local gate stops meaning anything.
set -uo pipefail

cd "$(dirname "$0")/.." || exit 1
ROOT="$PWD"

# Discovered rather than hardcoded, so a new module is covered the day it is added
# — the same property internal/ci's TestEveryGoModuleIsCoveredByCIVerification
# enforces for the CI matrices.
discover_modules() {
  find . -name go.mod -not -path './.git/*' -not -path './.capture/*' \
    | sed -e 's|/go.mod$||' -e 's|^\./||' -e 's|^\.$|.|' \
    | sed -e 's|^$|.|' \
    | sort
}

if [ "$#" -gt 0 ]; then
  MODULES=("$@")
else
  # shellcheck disable=SC2207 # module paths never contain whitespace
  MODULES=($(discover_modules))
fi

failures=()
skips=()

run_leg() {
  local module=$1 label=$2
  shift 2
  printf '  %-22s ' "$label"
  if out=$("$@" 2>&1); then
    echo "ok"
    return 0
  fi
  echo "FAIL"
  printf '%s\n' "$out" | sed 's/^/      /'
  failures+=("$module: $label")
  return 1
}

for module in "${MODULES[@]}"; do
  echo "== $module"
  if [ ! -f "$ROOT/$module/go.mod" ]; then
    echo "  no go.mod — skipping"
    skips+=("$module: no go.mod")
    continue
  fi
  cd "$ROOT/$module" || exit 1

  run_leg "$module" "go build" go build ./...
  run_leg "$module" "go vet" go vet ./...
  run_leg "$module" "go test -race" go test -race ./...

  # go mod tidy is a pure function of the source, so any diff it produces is
  # drift: Renovate bumps a dependency in one module and the nested ones keep the
  # old requirement, or a `replace ../..` goes stale after a module major bump.
  # Restore the files either way — this must never leave the tree dirty.
  printf '  %-22s ' "go mod tidy diff"
  cp go.mod "$ROOT/.tidy.go.mod.bak" && cp go.sum "$ROOT/.tidy.go.sum.bak"
  if go mod tidy >/dev/null 2>&1 &&
    diff -q "$ROOT/.tidy.go.mod.bak" go.mod >/dev/null &&
    diff -q "$ROOT/.tidy.go.sum.bak" go.sum >/dev/null; then
    echo "ok"
  else
    echo "FAIL — not tidy; run 'go mod tidy' in $module and commit the result"
    diff -u "$ROOT/.tidy.go.mod.bak" go.mod | sed 's/^/      /' | head -20
    failures+=("$module: go mod tidy diff")
  fi
  cp "$ROOT/.tidy.go.mod.bak" go.mod && cp "$ROOT/.tidy.go.sum.bak" go.sum
  rm -f "$ROOT/.tidy.go.mod.bak" "$ROOT/.tidy.go.sum.bak"

  if command -v golangci-lint >/dev/null 2>&1; then
    run_leg "$module" "golangci-lint" golangci-lint run
  else
    printf '  %-22s SKIP — golangci-lint not on PATH (CI still runs it)\n' "golangci-lint"
    skips+=("$module: golangci-lint")
  fi

  if command -v govulncheck >/dev/null 2>&1; then
    run_leg "$module" "govulncheck" govulncheck ./...
  else
    printf '  %-22s SKIP — install with: go install golang.org/x/vuln/cmd/govulncheck@v1.3.0\n' "govulncheck"
    skips+=("$module: govulncheck")
  fi

  cd "$ROOT" || exit 1
done

echo
if [ "${#skips[@]}" -gt 0 ]; then
  echo "SKIPPED (${#skips[@]}):"
  printf '  - %s\n' "${skips[@]}"
fi
if [ "${#failures[@]}" -gt 0 ]; then
  echo "FAILED (${#failures[@]}):"
  printf '  - %s\n' "${failures[@]}"
  exit 1
fi
echo "all modules verified: ${MODULES[*]}"
