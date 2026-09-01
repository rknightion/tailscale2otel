set shell := ["bash", "-euo", "pipefail", "-c"]

# Every Go module in the repository. There is no go.work ON PURPOSE — each tool
# module is a separate go.mod so it never affects the root module's `go build
# ./...` — so nothing run from the root reaches them and every leg has to be
# applied per module. internal/ci/TestEveryGoModuleIsCoveredByCIVerification (and
# its new justfile sibling, see the task) enforces that this list is complete.
modules := ". tools/apidrift tools/configcheck tools/metricscatalog tools/promqlcheck"

# The CI-only tool modules. The root module is deliberately excluded: it has
# dedicated legs and duplicating it here would double the slowest work.
tool_modules := "tools/apidrift tools/configcheck tools/metricscatalog tools/promqlcheck"

chart_dir := "deploy/helm/tailscale2otel"

# Pinned to what CI installs. golangci-lint tracks .github/workflows/ci.yml's
# golangci-lint-action `version:`; govulncheck tracks the `go install` line in the
# module-verify and govulncheck jobs. A local tool of another version reports
# different findings than the gate.
golangci_version := "v2.13.2"
govulncheck_version := "v1.3.0"

# show the task surface
default:
    @just --list

# install the pinned toolchain, the git hooks and every module's deps (idempotent)
[script('bash')]
setup:
    set -euo pipefail
    # Kept as a script: cmd/tailscale2otel/generate.go's //go:generate directive
    # invokes it directly, so it must keep working with no `just` on PATH.
    scripts/setup.sh
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@{{ golangci_version }}
    go install golang.org/x/vuln/cmd/govulncheck@{{ govulncheck_version }}
    # Installs helm-docs v1.14.2 (with the version ldflag) and
    # helm-values-schema-json v2.5.0 — the exact versions the helm.yml actions use.
    just gen-tools
    for m in {{ modules }}; do
      echo "== go mod download ($m)"
      (cd "$m" && go mod download)
    done

# format the justfile and every Go module in place
[group('dev')]
[script('bash')]
fmt:
    set -euo pipefail
    just --fmt
    for m in {{ modules }}; do
      echo "== golangci-lint fmt ($m)"
      (cd "$m" && golangci-lint fmt)
    done

# verify the justfile and every Go module are formatted; never mutates
[group('check')]
[no-exit-message]
[script('bash')]
fmt-check:
    set -euo pipefail
    just --fmt --check
    for m in {{ modules }}; do
      echo "== golangci-lint fmt --diff ($m)"
      (cd "$m" && golangci-lint fmt --diff)
    done

# run golangci-lint over one module, or every module when module is empty
[group('check')]
[no-exit-message]
[script('bash')]
lint module="":
    set -euo pipefail
    targets='{{ module }}'
    [ -n "$targets" ] || targets='{{ modules }}'
    for m in $targets; do
      echo "== golangci-lint run ($m)"
      (cd "$m" && golangci-lint run)
    done

# go vet one module, or every module when module is empty
[group('check')]
[no-exit-message]
[script('bash')]
vet module="":
    set -euo pipefail
    targets='{{ module }}'
    [ -n "$targets" ] || targets='{{ modules }}'
    for m in $targets; do
      echo "== go vet ($m)"
      go vet -C "$m" ./...
    done

# run the root module test suite with the race detector; filter narrows by -run
[group('check')]
[no-exit-message]
test filter="":
    go test -race -run '{{ filter }}' ./...

# build and race-test one tool module, or all four when module is empty
[group('check')]
[no-exit-message]
[script('bash')]
test-modules module="":
    set -euo pipefail
    targets='{{ module }}'
    [ -n "$targets" ] || targets='{{ tool_modules }}'
    for m in $targets; do
      echo "== go build + go test -race ($m)"
      go build -C "$m" ./...
      go test -C "$m" -race ./...
    done

# run the Python generator and release-gate unit tests
[group('check')]
[no-exit-message]
[script('bash')]
test-python:
    set -euo pipefail
    for d in deploy/grafana/gen deploy/alerts/gen scripts; do
      echo "== python3 -m unittest discover ($d)"
      python3 -m unittest discover -s "$d" -t "$d" -v
    done

# fail when `go mod tidy` moves go.mod/go.sum; tidies in place when it does
[group('check')]
[script('bash')]
tidy-check module="":
    set -euo pipefail
    targets='{{ module }}'
    [ -n "$targets" ] || targets='{{ modules }}'
    for m in $targets; do
      echo "== go mod tidy ($m)"
      before="$(mktemp -d)"
      cp "$m/go.mod" "$before/go.mod"
      cp "$m/go.sum" "$before/go.sum"
      go mod tidy -C "$m"
      if ! diff -u "$before/go.mod" "$m/go.mod" || ! diff -u "$before/go.sum" "$m/go.sum"; then
        rm -rf "$before"
        echo "::error::$m is not tidy — run 'just tidy-check $m' and commit the result" >&2
        exit 1
      fi
      rm -rf "$before"
    done

# scan one module for known vulnerabilities, or every module when module is empty
[group('check')]
[no-exit-message]
[script('bash')]
vuln module="":
    set -euo pipefail
    targets='{{ module }}'
    [ -n "$targets" ] || targets='{{ modules }}'
    for m in $targets; do
      echo "== govulncheck ($m)"
      (cd "$m" && govulncheck ./...)
    done

# regenerate every committed generated artifact, or only the named families
[group('gen')]
[script('bash')]
gen *targets:
    set -euo pipefail
    # Every family is a `gen-<target>` recipe, so `just --list` is the target list
    # and an unknown target fails as a just error. This spelling is kept because
    # the docs, the CI job names and a dozen test failure messages say
    # `just gen helm` / `just gen counts`; `just gen-helm` is the same thing.
    targets='{{ targets }}'
    for t in ${targets:-all}; do
      just "gen-$t"
    done

# Private: `just gen` is the advertised entry point, and this exists so `just gen`
# and `just gen all` are one definition rather than two lists.
#
# Deliberately does NOT depend on gen-tools — regenerating must stay
# side-effect-free for the pre-commit hook; installing is a once-per-machine act.
# gen-coverage runs LAST: the coverage page reports on what the dashboards
# reference, so it has to see the freshly generated ones.
#
# regenerate every generated artifact, in dependency order
[private]
gen-all: gen-helm gen-metrics gen-envref gen-config-schema gen-api-schemas gen-dashboards gen-promrules gen-counts gen-coverage

# install the pinned helm-docs + helm-values-schema-json (once per machine)
[group('gen')]
gen-tools:
    scripts/regen-generated.sh tools

# regenerate the chart's README.md and values.schema.json
[group('gen')]
gen-helm: gen-helm-docs gen-helm-schema

# regenerate deploy/helm/tailscale2otel/README.md from Chart.yaml + values.yaml + the template
[group('gen')]
gen-helm-docs:
    scripts/regen-generated.sh helm-docs

# regenerate the CHART's values.schema.json from values.yaml (draft 7)
[group('gen')]
gen-helm-schema:
    scripts/regen-generated.sh helm-schema

# regenerate the ROOT config.schema.json from the Config struct + config.example.yaml
[group('gen')]
gen-config-schema:
    scripts/regen-generated.sh config-schema

# regenerate docs/api/schemas from the published admin API response types
[group('gen')]
gen-api-schemas:
    go test ./internal/app/apicontract -run TestSchemasInSync -update

# regenerate docs/metrics.md from the in-code telemetry catalog
[group('gen')]
gen-metrics:
    scripts/regen-generated.sh metrics

# regenerate docs/env-vars.md from config.example.yaml
[group('gen')]
gen-envref:
    scripts/regen-generated.sh envref

# regenerate docs/signal-coverage.md from internal/catalog/signal_dispositions.json
[group('gen')]
gen-coverage:
    scripts/regen-generated.sh coverage

# regenerate the Grafana dashboards, the Grafana-managed rules and docs/alert-profiles.md
[group('gen')]
gen-dashboards:
    scripts/regen-generated.sh dashboards

# regenerate deploy/alerts/prometheus/tailscale2otel.rules.yaml
[group('gen')]
gen-promrules:
    scripts/regen-generated.sh promrules

# regenerate internal/catalog/capability_counts.json from the catalog + shipped artifacts
[group('gen')]
gen-counts:
    scripts/regen-generated.sh counts

# regenerate the Grafana, alert-rule and capability-count artifacts and fail on drift
[group('check')]
[script('bash')]
gen-check: gen-dashboards gen-promrules gen-counts
    set -euo pipefail
    if ! git diff --exit-code -- deploy/grafana deploy/alerts docs/alert-profiles.md internal/catalog/capability_counts.json; then
      echo "::error::generated dashboards, rules, or capability counts are out of date — run 'just gen dashboards promrules counts' and commit the result" >&2
      exit 1
    fi
    python3 scripts/check-capability-counts.py

# assert docs/metrics.md matches the in-code catalog and documented commands resolve
[group('check')]
[script('bash')]
docs-check:
    set -euo pipefail
    mkdir -p bin
    go build -C tools/metricscatalog -o "$PWD/bin/metricscatalog" .
    ./bin/metricscatalog -check
    python3 scripts/check_doc_commands.py

# helm lint + template the chart and run its render-level security contracts
[group('check')]
[script('bash')]
helm-lint:
    set -euo pipefail
    helm lint {{ chart_dir }}
    helm template t {{ chart_dir }} > /dev/null
    deploy/helm/tests/render-tests.sh

# regenerate the chart README + values.schema.json and fail on drift
[group('check')]
[script('bash')]
helm-gen-check: gen-helm
    set -euo pipefail
    if ! git diff --exit-code -- {{ chart_dir }}/README.md {{ chart_dir }}/values.schema.json; then
      echo "::error::chart README.md or values.schema.json is out of date — run 'just gen helm' and commit the result" >&2
      exit 1
    fi

# validate config.example.yaml and the chart-rendered config through config.Load
[group('check')]
[script('bash')]
config-check:
    set -euo pipefail
    mkdir -p bin
    go build -C tools/configcheck -o "$PWD/bin/configcheck" .
    helm template t {{ chart_dir }} --show-only templates/configmap.yaml \
      | yq '.data."config.yaml"' > /tmp/rendered-config.yaml
    echo "--- rendered config (head) ---"
    head -20 /tmp/rendered-config.yaml
    # The starters are part of the supported onboarding surface. Their real
    # credentials stay empty in git; placeholders make configcheck exercise the
    # provider/list validation without contacting either control plane.
    TS2OTEL_HEADSCALE__API_KEY=placeholder-headscale-api-key \
      ./bin/configcheck config.example.yaml /tmp/rendered-config.yaml \
      examples/config/grafana-cloud-otlp.yaml \
      examples/config/prometheus-only.yaml \
      examples/config/stdout.yaml \
      examples/config/headscale.yaml \
      examples/config/multi-tailnet.yaml

# parse every dashboard panel and provisioned rule expression with the Prometheus parser
[group('check')]
[no-exit-message]
promql:
    go run -C tools/promqlcheck . -root "$PWD"

# promtool-validate and unit-test the shipped Prometheus rules
[group('check')]
[no-exit-message]
[script('bash')]
rules-check:
    set -euo pipefail
    promtool check rules deploy/alerts/prometheus/tailscale2otel.rules.yaml
    promtool test rules deploy/alerts/tests/*.yaml

# assert the .gitignore and Docker build-context secret boundaries both hold
[group('check')]
hygiene:
    scripts/check-secret-hygiene.sh

# resolve the Compose assets in default and file-backed modes, self-test included
[group('check')]
compose-check:
    deploy/tests/compose-tests.sh --self-test

# start the supported Compose stack with file-backed secrets and prove it becomes healthy
[group('check')]
[script('bash')]
smoke tag="tailscale2otel:dev":
    set -euo pipefail
    d="$(mktemp -d)"
    project="tailscale2otel-smoke-$$"
    cleanup() {
      SECRETS_DIR="$d" TS2OTEL_SMOKE_IMAGE='{{ tag }}' docker compose -p "$project" -f deploy/docker-compose.yaml -f deploy/docker-compose.secrets.yaml -f deploy/docker-compose.ci.yaml down --volumes --remove-orphans >/dev/null 2>&1 || true
      rm -rf "$d"
    }
    trap cleanup EXIT
    for name in oauth_client_secret grafana_cloud_token admin_token streaming_token webhook_secret pyroscope_password headscale_api_key objectstore_access_key_id objectstore_secret_access_key objectstore_session_token; do
      printf '%s' 'tskey-smoke-not-a-real-secret' > "$d/$name"
    done
    chmod 644 "$d"/*
    if ! SECRETS_DIR="$d" TS2OTEL_SMOKE_IMAGE='{{ tag }}' docker compose -p "$project" -f deploy/docker-compose.yaml -f deploy/docker-compose.secrets.yaml -f deploy/docker-compose.ci.yaml up -d; then
      echo "::error::Compose stack failed to start"
      exit 1
    fi
    for _ in $(seq 1 20); do
      health="$(docker inspect "$(docker compose -p "$project" -f deploy/docker-compose.yaml -f deploy/docker-compose.secrets.yaml -f deploy/docker-compose.ci.yaml ps -q tailscale2otel)" | jq -r '.[0].State.Health.Status')"
      if [ "$health" = healthy ]; then
        break
      fi
      sleep 1
    done
    if [ "$health" != healthy ]; then
      docker compose -p "$project" -f deploy/docker-compose.yaml -f deploy/docker-compose.secrets.yaml -f deploy/docker-compose.ci.yaml logs
      echo "::error::Compose stack did not become healthy (status: $health)"
      exit 1
    fi
    if docker compose -p "$project" -f deploy/docker-compose.yaml -f deploy/docker-compose.secrets.yaml -f deploy/docker-compose.ci.yaml logs | grep -q 'tskey-smoke-not-a-real-secret'; then
      echo "::error::the credential value appeared in Compose logs"
      exit 1
    fi
    echo "Compose stack healthy with file-backed secrets; value not echoed"

# write a root-module coverage profile to coverage.out (informational, not gating)
[group('check')]
coverage:
    go test -covermode=atomic -coverprofile=coverage.out ./...

# run CodeRabbit in directory shards; transport-incomplete shards fail closed (see docs/coderabbit-sharded-review.md)
[group('dev')]
review-sharded base="main" dirs="cmd internal deploy scripts tools":
    python3 scripts/shard_coderabbit_review.py --base '{{ base }}' --dirs {{ dirs }}

# THE GATE — everything a pull request must pass, minus the container/goreleaser legs (see `ci`)
[group('check')]
check: fmt-check lint vet test test-modules test-python tidy-check vuln gen-check docs-check helm-lint helm-gen-check config-check promql rules-check hygiene compose-check build

# `check` plus the two heavy legs CI also gates: the goreleaser matrix and the image build + smoke
[group('check')]
ci: check snapshot image smoke

# build the binary into bin/, or `go build ./...` for a named module
[group('build')]
[script('bash')]
build module="":
    set -euo pipefail
    if [ -z '{{ module }}' ] || [ '{{ module }}' = "." ]; then
      mkdir -p bin
      go build -trimpath -o bin/tailscale2otel ./cmd/tailscale2otel
      go build ./...
    else
      go build -C '{{ module }}' ./...
    fi

# build the runtime container image from deploy/Dockerfile
[group('build')]
image tag="tailscale2otel:dev":
    docker buildx build --load -f deploy/Dockerfile -t '{{ tag }}' --build-arg VERSION=dev .

# cross-compile every release target with goreleaser; no publish, no sign, no sbom
[group('build')]
snapshot:
    goreleaser release --snapshot --clean --skip=publish,sign,sbom

# remove build output that `just setup` + `just build` can reproduce
[group('build')]
clean:
    rm -rf bin dist coverage.out

# run the exporter against a local config (LONG-RUNNING; Ctrl-C to stop)
[group('dev')]
run config="config.yaml": build
    ./bin/tailscale2otel -config '{{ config }}'

# load and validate a config file without starting the exporter
[group('dev')]
validate config="config.yaml": build
    ./bin/tailscale2otel -config '{{ config }}' -validate

# report drift between the live Grafana stack and what this repo ships (read-only)
[group('infra')]
[no-exit-message]
verify-deploy:
    python3 scripts/verify_deployment.py

# verify a saved Grafana ruler read-back after a known publication boundary
[group('infra')]
verify-rule-evaluations published_at status_file:
    python3 deploy/alerts/gen/verify_evaluations.py --published-at '{{ published_at }}' --status-file '{{ status_file }}' --json

# DELETE alert/recording rules on the live Grafana stack that this repo no longer defines
[confirm('This DELETES rules on the LIVE Grafana stack. Continue?')]
[group('infra')]
prune-rules folder keep_file:
    python3 scripts/grafana-prune-rules.py --folder '{{ folder }}' --keep-file '{{ keep_file }}'

# regenerate THIRD_PARTY_NOTICES.md for the shipped binary (release artifact, never committed)
[group('release')]
notices:
    scripts/notices.sh

# write SPDX + CycloneDX SBOMs for the built binary into dist/sbom (release artifact)
[group('release')]
sbom: build
    scripts/sbom.sh

# assert every expected asset is attached to a published release
[group('release')]
[no-exit-message]
release-check version tag:
    python3 scripts/check_release_assets.py --version '{{ version }}' --tag '{{ tag }}'

# block until a release tag is fetchable from the Go module proxy and sumdb
[group('release')]
wait-for-proxy version="":
    scripts/wait-for-module-proxy.sh {{ version }}

# rewrite the Go module path to a new major version (v2+ semantic import versioning)
[confirm('This rewrites the module path across every Go file. Continue?')]
[group('release')]
bump-major major="":
    scripts/bump-module-major.sh {{ major }}
