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
#   scripts/regen-generated.sh helm             # README.md + values.schema.json
#   scripts/regen-generated.sh helm-docs        # just the chart README.md
#   scripts/regen-generated.sh helm-schema      # just values.schema.json
#   scripts/regen-generated.sh metrics          # just docs/metrics.md
#   scripts/regen-generated.sh envref           # just docs/env-vars.md
#
# A missing tool is a loud SKIP (not a failure) so the hook never blocks a
# commit — CI's fail-on-diff checks remain the hard backstop. A regeneration
# that actually errors (e.g. the code doesn't compile) DOES fail.
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
CHART_DIR="$ROOT/deploy/helm/tailscale2otel"

note() { printf '  regen: %s\n' "$1"; }
skip() { printf '  regen: SKIP %s\n' "$1" >&2; }

regen_helm_docs() {
  if ! command -v helm-docs >/dev/null 2>&1; then
    skip "helm-docs not installed -> chart README.md not regenerated (CI will gate it)"
    return 0
  fi
  note "chart README.md (helm-docs)"
  # Mirrors losisin/helm-docs-github-action@v2 defaults in helm.yml.
  helm-docs \
    --chart-search-root "$CHART_DIR" \
    --values-file values.yaml \
    --output-file README.md \
    --template-files README.md.gotmpl
}

regen_helm_schema() {
  if ! command -v helm-values-schema-json >/dev/null 2>&1; then
    skip "helm-values-schema-json not installed -> values.schema.json not regenerated (CI will gate it)"
    return 0
  fi
  note "values.schema.json (draft 7)"
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

  local do_docs=0 do_schema=0 do_metrics=0 do_envref=0
  for t in "${targets[@]}"; do
    case "$t" in
      all)         do_docs=1; do_schema=1; do_metrics=1; do_envref=1 ;;
      helm)        do_docs=1; do_schema=1 ;;
      helm-docs)   do_docs=1 ;;
      helm-schema) do_schema=1 ;;
      metrics)     do_metrics=1 ;;
      envref)      do_envref=1 ;;
      *) printf 'regen-generated.sh: unknown target %q\n' "$t" >&2; exit 2 ;;
    esac
  done

  [ "$do_docs" = 1 ]    && regen_helm_docs
  [ "$do_schema" = 1 ]  && regen_helm_schema
  [ "$do_metrics" = 1 ] && regen_metrics
  [ "$do_envref" = 1 ]  && regen_envref
  return 0
}

main "$@"
