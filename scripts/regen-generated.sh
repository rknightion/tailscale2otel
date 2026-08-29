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
# local output is byte-identical to what CI expects. Run it by hand
# (`scripts/regen-generated.sh`) or let the pre-commit hook (.githooks/pre-commit)
# call it with just the targets your staged changes touched.
#
# Usage:
#   scripts/regen-generated.sh                  # regenerate everything
#   scripts/regen-generated.sh tools            # install/pin the helm tools (see below)
#   scripts/regen-generated.sh helm             # README.md + values.schema.json
#   scripts/regen-generated.sh helm-docs        # just the chart README.md
#   scripts/regen-generated.sh helm-schema      # just the CHART's values.schema.json
#   scripts/regen-generated.sh config-schema    # just the ROOT config.schema.json
#   scripts/regen-generated.sh metrics          # just docs/metrics.md
#   scripts/regen-generated.sh envref           # just docs/env-vars.md
#   scripts/regen-generated.sh coverage         # just docs/signal-coverage.md
#   scripts/regen-generated.sh promrules        # just the shipped Prometheus rules
#   scripts/regen-generated.sh counts           # just the public capability-count source
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
#   helm-values-schema-json v2.5.0 <- losisin/helm-values-schema-json-action@v3.1.0
#                                     (action v3.1.0 pins TOOL v2.5.0 — the action
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

main() {
  local targets=("$@")
  [ ${#targets[@]} -eq 0 ] && targets=(all)

  local do_tools=0 do_docs=0 do_schema=0 do_metrics=0 do_envref=0 do_dash=0 do_cov=0 do_promrules=0 do_counts=0 do_cfgschema=0
  for t in "${targets[@]}"; do
    case "$t" in
      # `all` deliberately does NOT install tools — it must stay side-effect-free
      # for the pre-commit hook. Run `tools` explicitly (once per machine).
      all)         do_docs=1; do_schema=1; do_metrics=1; do_envref=1; do_dash=1; do_cov=1; do_promrules=1; do_counts=1; do_cfgschema=1 ;;
      tools)       do_tools=1 ;;
      helm)        do_docs=1; do_schema=1 ;;
      helm-docs)   do_docs=1 ;;
      helm-schema) do_schema=1 ;;
      config-schema) do_cfgschema=1 ;;
      metrics)     do_metrics=1 ;;
      envref)      do_envref=1 ;;
      coverage)    do_cov=1 ;;
      dashboards)  do_dash=1 ;;
      promrules)   do_promrules=1 ;;
      counts)      do_counts=1 ;;
      *) printf 'regen-generated.sh: unknown target %q\n' "$t" >&2; exit 2 ;;
    esac
  done

  [ "$do_tools" = 1 ]   && regen_tools
  [ "$do_docs" = 1 ]    && regen_helm_docs
  [ "$do_schema" = 1 ]  && regen_helm_schema
  [ "$do_metrics" = 1 ] && regen_metrics
  [ "$do_envref" = 1 ]  && regen_envref
  [ "$do_cfgschema" = 1 ] && regen_config_schema
  [ "$do_dash" = 1 ]    && regen_dashboards
  [ "$do_promrules" = 1 ] && regen_promrules
  [ "$do_counts" = 1 ]  && regen_counts
  # After the dashboards: the coverage page reports on what they reference.
  [ "$do_cov" = 1 ]     && regen_coverage
  return 0
}

main "$@"
