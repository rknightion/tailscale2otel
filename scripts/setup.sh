#!/usr/bin/env sh
#
# One-time dev bootstrap (idempotent, always exits 0): point git at the repo's
# checked-in hooks so the pre-commit artifact-regen hook (.githooks/pre-commit)
# is active. Git deliberately runs nothing on clone, so this must run once per
# clone — it's wired into `go generate ./...` (see cmd/tailscale2otel/generate.go)
# and can also be run directly:
#
#   scripts/setup.sh
#
# CI never runs `go generate`, so this never executes there. The pre-commit hook
# only ever SKIPs (never blocks) on a missing tool and CI's fail-on-diff gates are
# the hard backstop, so a missing local tool here is advisory only.
set -u

ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || {
  echo "setup: not a git repo — skipping hook install" >&2
  exit 0
}
cd "$ROOT" || exit 0

current=$(git config --local core.hooksPath 2>/dev/null || true)
if [ "$current" = ".githooks" ]; then
  echo "setup: git hooks already enabled (core.hooksPath=.githooks)"
elif git config --local core.hooksPath .githooks; then
  echo "setup: enabled git hooks (core.hooksPath=.githooks)"
else
  echo "setup: could not set core.hooksPath — run manually: git config core.hooksPath .githooks" >&2
fi

# Advisory only — warn (never fail) if a generator tool the hook uses is missing.
missing=
for t in helm-docs helm-values-schema-json go; do
  command -v "$t" >/dev/null 2>&1 || missing="$missing $t"
done
[ -n "$missing" ] && echo "setup: NOTE missing generator tool(s):$missing — local regen for those is skipped (CI still gates them)" >&2

exit 0
