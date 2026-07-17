#!/usr/bin/env bash
#
# Regenerate the repo's committed *generated* artifacts so they never drift from
# their sources (which is what fails CI's `fail-on-diff` gates). Each artifact is
# a pure function of its inputs:
#
#   chart README.md     <- Chart.yaml + values.yaml + README.md.gotmpl   (helm-docs)
#   values.schema.json  <- values.yaml                                   (helm-values-schema-json, draft 7)
#   docs/metrics.md     <- the in-code telemetry catalog                 (tools/metricscatalog)
#   docs/env-vars.md    <- config.example.yaml                           (TestEnvReferenceDocInSync -update)
#
# The commands here mirror CI exactly (.github/workflows/helm.yml, the
# metricscatalog step in CLAUDE.md, and the `go test` gate for env-vars.md) so
# local output is byte-identical to what CI expects. Run it by hand
# (`scripts/regen-generated.sh`) or let the pre-commit hook (.githooks/pre-commit)
# call it with just the targets your staged changes touched.
#
# Usage:
#   scripts/regen-generated.sh                  # regenerate everything
#   scripts/regen-generated.sh tools            # install/pin the helm tools (see below)
#   scripts/regen-generated.sh helm             # README.md + values.schema.json
#   scripts/regen-generated.sh helm-docs        # just the chart README.md
#   scripts/regen-generated.sh helm-schema      # just values.schema.json
#   scripts/regen-generated.sh metrics          # just docs/metrics.md
#   scripts/regen-generated.sh envref           # just docs/env-vars.md
#
# A missing OR VERSION-MISMATCHED tool is a loud SKIP (not a failure) so the hook
# never blocks a commit — CI's fail-on-diff checks remain the hard backstop. A
# regeneration that actually errors (e.g. the code doesn't compile) DOES fail.
#
# ---------------------------------------------------------------------------
# Why the helm tool versions are PINNED here (re-learn-proof)
# ---------------------------------------------------------------------------
# CI pins the *actions*, and each action installs a specific tool binary. A local
# tool of a different version silently produces DIFFERENT output, which then
# fails CI's fail-on-diff — or worse, gets committed as unrelated churn. So the
# versions below must track what the actions in .github/workflows/helm.yml
# install. Run `scripts/regen-generated.sh tools` to install exactly those.
#
#   helm-docs            v1.14.2  <- losisin/helm-docs-github-action@v2
#   helm-values-schema-json v2.5.0 <- losisin/helm-values-schema-json-action@v3.0.1
#                                     (action v3.0.1 pins TOOL v2.5.0 — the action
#                                      and tool versions deliberately differ; the
#                                      tool version is baked into the action's
#                                      dist/index.js as `version$1`.)
#
# THE helm-docs LDFLAGS GOTCHA: helm-docs takes its version from a build-time
# ldflag (`var version string` in package main), NOT from Go build info. A plain
# `go install github.com/norwoodj/helm-docs/cmd/helm-docs@v1.14.2` leaves it
# EMPTY, and the README template's `{{ template "helm-docs.versionFooter" . }}`
# is guarded by `{{ if .HelmDocsVersion }}` — so an empty version silently drops
# the whole footer from the generated README, producing a *plausible but wrong*
# file that differs from CI's. The upstream release binaries are built by
# goreleaser, whose default ldflags set `-X main.version={{.Version}}`. So the
# install below must pass that ldflag (with the leading `v` stripped, matching
# goreleaser's {{.Version}}). An empty version is also why a broken install has
# no `--version` flag at all: cobra only registers it when Version is non-empty,
# which is exactly what version_of() keys off to detect the bad install.
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
CHART_DIR="$ROOT/deploy/helm/tailscale2otel"

# Pinned tool versions — keep in sync with .github/workflows/helm.yml (see above).
HELM_DOCS_VERSION="v1.14.2"
HELM_DOCS_PKG="github.com/norwoodj/helm-docs/cmd/helm-docs"
HELM_SCHEMA_VERSION="v2.5.0"
HELM_SCHEMA_PKG="github.com/losisin/helm-values-schema-json/v2"

note() { printf '  regen: %s\n' "$1"; }
skip() { printf '  regen: SKIP %s\n' "$1" >&2; }

# version_of <bin> — prints the tool's semver (no leading "v"), or nothing when the
# binary is absent or reports no version. Both tools print a single line
# containing an x.y.z ("helm-docs version 1.14.2", "helm schema version v2.5.0").
version_of() {
  command -v "$1" >/dev/null 2>&1 || return 0
  "$1" --version 2>/dev/null | head -1 | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1
}

install_helm_docs() {
  note "installing helm-docs $HELM_DOCS_VERSION (with the version ldflag — see header)"
  go install -ldflags "-s -w -X main.version=${HELM_DOCS_VERSION#v}" \
    "${HELM_DOCS_PKG}@${HELM_DOCS_VERSION}"
}

install_helm_schema() {
  note "installing helm-values-schema-json $HELM_SCHEMA_VERSION"
  go install "${HELM_SCHEMA_PKG}@${HELM_SCHEMA_VERSION}"
}

# have_tool <bin> <pinned-version> <install-target> — true when <bin> is installed
# AT the pinned version. Anything else is a loud SKIP naming the exact fix, rather
# than a silent regeneration with the wrong tool.
have_tool() {
  local bin="$1" want="${2#v}" target="$3" got
  got="$(version_of "$bin")"
  if [ -z "$got" ]; then
    if command -v "$bin" >/dev/null 2>&1; then
      skip "$bin reports no version (likely 'go install'ed without the version ldflag) -> not regenerated. Fix: scripts/regen-generated.sh $target"
    else
      skip "$bin not installed -> not regenerated (CI will gate it). Fix: scripts/regen-generated.sh $target"
    fi
    return 1
  fi
  if [ "$got" != "$want" ]; then
    skip "$bin is $got but CI uses $want -> not regenerated (its output would differ). Fix: scripts/regen-generated.sh $target"
    return 1
  fi
  return 0
}

regen_tools() {
  if ! command -v go >/dev/null 2>&1; then
    skip "go not installed -> cannot install the helm tools"
    return 0
  fi
  install_helm_docs
  install_helm_schema
  note "tools pinned: helm-docs $(version_of helm-docs), helm-values-schema-json $(version_of helm-values-schema-json)"
}

regen_helm_docs() {
  have_tool helm-docs "$HELM_DOCS_VERSION" tools || return 0
  note "chart README.md (helm-docs $HELM_DOCS_VERSION)"
  # Mirrors losisin/helm-docs-github-action@v2 defaults in helm.yml.
  helm-docs \
    --chart-search-root "$CHART_DIR" \
    --values-file values.yaml \
    --output-file README.md \
    --template-files README.md.gotmpl
}

regen_helm_schema() {
  have_tool helm-values-schema-json "$HELM_SCHEMA_VERSION" tools || return 0
  note "values.schema.json (draft 7, helm-values-schema-json $HELM_SCHEMA_VERSION)"
  # Mirrors losisin/helm-values-schema-json-action@v2 in helm.yml (draft: 7).
  helm-values-schema-json \
    --values "$CHART_DIR/values.yaml" \
    --output "$CHART_DIR/values.schema.json" \
    --draft 7
}

regen_metrics() {
  if ! command -v go >/dev/null 2>&1; then
    skip "go not installed -> docs/metrics.md not regenerated (CI will gate it)"
    return 0
  fi
  note "docs/metrics.md (metricscatalog)"
  # tools/metricscatalog is a separate module; -C enters it, -file is CWD-relative
  # so pass an absolute path. Mirrors the command in CLAUDE.md.
  go run -C "$ROOT/tools/metricscatalog" . -write -file "$ROOT/docs/metrics.md"
}

regen_envref() {
  if ! command -v go >/dev/null 2>&1; then
    skip "go not installed -> docs/env-vars.md not regenerated (CI will gate it)"
    return 0
  fi
  note "docs/env-vars.md (config env-var reference)"
  # The reference table is generated from config.example.yaml by the golden test's
  # -update mode (root module; no separate tool). CI's `go test` run gates drift.
  go test -C "$ROOT" ./internal/config -run TestEnvReferenceDocInSync -update -count=1 >/dev/null
}

main() {
  local targets=("$@")
  [ ${#targets[@]} -eq 0 ] && targets=(all)

  local do_tools=0 do_docs=0 do_schema=0 do_metrics=0 do_envref=0
  for t in "${targets[@]}"; do
    case "$t" in
      # `all` deliberately does NOT install tools — it must stay side-effect-free
      # for the pre-commit hook. Run `tools` explicitly (once per machine).
      all)         do_docs=1; do_schema=1; do_metrics=1; do_envref=1 ;;
      tools)       do_tools=1 ;;
      helm)        do_docs=1; do_schema=1 ;;
      helm-docs)   do_docs=1 ;;
      helm-schema) do_schema=1 ;;
      metrics)     do_metrics=1 ;;
      envref)      do_envref=1 ;;
      *) printf 'regen-generated.sh: unknown target %q\n' "$t" >&2; exit 2 ;;
    esac
  done

  [ "$do_tools" = 1 ]   && regen_tools
  [ "$do_docs" = 1 ]    && regen_helm_docs
  [ "$do_schema" = 1 ]  && regen_helm_schema
  [ "$do_metrics" = 1 ] && regen_metrics
  [ "$do_envref" = 1 ]  && regen_envref
  return 0
}

main "$@"
