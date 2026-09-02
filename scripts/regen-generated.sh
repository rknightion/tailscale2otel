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
#   config.schema.json  <- the Config struct + config.example.yaml       (TestConfigSchemaInSync -update)
#   docs/signal-coverage.md <- internal/catalog/signal_dispositions.json (TestSignalCoverageDocInSync -update)
#   deploy/alerts/prometheus/tailscale2otel.rules.yaml <- alerts catalogue (build_rules.py --prom-out)
#   internal/catalog/capability_counts.json <- catalog + shipped artifacts (check-capability-counts.py)
#
# The commands here mirror CI exactly (.github/workflows/helm.yml, the
# metricscatalog step in CLAUDE.md, and the `go test` gate for env-vars.md) so
# local output is byte-identical to what CI expects. Run it through `just gen`,
# or let the pre-commit hook (.githooks/pre-commit) invoke just the `gen-*`
# recipes your staged changes touched.
#
# Usage:
#   scripts/regen-generated.sh <target> [<target>...]
#
# THE TARGET LIST IS NOT MAINTAINED HERE ANY MORE. Every target is simply the
# name of a `regen_<target>` function below (dashes spelled as underscores), and
# each has a matching `gen-<target>` recipe in the justfile — which is where
# `just --list` advertises it, with a doc comment naming the artifact. There is
# no dispatch table to forget to extend: config.schema.json was missing from the
# old one for months (TSO-0026), and its absence was invisible because the list
# lived in a shell script nobody reads.
#
# The COMPOSITES live in the justfile too, as recipe dependencies: `just gen`
# regenerates everything and `just gen-helm` does the chart pair. This script no
# longer decides what "everything" means, and it takes no default target.
#
#   just gen                # everything (the entry point; see `just --list`)
#   just gen-<target>       # one family
#   just gen <target>...    # same, spelled the way the docs and error messages do
#
# Call this script DIRECTLY only where `just` is not available:
# scripts/cloud-environment-setup.sh runs `scripts/regen-generated.sh tools` to
# provision an environment that has no `just` on PATH yet.
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
# install. Run `just gen-tools` to install exactly those.
#
#   helm-docs            v1.14.2  <- losisin/helm-docs-github-action@v2
#   helm-values-schema-json v2.6.0 <- losisin/helm-values-schema-json-action@v3.2.0
#                                     (action v3.2.0 pins TOOL v2.6.0 — the action
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
HELM_SCHEMA_VERSION="v2.6.0"
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
      skip "$bin reports no version (likely 'go install'ed without the version ldflag) -> not regenerated. Fix: just gen-$target"
    else
      skip "$bin not installed -> not regenerated (CI will gate it). Fix: just gen-$target"
    fi
    return 1
  fi
  if [ "$got" != "$want" ]; then
    skip "$bin is $got but CI uses $want -> not regenerated (its output would differ). Fix: just gen-$target"
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
  # Mirrors losisin/helm-values-schema-json-action in helm.yml (draft: 7,
  # additionalProperties: false). Keep the two in step: the schema-root flag has
  # no values.yaml annotation equivalent — a `# @schema` comment at the top of the
  # file attaches to the first KEY, not to the root mapping — so root strictness
  # exists only here and in the workflow input. Drop it from either side and
  # `--set secrets.foo=bar` silently starts rendering again (#304).
  helm-values-schema-json \
    --values "$CHART_DIR/values.yaml" \
    --output "$CHART_DIR/values.schema.json" \
    --schema-root.additional-properties=false \
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

regen_config_schema() {
  if ! command -v go >/dev/null 2>&1; then
    skip "go not installed -> config.schema.json not regenerated (CI will gate it)"
    return 0
  fi
  note "config.schema.json (root config JSON Schema)"
  # NOT the chart's values.schema.json — that is the `helm-schema` target. This is
  # the repo-root schema describing config.yaml itself, generated from the Config
  # struct and config.example.yaml by the golden test's -update mode. Its drift
  # gate is TestConfigSchemaInSync inside the normal `go test -race ./...` run, so
  # a stale file fails the suite rather than a dedicated workflow step.
  go test -C "$ROOT" ./internal/config -run TestConfigSchemaInSync -update -count=1 >/dev/null
}

regen_coverage() {
  if ! command -v go >/dev/null 2>&1; then
    skip "go not installed -> docs/signal-coverage.md not regenerated (CI will gate it)"
    return 0
  fi
  note "docs/signal-coverage.md (from internal/catalog/signal_dispositions.json)"
  # The page is a pure function of the COMMITTED manifest, so regenerating it is
  # always safe. The manifest itself is deliberately NOT regenerated here: adding
  # or pruning its rows is a decision, not a formatting step, so it stays a
  # hand-run `go test ./internal/catalog -run TestSignalDispositionsInSync -update`.
  go test -C "$ROOT" ./internal/catalog -run TestSignalCoverageDocInSync -update -count=1 >/dev/null
}

regen_dashboards() {
  if ! command -v python3 >/dev/null 2>&1; then
    skip "python3 not installed -> deploy/grafana + deploy/alerts not regenerated (CI will gate them)"
    return 0
  fi
  # ONE invocation writes the whole dashboard FAMILY (#526): build.py emits every
  # entry in dashboards.ALL to its own spec.out. Both must be built in one process
  # so the signal-coverage union is computed across them.
  note "deploy/grafana/tailscale2otel-{tailnet,health}.json (grafana/gen/build.py)"
  python3 "$ROOT/deploy/grafana/gen/build.py" --out-dir "$ROOT" >/dev/null
  # MUST run after build.py: build_rules.py resolves each alert's canonical panel
  # BY TITLE against the generated dashboard, and hard-fails on a title that
  # matches zero or more than one panel.
  note "deploy/alerts/grafana-managed/ (alerts/gen/build_rules.py)"
  python3 "$ROOT/deploy/alerts/gen/build_rules.py" --out "$ROOT/deploy/alerts/grafana-managed" >/dev/null
  note "docs/alert-profiles.md (alerts/gen/build_rules.py --docs-out)"
  # The installable-profile (#389) installation guide: a pure function of the
  # PROFILES table and each rule's policy in build_rules.py, so it is safe to
  # regenerate unconditionally alongside the manifests above. Drift is gated by
  # a unittest in deploy/alerts/gen/test_rules.py (run in the `dashboards-drift`
  # CI job's "generator unit tests" step), not by the fail-on-diff `git diff`
  # below — that check is scoped to deploy/grafana + deploy/alerts on purpose.
  python3 "$ROOT/deploy/alerts/gen/build_rules.py" --docs-out "$ROOT/docs/alert-profiles.md" >/dev/null
  # Nothing under deploy/grafana or deploy/alerts is hand-maintained any more —
  # the four legacy classic-schema dashboards and the Prometheus-ruler rules file
  # were deleted (#394) precisely because sitting outside this gate let them rot.
  # Both generators are stdlib-only and deterministic (no time/random/set
  # ordering), so rerunning them is a no-op on a clean tree, which is what makes
  # the CI fail-on-diff gate possible.
  #
  # The supported Prometheus rendering is generated separately by the
  # `promrules` target below. Keeping it out of this function lets a rule-only
  # lane regenerate and validate its artifact without rebuilding dashboards.
}

regen_promrules() {
  if ! command -v python3 >/dev/null 2>&1; then
    skip "python3 not installed -> deploy/alerts/prometheus/tailscale2otel.rules.yaml not regenerated (CI will gate it)"
    return 0
  fi
  note "deploy/alerts/prometheus/tailscale2otel.rules.yaml (alerts/gen/build_rules.py --prom-out)"
  mkdir -p "$ROOT/deploy/alerts/prometheus"
  python3 "$ROOT/deploy/alerts/gen/build_rules.py" \
    --prom-out "$ROOT/deploy/alerts/prometheus/tailscale2otel.rules.yaml" >/dev/null
}

regen_counts() {
  if ! command -v python3 >/dev/null 2>&1; then
    skip "python3 not installed -> internal/catalog/capability_counts.json not regenerated (CI will gate it)"
    return 0
  fi
  note "internal/catalog/capability_counts.json (public capability counts)"
  python3 "$ROOT/scripts/check-capability-counts.py" --write
}

# The target list, derived from the functions above rather than restated.
known_targets() {
  declare -F | sed -n 's/^declare -f regen_//p' | tr '_' '-' | sort
}

usage() {
  {
    printf 'usage: %s <target> [<target>...]\n\n' "${0##*/}"
    printf 'targets (one per generated-artifact family):\n'
    known_targets | sed 's/^/  /'
    printf '\nPrefer the justfile: `just gen` regenerates everything and\n'
    printf '`just gen-<target>` one family, each documented in `just --list`.\n'
  } >&2
}

main() {
  [ "$#" -gt 0 ] || { printf '%s: no target given\n\n' "${0##*/}" >&2; usage; exit 2; }

  # Resolve EVERY target before running ANY of them, so a typo in the second
  # argument cannot leave the first half-applied.
  local t
  for t in "$@"; do
    if ! declare -F "regen_${t//-/_}" >/dev/null 2>&1; then
      printf '%s: unknown target %q\n\n' "${0##*/}" "$t" >&2
      usage
      exit 2
    fi
  done

  # No fan-out and no implicit ordering: `just` composes the families (and owns
  # the one real ordering constraint — the coverage page reports on what the
  # dashboards reference, so `gen-coverage` is listed last in `gen-all`).
  for t in "$@"; do
    "regen_${t//-/_}"
  done
}

main "$@"
