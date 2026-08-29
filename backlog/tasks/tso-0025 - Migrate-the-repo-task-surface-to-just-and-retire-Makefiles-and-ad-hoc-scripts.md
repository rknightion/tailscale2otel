---
id: TSO-0025
title: Migrate the repo task surface to just and retire Makefiles and ad-hoc scripts
status: Done
assignee:
  - '@codex'
created_date: '2026-08-28 19:05'
updated_date: '2026-08-29 11:18'
labels:
  - 'wave:2-fleet'
dependencies: []
priority: medium
type: chore
ordinal: 28000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Fleet-wide migration of this repo's developer and CI task surface to the `just` command runner,
per the frozen standard (mandatory seven-recipe vocabulary, six groups, one self-contained
top-level `justfile`, CI steps collapse to `run: just <recipe>`).

**This repo has NO Makefile.** Nothing to delete there. The work is (a) authoring the justfile
against the repo's real five-module Go toolchain plus its Python generators, Helm chart and
Compose assets, (b) absorbing the multi-line `run: |` blocks in `.github/workflows/ci.yml` and
`helm.yml`, (c) deleting exactly one absorbed script, and (d) — the part that will actually bite —
updating `internal/ci/workflowcontract_test.go`, which asserts on the **literal text of workflow
`run:` bodies** and will go red the moment a step becomes `run: just <recipe>`.

## 1. Outcome

`just --list` is the one true answer to "what can I do in this repo". A single top-level `justfile`
holds every build, test, lint, generate and validate command, expressed against the repo's real
tooling: five Go modules (`.`, `tools/apidrift`, `tools/configcheck`, `tools/metricscatalog`,
`tools/promqlcheck`), `golangci-lint` v2 with `.golangci.yml`, `govulncheck`, three stdlib-only
Python `unittest` suites, `helm` + `yq`, `promtool`, `docker buildx` and `goreleaser`. `just check`
is the full local gate and reproduces everything `ci-success` and `helm-success` require except the
two heavy container/cross-compile legs, which live in `just ci`. Every `run:` block in `ci.yml` and
`helm.yml` that carried build/test/lint/generate logic is a one-line `just` call behind a pinned
`extractions/setup-just` step. `scripts/verify-modules.sh` is gone (absorbed). Every other script
survives with a recipe in front of it. `AGENTS.md` gains a Task interface section, `backlog/config.yml`'s
`definition_of_done` names `just` recipes, and `internal/ci/workflowcontract_test.go` still enforces
every guarantee it enforces today, now resolving through the justfile instead of matching literal
shell.

## 2. The complete justfile

Drop this in at the repo root as `justfile` (lowercase, no extension). Adjust only where §9's traps
say to verify a command's real behaviour first.

```just
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
    scripts/regen-generated.sh tools
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

# regenerate every committed generated artifact (idempotent; run before committing)
[group('gen')]
gen *targets:
    scripts/regen-generated.sh {{ targets }}

# regenerate the Grafana, alert-rule and capability-count artifacts and fail on drift
[group('check')]
[script('bash')]
gen-check:
    set -euo pipefail
    scripts/regen-generated.sh dashboards promrules counts
    if ! git diff --exit-code -- deploy/grafana deploy/alerts internal/catalog/capability_counts.json; then
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
helm-gen-check:
    set -euo pipefail
    scripts/regen-generated.sh helm
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
    ./bin/configcheck config.example.yaml /tmp/rendered-config.yaml

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

# prove the built image reads file-backed secrets and never echoes their value
[group('check')]
[script('bash')]
smoke tag="tailscale2otel:dev":
    set -euo pipefail
    d="$(mktemp -d)"
    printf '%s' 'tskey-smoke-not-a-real-secret' > "$d/oauth_client_secret"
    chmod 644 "$d"/*
    out="$(docker run --rm \
      -v "$d/oauth_client_secret:/run/secrets/oauth_client_secret:ro" \
      -e TS2OTEL_TAILSCALE__TAILNET=example.com \
      -e TS2OTEL_TAILSCALE__AUTH__METHOD=oauth \
      -e TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID=smoke \
      -e TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET_FILE=/run/secrets/oauth_client_secret \
      '{{ tag }}' -validate 2>&1)" || {
        echo "::error::-validate failed with a file-backed secret:"; echo "$out"; exit 1; }
    echo "$out"
    if grep -q 'tskey-smoke-not-a-real-secret' <<<"$out"; then
      echo "::error::the credential value appeared in -validate output"; exit 1
    fi
    if docker run --rm \
      -e TS2OTEL_TAILSCALE__TAILNET=example.com \
      -e TS2OTEL_TAILSCALE__AUTH__METHOD=oauth \
      -e TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID=smoke \
      -e TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET_FILE=/run/secrets/absent \
      '{{ tag }}' -validate >/dev/null 2>&1; then
      echo "::error::a missing *_FILE path was accepted; the file is not actually being read"
      exit 1
    fi
    echo "file-backed secret read, value not echoed, missing path rejected"

# write a root-module coverage profile to coverage.out (informational, not gating)
[group('check')]
coverage:
    go test -covermode=atomic -coverprofile=coverage.out ./...

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
```

### Deliberate deviation from §1, recorded

`check` is a **proper subset** of what `ci-success` gates: it omits `goreleaser-snapshot` and
`docker-build`'s image build + smoke. Those two legs need `goreleaser` and `docker buildx` and run
20 and 25 minutes respectively; putting them in the recipe an agent runs in a loop makes `just check`
unusable and it stops being run at all. `ci` names the delta explicitly and CI's two jobs call
`just snapshot` / `just image` + `just smoke` directly, so nothing is silently ungated. If the fleet
wants strict §1 compliance, fold `snapshot`, `image` and `smoke` into `check`'s dependency list and
delete `ci` — one line each way.

## 3. Makefile disposition

**There is no Makefile, GNUmakefile or `.mk` file anywhere in this repository** (`git ls-files |
grep -iE '(^|/)(GNUm|M)akefile|\.mk$'` returns nothing). Nothing to convert, nothing to `git rm`.
If the migrating agent finds one, it was added after this task was filed — convert it in full and
`git rm` it, per §6.

## 4. Script disposition

Twelve tracked shell scripts, eight tracked Python entry points (plus the Python generator packages
and their `test_*.py` suites, which are library code and out of scope).

| Script | Disposition | Recipe | Notes |
|---|---|---|---|
| `scripts/verify-modules.sh` (138 lines) | **ABSORB — `git rm`** | `just lint` + `just vet` + `just test` + `just test-modules` + `just tidy-check` + `just vuln`, all reachable from `just check` | Its whole body is "discover the modules, run the same six legs CI runs in each". The justfile's `modules` / `tool_modules` variables replace its `find`-based discovery; see §9 for the test that keeps that list complete. Its "loud SKIP when golangci-lint/govulncheck is absent" behaviour is deliberately NOT reproduced — a missing tool now fails the gate, which is stricter. |
| `scripts/setup.sh` | KEEP | `just setup` | `cmd/tailscale2otel/generate.go` carries `//go:generate sh ../../scripts/setup.sh`, so it must keep working with no `just` on PATH. |
| `scripts/regen-generated.sh` (300 lines) | KEEP | `just gen`, `just gen-check`, `just helm-gen-check`, and `just setup` (`tools` target) | A real program: pinned-version detection, per-target dispatch, ordered generator invocations, loud-skip semantics. `internal/ci/TestGeneratedGrafanaArtifactsAreDriftGated` also asserts it is what CI runs. |
| `scripts/check-secret-hygiene.sh` (295 lines) | KEEP | `just hygiene` | Real program: plants sentinel files, probes the Docker build context from inside the builder, EXIT-trap cleanup. |
| `scripts/ci-retry.sh` | KEEP | none (called directly from workflow `run:` blocks) | CI infrastructure, not a developer task. `internal/ci/TestCIRetryIsFailClosed` executes it directly and `TestTransientFetchesAreRetried` requires every network fetch line in a workflow to name it — see §9. |
| `scripts/notices.sh` | KEEP | `just notices` | Real program: go-licenses TSV pipeline, per-module NOTICE discovery, temp-file trap. Also invoked from release workflows as `bash scripts/notices.sh` — those call sites do NOT change (§5). |
| `scripts/sbom.sh` | KEEP | `just sbom` | Thin, but it is a release-artifact producer with env-var contract (`SYFT`, `SBOM_TARGET`, `OUT_DIR`, `SBOM_NAME`) documented in its header; wrapping it keeps that contract. |
| `scripts/wait-for-module-proxy.sh` | KEEP | `just wait-for-proxy` | Polling loop with timeout/backoff. Invoked from `auto-rc.yml` and `release-please.yml` inside a `pre-cmd:` **input to a reusable workflow** — that call site cannot use `just` and must not change (§5). |
| `scripts/bump-module-major.sh` (118 lines) | KEEP | `just bump-major` (`[confirm]`) | Real program: major inference from the release-please manifest, repo-wide path rewrite. |
| `scripts/cloud-environment-setup.sh` (163 lines) | KEEP, **no recipe** | none | Runs as the Codex/Claude cloud-agent environment setup command on a machine that has no repo checkout state and no `just`. Header explicitly says local agents must not execute it. Out of scope entirely. |
| `deploy/helm/tests/render-tests.sh` (1624 lines) | KEEP | `just helm-lint` | Shell test suite (§6). |
| `deploy/tests/compose-tests.sh` (300 lines) | KEEP | `just compose-check` | Shell test suite with a `--self-test` mode (§6). |
| `.githooks/pre-commit` | KEEP, **no recipe** | none | Shipped git hook executed by git, not by a developer. It calls `scripts/regen-generated.sh` directly and must keep doing so. |
| `scripts/check_doc_commands.py` | KEEP | `just docs-check` | Real program (~450 lines); extracts and validates documented helm/env commands. |
| `scripts/check-capability-counts.py` | KEEP | `just gen-check` (checks) and `scripts/regen-generated.sh counts` (writes) | Real program. |
| `scripts/check_release_assets.py` | KEEP | `just release-check` | Real program; called from `release-please.yml` — that call site does NOT change (§5). |
| `scripts/review_changelog.py` | KEEP, **no recipe** | none | Scheduled-lane program invoked only by `changelog-review.yml`; not a developer task. |
| `scripts/grafana-prune-rules.py` | KEEP | `just prune-rules` (`[confirm]`) | Real program that mutates a live Grafana stack; also called from `grafana-sync.yml` — that call site does NOT change (§5). |
| `scripts/verify_deployment.py` | KEEP | `just verify-deploy` | Real program, read-only drift report against a live stack. |
| `scripts/check_doc_security_claims.py` | KEEP, **no recipe** | none | Invoked by `clientlib-main.yml`/docs lanes only; leave alone. If it turns out to be unreferenced, that is a separate cleanup, not this task. |
| `deploy/grafana/gen/*.py`, `deploy/alerts/gen/*.py` | KEEP | `just gen`, `just test-python` | Generator packages and their unittest suites. |

**Exactly one `git rm`: `scripts/verify-modules.sh`.** Do it last (§8), and only after grepping the
repo for references — `AGENTS.md:297`, and several `docs/superpowers/plans/*.md` files, which are
untracked (`git ls-files docs/superpowers` is empty) and must be left alone.

## 5. CI changes

### The setup-just step (exact YAML)

Insert immediately after the `actions/checkout` step and before the first `run: just …` in every job
that gains one. Resolve the current `extractions/setup-just` v4 commit SHA with
`gh api repos/extractions/setup-just/git/ref/tags/v4 -q .object.sha` and pin it, matching the fleet
convention already used throughout these workflows.

```yaml
      - uses: extractions/setup-just@<pinned-sha> # v4
        with:
          just-version: '1.58.0'
```

### `.github/workflows/ci.yml`

| Job | Step (current) | Becomes |
|---|---|---|
| `build-test` | `- name: go vet` / `run: go vet ./...` | `run: just vet .` |
| `build-test` | `- name: go build` / `run: go build ./...` | `run: just build .` |
| `build-test` | `- name: go test -race` / `run: go test -race ./...` | `run: just test` |
| `build-test` | `- name: go mod tidy leaves no diff` (7-line `run: \|`) | `run: just tidy-check .` |
| `lint` (matrix) | `uses: golangci/golangci-lint-action@…` | **UNCHANGED** — never convert a `uses:` (§8) |
| `module-verify` (matrix) | `install govulncheck (pinned)` `run:` | **UNCHANGED** (tool install) |
| `module-verify` | `go build` + `go vet` + `go test -race` steps (each with `working-directory:`) | one step: `run: just test-modules ${{ matrix.module }}` and one: `run: just vet ${{ matrix.module }}`; drop the per-step `working-directory:` |
| `module-verify` | `go mod tidy leaves no diff` (7-line `run: \|`) | `run: just tidy-check ${{ matrix.module }}`; drop `working-directory:` |
| `module-verify` | `govulncheck` | `run: just vuln ${{ matrix.module }}`; drop `working-directory:` |
| `docs-catalog` | `Build metricscatalog` + `docs/metrics.md is in sync` + `documented install commands reference real values` (3 steps) | one step: `run: just docs-check` |
| `dashboards-drift` | `generator unit tests` (5-line `run: \|` loop) | `run: just test-python` |
| `dashboards-drift` | `regenerate the generated dashboard and rule artifacts` + `fail on diff` + `check public capability-count summaries` (3 steps) | one step: `run: just gen-check` |
| `dashboards-drift` | `install promtool (pinned)` (`run: \|` with `scripts/ci-retry.sh curl …`) | **UNCHANGED** — tool install, and `TestTransientFetchesAreRetried` matches those literal lines |
| `dashboards-drift` | `promqlcheck (dashboard + rule expressions)` | `run: just promql` |
| `dashboards-drift` | `promtool check rules` + `promtool test rules` (2 steps) | one step: `run: just rules-check` |
| `govulncheck` | `install govulncheck (pinned)` | **UNCHANGED** |
| `govulncheck` | `govulncheck ./...` | `run: just vuln .` |
| `goreleaser-snapshot` | `uses: goreleaser/goreleaser-action@…` | **UNCHANGED** — a `uses:`, and it carries `distribution`/`version`/`args` inputs |
| `docker-build` | `repository secret-hygiene gate` / `run: scripts/check-secret-hygiene.sh` | `run: just hygiene` (keep `env: HYGIENE_REQUIRE_DOCKER: "1"`) |
| `docker-build` | `Compose assets resolve…` / `run: deploy/tests/compose-tests.sh --self-test` | `run: just compose-check` |
| `docker-build` | `Restore Go build cache` / `Inject Go build cache` / `build image (no push)` | **UNCHANGED** — caches and a `uses:` |
| `docker-build` | `file-based secrets are read by the real image` (30-line `run: \|`) | `run: just smoke tailscale2otel:ci` |
| `coverage` | `go test (coverage profile)` | `run: just coverage` |
| `coverage` | `Upload coverage to Codacy` | **UNCHANGED** — a `uses:` |
| `fuzz` (matrix) | the 25-line `run: \|` classifier | **UNCHANGED** — see §9; it reads `$GITHUB_OUTPUT`-adjacent state, writes `::warning::` annotations and is pinned literally by `internal/ci` fuzz-lane tests |
| `ci-success` | everything | **UNCHANGED** |

`setup-just` is needed in: `build-test`, `module-verify`, `docs-catalog`, `dashboards-drift`,
`govulncheck`, `docker-build`, `coverage`. NOT in `lint`, `goreleaser-snapshot`, `fuzz`, `ci-success`.

### `.github/workflows/helm.yml`

| Job | Step (current) | Becomes |
|---|---|---|
| `lint-template` | `helm lint` + `helm template` + `chart render contracts` (3 steps) | one step: `run: just helm-lint` |
| `lint-template` | `values.schema.json is in sync (losisin/helm-values-schema-json)` | **UNCHANGED** — a `uses:` with `fail-on-diff: true` |
| `lint-template` | `helm-docs is in sync (norwoodj/helm-docs)` | **UNCHANGED** — a `uses:` with `fail-on-diff: true` |
| `configcheck` | `Build configcheck` + `Render Helm config` + `Validate example + rendered config` (3 steps) | one step: `run: just config-check` |
| `helm-success` | everything | **UNCHANGED** |

`setup-just` goes in both `lint-template` and `configcheck`, after `azure/setup-helm`.
`env: CHART_DIR: deploy/helm/tailscale2otel` at workflow level becomes unused by the converted
steps — leave it in place; the `uses:` steps still interpolate `${{ env.CHART_DIR }}`.

### What must NOT change, anywhere

- **`ci-success`** (`ci.yml`) and **`helm-success`** (`helm.yml`) — the job names, the `if: always()`
  guard, and the exact `needs:` lists. Branch protection on `main` requires these two check names by
  string; a rename stalls every bot PR.
- Every `permissions:` block, every `concurrency:` group, every `timeout-minutes:`
  (`TestEveryBoundableJobHasATimeout` enforces the last one).
- Every `persist-credentials: false` on `actions/checkout`.
- Every SHA-pinned `uses:` and its `# vN` comment. **Never convert a `uses:` into `run: just`.**
- The reusable calls: `rknightion/.github/.github/workflows/container-publish.yml` (publish.yml),
  the binaries reusable in `release-please.yml` / `auto-rc.yml` and their `pre-cmd:` strings
  (`bash scripts/wait-for-module-proxy.sh … && … && bash scripts/notices.sh`), and the local
  composite actions `./.github/actions/report-drift` / `./.github/actions/resolve-drift`.
- Every matrix definition, including `lint`'s five-module matrix, `module-verify`'s four-module
  matrix and `fuzz`'s nine-target `include:` list.
- The `if: github.event_name == 'push'` guards on `coverage` and `fuzz`
  (`TestNonGatingCILanesDoNotRunOnPullRequests`).
- **These workflows are not touched at all:** `release-please.yml`, `publish.yml`, `auto-rc.yml`,
  `codeql.yml`, `zizmor.yml`, `actionlint.yml`, `scorecard.yml`, `dependency-review.yml`,
  `docker-security.yml`, `ghcr-cleanup.yml`, `trigger-docs-sync.yml`, `arm-automerge.yml`,
  `api-drift.yml`, `changelog-review.yml`, `iana-freshness.yml`, `clientlib-main.yml`,
  `live-contract.yml`, `fuzz-scheduled.yml`, `grafana-sync.yml`. They are GitHub-native, scheduled
  advisory lanes, or release plumbing; several are pinned literally by `internal/ci` tests.

## 6. Docs and agent-contract changes

### `AGENTS.md` (`CLAUDE.md` just `@AGENTS.md`-includes it — do not fork content back into it)

- **`AGENTS.md:9-18`** — the fenced `sh` Commands block. Replace the raw `go`/`golangci-lint` lines
  with the Task interface section below, keeping the `./tailscale2otel -config config.yaml` line's
  meaning as `just run`.
- **`AGENTS.md:19`** — "`govulncheck` is a CI gate: `go install … && govulncheck ./...`" → point at
  `just vuln` and note `just setup` installs the pinned binary.
- **`AGENTS.md:35-42`** — the `scripts/regen-generated.sh` block → `just gen` (and `just gen tools`,
  `just gen helm`, `just gen dashboards promrules counts`). Keep the prose explaining the pins.
- **`AGENTS.md:54`, `:56`, `:61`, `:88`, `:93-94`** — `scripts/regen-generated.sh …` and
  `scripts/check-capability-counts.py --write` references → `just gen …`. Keep the underlying tool
  names in the table (they are the source of truth for the pins).
- **`AGENTS.md:72`** — "run `go generate ./...` (or `scripts/setup.sh`)" → add `just setup` as the
  first-listed option; keep both existing ones (they still work, deliberately).
- **`AGENTS.md:168-169`** — "After every change run `go build ./... && go vet ./... && go test -race
  ./...` and keep `golangci-lint run` clean" → "After every change run `just check`".
- **`AGENTS.md:284`** — the "Root module: `go vet` · `go build` · …" gate summary → name the recipes.
- **`AGENTS.md:297`** — "**Run `scripts/verify-modules.sh` locally**" → "**Run `just check`
  locally**", and delete the sentence about it skipping when golangci-lint/govulncheck are absent
  (the recipes fail instead).
- **`AGENTS.md:394`** — `python3 scripts/verify_deployment.py` → `just verify-deploy`.
- **`AGENTS.md:419`** — `scripts/bump-module-major.sh` → `just bump-major`.

Add this section verbatim near the top of `AGENTS.md`, replacing the current `## Commands` block.
**Do not paste the recipe list into it** (§9 of the standard):

```markdown
## Task interface

This repo's task surface is a `justfile`. Discover it, don't guess it:

    just --list                        # human-readable
    just --dump --dump-format json     # machine-readable
    just --show <recipe>               # what a recipe actually runs

- `just check` is the full gate and is what CI enforces. It must pass before you commit.
  `just ci` adds the two heavy legs (`goreleaser` cross-compile, container image + smoke)
  that `ci.yml` also gates.
- Prefer `just <recipe>` over the underlying tool. If you are typing `go test`, you want `just test`.
- Run `just` with stdin from /dev/null. Recipes marked `[confirm]` are destructive — stop and ask
  before running one; never pass `--yes` or `JUST_YES=1`.
- If a task you need does not exist, add a recipe with a `#` doc comment and a `[group(...)]`
  rather than running a bare command.
- `just setup` once per clone: it installs the pinned `golangci-lint`, `govulncheck` and the two
  Helm generators, and wires `core.hooksPath` at `.githooks`.
```

### `README.md`

- **`README.md:253`** — "run `scripts/regen-generated.sh` before committing changes that touch them"
  → "run `just gen` before committing changes that touch them".

### `docs/`

- **`docs/alerts.md:51`** — "alongside it with `scripts/regen-generated.sh promrules`" → `just gen promrules`.
- **`docs/alerts.md:181`** — the fenced `scripts/regen-generated.sh promrules` line → `just gen promrules`.
- **`docs/alert-profiles.md:10`** — this file is **GENERATED** by
  `deploy/alerts/gen/build_rules.py --docs-out`. Do NOT hand-edit it: change the string in
  `build_rules.py`, then `just gen dashboards`.
- **`docs/env-vars.md:27`** — also **GENERATED** (between markers). Change the source string in
  `internal/config`'s `TestEnvReferenceDocInSync` writer, then `just gen envref`.
- **`docs/installation.md:255`** — `scripts/check-secret-hygiene.sh` → mention `just hygiene` as the
  way to run it; keep the script path, the prose is describing what the gate *is*.
- **`docs/installation.md:285`** — `scripts/check_doc_commands.py` → same treatment (`just docs-check`).
- **`docs/installation.md:248`** — `scripts/notices.*` reference is about shipped release artifacts;
  leave it.
- **`deploy/CLAUDE.md`** — every `scripts/regen-generated.sh dashboards` mention → `just gen dashboards`.
  Grep it: `grep -n 'regen-generated' deploy/CLAUDE.md`.
- **`docs/superpowers/**`** is UNTRACKED (gitignored). Do not touch it; its many
  `scripts/regen-generated.sh` / `scripts/verify-modules.sh` hits are historical plan scratch.

After any docs edit, re-run `just docs-check` — `scripts/check_doc_commands.py` parses fenced
`helm` commands and `TS2OTEL_*` names out of `README.md` and `docs/*.md`, and it will fail on a
broken edit. It does not parse `just`, so `just` lines are inert to it.

## 7. `backlog/config.yml`

Current (`backlog/config.yml:5-8`):

```yaml
definition_of_done:
  - "go build ./... && go vet ./... && go test -race ./..."
  - "golangci-lint run"
  - "scripts/regen-generated.sh (only if a generated artifact's inputs changed)"
```

Replace with, via `backlog config set` or the Backlog CLI's config editor — **never hand-edit
files under `backlog/`**:

```yaml
definition_of_done:
  - "just check passes (the full gate; it is what CI enforces)"
  - "just gen leaves no diff (only if a generated artifact's inputs changed)"
  - "just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]"
```

## 8. Order of work

Every step ends with the repo green. Do not reorder; deletions are last on purpose.

1. **Write `justfile`** at the repo root. Nothing else changes yet — no workflow, no doc, no
   deletion. `.gitignore` already covers `/bin/`, `*.out` and `dist`, so `build`, `coverage` and
   `snapshot` produce no untracked churn; confirm with `git status` after the first run.
2. **Prove the vocabulary loads:** `just --list`, `just --groups`, `just --dump --dump-format json
   > /dev/null`, `just --fmt --check`. Any non-zero here means an unstable feature crept in — §5.8
   makes *the whole file* unlistable, which blinds every agent. Fix before continuing.
3. **Prove each leaf recipe locally, individually,** against the current tree (which is green):
   `just fmt-check`, `just lint`, `just vet`, `just test`, `just test-modules`, `just test-python`,
   `just tidy-check`, `just vuln`, `just gen-check`, `just docs-check`, `just helm-lint`,
   `just helm-gen-check`, `just config-check`, `just promql`, `just rules-check`, `just hygiene`,
   `just compose-check`, `just build`. Then `just check` end to end, then `just check` a **second**
   time to prove idempotence and that nothing left the tree dirty (`git status --porcelain` empty).
4. **Update `internal/ci/workflowcontract_test.go` FIRST, before touching any workflow.** Add a
   justfile-resolving helper and rewrite the affected assertions (§9). Land it with the workflows
   unchanged and confirm `just test` is still green — the helper must pass against both the current
   literal `run:` bodies and the converted ones during the transition, or write it to accept either
   form for one commit.
5. **Convert `ci.yml`,** job by job, inserting `setup-just` where §5 says. Run `actionlint` locally
   after each job (`actionlint .github/workflows/ci.yml`), and re-run `just test` — the `internal/ci`
   contract tests are the real gate here, not actionlint.
6. **Convert `helm.yml`** the same way.
7. **Push and watch a real CI run.** `ci-success` and `helm-success` must both go green. This is the
   proof; a local `just check` does not exercise `setup-just`, the runner's `yq`, or `promtool`.
8. **Docs and agent contract** (§6) — `AGENTS.md`, `README.md`, `docs/alerts.md`,
   `docs/installation.md`, `deploy/CLAUDE.md`. Re-run `just docs-check`.
9. **`backlog/config.yml`** (§7), through the Backlog CLI.
10. **Deletions, last.** `grep -rn 'verify-modules' --exclude-dir=.git --exclude-dir=docs/superpowers .`
    must return nothing outside the deleted file itself, then `git rm scripts/verify-modules.sh`.
    Re-run `just check` and push.

## 9. Traps specific to this repo

1. **`internal/ci/workflowcontract_test.go` asserts on the literal text of workflow `run:` bodies.**
   This is the single largest hazard. These will fail the moment a step becomes `run: just <recipe>`:
   - `TestSchemaDrivenDecodeTestsRideAGatedLeg` (`:251`) — a step in `build-test` whose `run` contains
     the substring `go test -race ./...`.
   - `TestModuleVerifyRunsTheFullPerModuleGate` (`:416`) — `module-verify`'s concatenated `run:`
     bodies must contain lines *beginning with* `go build ./...`, `go vet ./...`,
     `go test -race ./...`, `go mod tidy`, `govulncheck ./...`. The test deliberately matches
     line-prefixes rather than substrings, precisely to defeat "the command name appears in an error
     message".
   - `TestRootModuleTidyIsChecked` (`:472`) — a line in `build-test` beginning with `go mod tidy`.
   - `TestGeneratedGrafanaArtifactsAreDriftGated` (`:510`) — `dashboards-drift`'s body must contain
     `regen-generated.sh dashboards`, `git diff --exit-code`, `deploy/grafana`, `deploy/alerts`.
   - `TestPrometheusRulesAreCheckedByPromtool` (`:567`) — `promtool check rules
     deploy/alerts/prometheus/tailscale2otel.rules.yaml` and `promtool test rules`.
   - `TestDashboardsDriftRunsPromqlcheck` (`:623`).
   - `TestPythonGeneratorTestsRunInCI` (`:673`) — `python3 -m unittest discover`, and a scoped check
     that `scripts` is among the discovered directories.
   - `TestDocumentedInstallCommandsAreChecked` (`:1434`) — `docs-catalog` runs
     `scripts/check_doc_commands.py`.

   **The fix, and it must preserve the guarantee, not delete it:** add an unexported helper to
   `internal/ci` that reads the top-level `justfile` as text and returns a recipe's body lines plus,
   transitively, its dependencies' body lines. Then rewrite each assertion as two halves: (a) the
   workflow step invokes `just <recipe>`, and (b) that recipe transitively invokes the command the
   test cares about. Parse the justfile with a small text scanner (recipe header = a line at column
   0 matching `^([a-z0-9-]+)([^:]*):(.*)$` with the dependency list after the colon; body = the
   following indented lines; `[attribute]` lines immediately above the header) rather than shelling
   out to `just --dump` — the tests run in `go test -race ./...` on machines and CI jobs that may
   not have `just` on PATH, and a test that skips when a tool is missing is how a gate stops meaning
   anything. Stdlib only, matching every other test in that package.

   Do **not** weaken an assertion to "the step mentions just". A test that passes for any justfile
   is worse than no test.

2. **`TestTransientFetchesAreRetried` (`:1160`) scans every workflow `run:` body for network fetch
   lines and requires `ci-retry.sh` on the same line.** If you move the `promtool` install (or the
   `api-drift` / `iana-freshness` curls) into a recipe, the assertion finds no fetch lines and passes
   **vacuously** — the guarantee silently evaporates. **Leave every `curl`/`wget` line in the
   workflows.** Tool installation is CI infrastructure, not build logic; §8 of the standard is about
   the *shell in a step*, and a `sudo install -m 0755 … /usr/local/bin/promtool` is not a developer
   task.

3. **`TestCIRetryIsFailClosed` (`:1085`) executes `scripts/ci-retry.sh` from the test.** The script
   must stay at that exact path. It gets no recipe.

4. **No `go.work`, on purpose.** `go build ./...`, `go vet ./...`, `go test -race ./...` and
   `govulncheck ./...` from the repo root all stop at the root module boundary and reach **none** of
   `tools/apidrift`, `tools/configcheck`, `tools/metricscatalog`, `tools/promqlcheck`. Every recipe
   that means "all modules" must loop. Do not "simplify" by adding a `go.work` — it would change what
   the root module's `go build ./...` resolves and is a deliberate design decision (`AGENTS.md:289`).

5. **`modules` / `tool_modules` are hardcoded, replacing `verify-modules.sh`'s `find`-based
   discovery.** That discovery existed so a fifth module is covered the day it lands. Restore the
   property: add a test beside `TestEveryGoModuleIsCoveredByCIVerification` (`:387`) asserting every
   directory containing a `go.mod` (excluding `.git`, `.capture`, `.claude`, `dist`) appears in the
   justfile's `modules :=` line. **`.claude/` must stay excluded** — it holds agent git worktrees,
   each a full checkout of this repository; without the exclusion every leg runs once per worktree
   and another checkout's in-flight edits get reported as this one's failures.

6. **`tools/metricscatalog` and `tools/configcheck` cannot be `go run` from the root.**
   `go run ./tools/metricscatalog` fails with "main module does not contain package" despite the
   tool's own help text. Use `go run -C tools/metricscatalog .` or `go build -C … -o "$PWD/bin/…" .`
   — and with `-C`, every other path argument is relative to the *new* directory, so `-o` and
   `-file` must be absolute. The `docs-check` and `config-check` recipes above do this correctly;
   do not "tidy" the `$PWD/` prefixes away.

7. **The two Helm generator tools are version-pinned and their output differs by version.**
   `helm-docs` **v1.14.2** must be installed with `-ldflags "-X main.version=1.14.2"` or it reports
   no version, silently drops the README's version footer, and produces a plausible-but-wrong file.
   `helm-values-schema-json` **v2.5.0** (note: the *action* is v3.1.0; the tool version it installs
   is different and is baked into the action's `dist/index.js`). `just setup` delegates to
   `scripts/regen-generated.sh tools`, which handles both correctly — never hand-install them and
   never inline the `go install` lines into the justfile.

8. **`golangci-lint` version skew.** CI's action pins **v2.13.2**;
   `scripts/cloud-environment-setup.sh:15` pins **2.13.1**. The justfile's `golangci_version` must
   track the CI action. Bump `cloud-environment-setup.sh` to match in this task, or note the
   divergence — a lint finding that only appears in CI is exactly the class of failure `just check`
   exists to prevent.

9. **Verify `golangci-lint fmt --diff` actually exits non-zero on unformatted input** before relying
   on it in `fmt-check` (`printf 'package main\nfunc  x(){}\n' > /tmp/x/main.go` in a scratch module,
   then check `$?`). If it exits 0, drop those lines and rely on `lint`: `.golangci.yml`'s
   `formatters:` block (gofmt + goimports) already fails `golangci-lint run` on unformatted code,
   which is why there is no separate gofmt step in CI. `fmt-check` then reduces to
   `just --fmt --check`, which is still §1-compliant.

10. **`go test -run ''` matches every test** — that is what makes the `test filter=""` default
    correct. Do not "fix" it to a conditional; and keep the `'{{ filter }}'` quoting, or
    `just test 'Foo Bar'` splits into two arguments (§10).

11. **`[script('bash')]` overrides `set shell`.** Every scripted recipe above starts with
    `set -euo pipefail` for that reason. Drop that line from one and a mid-recipe failure stops
    failing the recipe — a silently green gate.

12. **`gen-check` and `tidy-check` mutate the working tree when it is already drifted.** That is
    intentional and mirrors CI exactly: they regenerate/tidy, then fail on the diff. On a clean tree
    both are no-ops, which is what makes them safe inside `check`. Say so in the doc comments; do not
    "fix" them into read-only checks, because then nothing produces the corrected artifact.

13. **`deploy/grafana/gen/build.py` must run before `deploy/alerts/gen/build_rules.py`** — the rule
    builder resolves each alert's canonical panel *by title* against the generated dashboard and
    hard-fails on zero or multiple matches. `scripts/regen-generated.sh` already enforces the order;
    that is one more reason `gen` wraps the script instead of inlining the two `python3` calls.

14. **`signal_dispositions.json` is NOT regenerated by `just gen`.** `regen-generated.sh coverage`
    rebuilds only the *page*. The manifest itself is a deliberate hand-run
    (`go test ./internal/catalog -run TestSignalDispositionsInSync -update`) and an empty disposition
    always fails the gate by design. Do not add a recipe that regenerates it as part of `gen` or
    `check` — that would let an agent make a red gate green without giving the signal a panel.

15. **`promtool`, `yq`, `helm`, `syft`, `go-licenses`, `goreleaser`, `gcx` are not on PATH by
    default.** `promtool` comes from a pinned release tarball in CI (never `go install` — the
    prometheus module carries `replace` directives that `go install` refuses); `yq` is preinstalled
    on `ubuntu-latest` runners. Locally, `just rules-check`, `just helm-lint`, `just config-check`,
    `just sbom`, `just notices`, `just snapshot` and `just verify-deploy` will fail loudly on a
    missing binary. Consider adding `require('promtool')` / `require('yq')` guards to those recipes
    (`require` **is** stable in 1.58, contrary to some online material) so the error names the
    missing tool rather than surfacing as `command not found`.

16. **The `fuzz` job's 25-line classifier must stay in the workflow.** It distinguishes a real
    crasher (`Failing input written to`) from Go's benign `-fuzztime` shutdown race, and emits
    `::error::`/`::warning::` workflow annotations. It is CI-specific behaviour with no local
    meaning, and `TestEveryFuzzTargetRunsInBothMatrices` / `TestScheduledFuzzLaneIsAdvisoryOnly`
    read that lane.

17. **`actionlint` CI can fail on a workflow edit that is clean locally.** actionlint shells out to
    whatever `shellcheck` is on PATH; local shellcheck 0.11.0 does not emit SC2015 while the
    runner's older one does. When you edit a `run:` block, prefer a plain `if` over
    `A && B || C` rather than trusting a local actionlint run (`AGENTS.md:21-30`). Converting steps
    to one-line `run: just …` removes shell from most steps, which makes this *less* likely — but
    the surviving multi-line blocks (promtool install, fuzz) still carry the risk.

18. **`config-check` writes `/tmp/rendered-config.yaml`.** That is fine on Linux and macOS. If the
    fleet ever wants Windows, switch it to `mktemp`; not in scope now.

## 10. Out of scope

Do not touch any of the following.

**Scripts that survive with no recipe:** `scripts/cloud-environment-setup.sh` (cloud-agent
environment bootstrap; runs where there is no `just`), `scripts/ci-retry.sh` (CI infrastructure,
pinned by `TestCIRetryIsFailClosed` and `TestTransientFetchesAreRetried`),
`scripts/review_changelog.py` (scheduled lane only), `scripts/check_doc_security_claims.py`,
`.githooks/pre-commit` (executed by git, calls `regen-generated.sh` directly and must keep doing so).

**Scripts that survive WITH a recipe — the recipe wraps them, it does not replace them:**
`scripts/setup.sh`, `scripts/regen-generated.sh`, `scripts/check-secret-hygiene.sh`,
`scripts/notices.sh`, `scripts/sbom.sh`, `scripts/wait-for-module-proxy.sh`,
`scripts/bump-module-major.sh`, `scripts/check_doc_commands.py`,
`scripts/check-capability-counts.py`, `scripts/check_release_assets.py`,
`scripts/grafana-prune-rules.py`, `scripts/verify_deployment.py`,
`deploy/helm/tests/render-tests.sh`, `deploy/tests/compose-tests.sh`, and everything under
`deploy/grafana/gen/` and `deploy/alerts/gen/`.

**Workflows — no edits at all:** `release-please.yml`, `publish.yml`, `auto-rc.yml`, `codeql.yml`,
`zizmor.yml`, `actionlint.yml`, `scorecard.yml`, `dependency-review.yml`, `docker-security.yml`,
`ghcr-cleanup.yml`, `trigger-docs-sync.yml`, `arm-automerge.yml`, `api-drift.yml`,
`changelog-review.yml`, `iana-freshness.yml`, `clientlib-main.yml`, `live-contract.yml`,
`fuzz-scheduled.yml`, `grafana-sync.yml`. Only `ci.yml` and `helm.yml` change.

**Also out of scope:** adding a `go.work`; changing `.golangci.yml`, `.goreleaser.yaml`,
`renovate.json`, `.codacy.yaml`, `release-please-config.json` or `.release-please-manifest.json`;
changing any generated artifact by hand (`docs/metrics.md`, `docs/env-vars.md`,
`docs/signal-coverage.md`, `docs/alert-profiles.md`, the chart `README.md`, `values.schema.json`,
`deploy/grafana/*.json`, `deploy/alerts/grafana-managed/*`,
`deploy/alerts/prometheus/tailscale2otel.rules.yaml`, `internal/catalog/capability_counts.json`);
anything under `docs/superpowers/` (untracked planning scratch); the `.claude/` worktrees;
`archive/`; and any change to what the recipes actually run — this migration moves commands, it does
not change them.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A top-level `justfile` exists and `just --list` shows all seven mandatory recipes (default, setup, fmt, fmt-check, lint, test, check), each public recipe carrying a `#` doc comment and — apart from default and setup — a `[group(...)]` drawn only from check/build/dev/gen/infra/release; `just --groups` lists no other group.
- [x] #2 `just check` passes on a clean checkout and a second consecutive run leaves `git status --porcelain` empty (idempotent, re-runnable, does not dirty the tree).
- [x] #3 `just --fmt --check` exits 0 and `just --dump --dump-format json` exits 0 — no unstable feature is used, so `--list` and `--dump` stay usable by agents.
- [x] #4 `just check` reproduces every leg ci-success and helm-success gate except the goreleaser and container legs (which `just ci` adds): golangci-lint, go vet, go test -race and go mod tidy diffs across all five Go modules (., tools/apidrift, tools/configcheck, tools/metricscatalog, tools/promqlcheck), the three python -m unittest suites, regen-generated.sh dashboards promrules counts + git diff --exit-code, metricscatalog -check, check_doc_commands.py, helm lint/template/render-tests.sh, configcheck against config.example.yaml and the rendered chart config, promqlcheck, promtool check rules + test rules, check-secret-hygiene.sh and compose-tests.sh --self-test.
- [x] #5 Only .github/workflows/ci.yml and helm.yml change; their build-test, module-verify, docs-catalog, dashboards-drift, govulncheck, docker-build, coverage, lint-template and configcheck jobs each carry a SHA-pinned `extractions/setup-just` step with `just-version: '1.58.0'` and call `just <recipe>`, while the `ci-success` and `helm-success` job names and `needs:` lists, all `permissions:`, `concurrency:`, `timeout-minutes:`, `persist-credentials: false`, every `uses:` and every matrix are unchanged, and every scripts/ci-retry.sh network-fetch line stays literal in the workflow.
- [x] #6 internal/ci/workflowcontract_test.go resolves workflow steps through the justfile (a stdlib recipe-body reader with transitive dependency expansion, no shelling out to `just`) and `go test ./internal/ci` is green, with TestSchemaDrivenDecodeTestsRideAGatedLeg, TestModuleVerifyRunsTheFullPerModuleGate, TestRootModuleTidyIsChecked, TestGeneratedGrafanaArtifactsAreDriftGated, TestPrometheusRulesAreCheckedByPromtool, TestDashboardsDriftRunsPromqlcheck, TestPythonGeneratorTestsRunInCI and TestDocumentedInstallCommandsAreChecked each still failing when the underlying command is removed from the recipe.
- [x] #7 A test beside TestEveryGoModuleIsCoveredByCIVerification asserts every directory holding a go.mod (excluding .git, .capture, .claude, dist) appears in the justfile's `modules :=` line, so a fifth module cannot be added and silently skipped now that verify-modules.sh's find-based discovery is gone.
- [x] #8 `scripts/verify-modules.sh` is deleted and `git grep -n 'verify-modules' -- ':!backlog' ':!archive' ':!docs/superpowers'` returns nothing, so no active tracked product, source, script, workflow or documentation surface still references it; every KEEP script still exists and is reachable via a recipe — setup.sh (just setup), regen-generated.sh (just gen / gen-check / helm-gen-check), check-secret-hygiene.sh (just hygiene), notices.sh (just notices), sbom.sh (just sbom), wait-for-module-proxy.sh (just wait-for-proxy), bump-module-major.sh (just bump-major), render-tests.sh (just helm-lint), compose-tests.sh (just compose-check), check_doc_commands.py (just docs-check), verify_deployment.py (just verify-deploy), grafana-prune-rules.py (just prune-rules), check_release_assets.py (just release-check) — and ci-retry.sh, cloud-environment-setup.sh, review_changelog.py and .githooks/pre-commit remain deliberately recipe-less.
- [x] #9 AGENTS.md carries the Task interface section naming `just check` as the gate, no longer instructs anyone to run raw `go build ./... && go vet ./... && go test -race ./...`, `golangci-lint run`, `scripts/regen-generated.sh` or `scripts/verify-modules.sh`, and does not paste the recipe list; README.md:253, docs/alerts.md and deploy/CLAUDE.md name `just gen` instead of `scripts/regen-generated.sh`; `just docs-check` still passes after the edits.
- [x] #10 backlog/config.yml's definition_of_done names `just check` and `just gen` (and the justfile authoring rule) instead of raw go/golangci-lint commands and scripts/regen-generated.sh, and was changed through the backlog CLI rather than by hand-editing the file.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 go build ./... && go vet ./... && go test -race ./...
- [x] #2 golangci-lint run
- [x] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Run started from the commissioned 2026-08-28 wave2 goal. Preflight found the clean checkout at 62edf04 while origin/main had advanced to f55db60 through two unrelated dependency/action updates; main was fast-forwarded before implementation. The ordered plan in §8 remains the plan of record.

Implementation and all in-scope verification are complete. Authored commits: e81471a (justfile), aa35103 (workflow-contract resolver and module coverage), 4180c7a (CI and Helm conversion), dd3c458 (documentation), c8cbf11 (Backlog Definition of Done), 495611c (retired verifier deletion). Two consecutive exact-head just check runs passed and the second left git status --porcelain empty; just --fmt --check, JSON dump parsing, actionlint, docs-check, all eight required negative tests, and CodeRabbit gates passed. Hosted checkpoint 4180c7ab365b986697342f0106cae5ec1d276b59 passed CI run 33244634311 including ci-success and Helm run 33244634292 including helm-success.

Park boundary: AC #8 cannot be checked literally. scripts/verify-modules.sh is deleted, every KEEP script and required recipe was verified, and the active source/product sweep is empty. However the commissioned grep necessarily finds the term in committed historical archives, older completed tasks, backlog/docs, codex goal/report history, TSO-0025 itself, and untracked .claude worktrees. Those files are outside this task and the authorized write set. The final exact-head CI run is also subject to main concurrency cancellation; the run-end report records the terminal hosted state after this tracker commit.
<!-- SECTION:NOTES:END -->

## Comments

<!-- COMMENTS:BEGIN -->
author: campaign-ordering
created: 2026-08-29 09:18
---
## Fleet ordering — WAVE 2. Starts after the Wave 0 pilot (`sf2loki` / SFL-0073) and the Wave 1 hubs land.

Within Wave 2 the order is free — these repos do not depend on each other. Batching by language is worthwhile so one lane reuses its Makefile-to-recipe mapping across similar repos.

Do not start before the pilot reports. The standard may be amended off the back of it, and picking this up early risks coding against a superseded seam.

**Provisioning `just` in CI.** Which mechanism depends on the runner, and the two must not be mixed:

| Runner | Mechanism |
| --- | --- |
| `arc-arm64` (m7kni self-hosted) | `just` is **baked into the runner image** by `m7kni/ci-tools` (`runner-image/Dockerfile`, `ARG JUST_VERSION`). Do **not** add `extractions/setup-just`, and delete the step if this repo already has one — it installs a second `just` earlier on `PATH` and turns the image pin into a lie. |
| GitHub-hosted (all `rknightion` repos) | `extractions/setup-just`, SHA-pinned, with an explicit `just-version:`. |

Both sides currently sit on **1.58.0** and are Renovate-managed. `ci-tools`' `Tool version drift` workflow fails if the Dockerfile `ARG` and the published image ever disagree, and lists any repo still carrying a second pin.

**While you are in the workflow files, check the hub pin.** On 2026-08-29 Renovate was unfrozen for `rknightion/.github` in `m7kni/renovate-config` — it had been `enabled: false` on the mistaken belief that callers tracked `@main`, which froze the fleet across 19 different hub SHAs (v1.3.1 June → v1.9.7 August) so that no hub fix ever propagated. Bumps now arrive as one grouped, CI-gated, automerged PR per repo. **A `uses:` whose comment is not a real `# vX.Y.Z` still cannot be bumped** (it resolves to a digest-only update, which the fleet rules disable) — if you find one, repair the comment as part of this task.
---

author: campaign-ordering
created: 2026-08-29 10:42
---
## Standard amendment — `ci` is the sanctioned superset of `check` (RATIFIED)

This supersedes the frozen wording *"`check` is the complete local gate and reproduces every CI job that can run off a GitHub runner"*, which several lanes could not honour without making the pre-commit gate depend on a Docker daemon.

**The definitions now are:**

- **`check`** — everything that runs with **only the language toolchain installed**. This is the pre-commit gate. A leg that runs on a bare toolchain belongs here *however long it takes*.
- **`ci`** — `check` plus the legs CI gates that need a **Docker daemon, a service container, or cross-compilation**, and nothing else. Written as `ci: check <heavy legs>`.

**Every leg you put in `ci` must carry a comment naming which of those three it needs.** That comment is the guard: without it `ci` becomes the bin for anything slow or awkward, `check` quietly stops meaning much, and the fleet is back to a per-repo gate.

Eleven of the 42 lanes arrived at this shape independently before it was ratified, which is why it won.

**If this repo has no such legs, it has no `ci` recipe at all** and `check` is the whole gate. Do not add an empty one.
---

author: campaign-ordering
created: 2026-08-29 10:57
---
## Fleet alignment — the 2otel family converges on one CI shape

These seven Go repos are near-identical applications and had drifted into **two naming dialects and materially different coverage**. The migration rewrites every `run:` block anyway, so converge them in the same change rather than preserving the drift in new clothes.

**Canonical job names** — used by tailscale2otel, graph2otel, polylens2otel and rfc6035-2otel, so this is the majority convention, not an invention:

`build-test` · `lint` · `govulncheck` · `goreleaser-snapshot` · `docker-build` · `coverage` · `ci-success`

`opnsense2otel` and `transceiver-exporter` currently use a second dialect — `tests`, `race`, `docker-build-verify`. Rename to the canonical set as part of this task.

**`ci-success` is the only check the branch ruleset gates**, so jobs can be renamed or merged freely *provided* `ci-success`'s `needs:` list is updated in the same commit. Never rename `ci-success` itself.

**Required gates, and where each lives after the migration:**

| Gate | Recipe | Note |
| --- | --- | --- |
| build + test + `-race` | `just test` | `-race` belongs in the standard test run |
| golangci-lint | `just lint` | needs a `.golangci.yml`, schema v2 |
| **gosec** | `just lint` | **a golangci-lint linter, NOT a separate job** — enable it in `.golangci.yml`. Four of the seven already do it this way; a standalone gosec job would be a third dialect |
| govulncheck | `just vuln` | pinned `golang.org/x/vuln/cmd/govulncheck@v1.3.0`, matching the family |
| goreleaser snapshot | `just snapshot` | cross-compile ⇒ belongs in `ci`, not `check` |
| container build | `just image` | needs a Docker daemon ⇒ belongs in `ci`, not `check` |

**Already done for you (2026-08-29):** `govulncheck` was added to `opnsense2otel`, `transceiver-exporter` and `codexlb2otel` ahead of the migration, because those three had no dependency vulnerability scanning at all. Convert those jobs to `just vuln` like any other; do not re-add them.

**Still missing, fix as part of this task:**

- `opnsense2otel` — has `.golangci.yml` but **`gosec` is not enabled** in it.
- `transceiver-exporter` — **no `.golangci.yml` at all**, and no `-race` in its test job.
- `codexlb2otel` — no `.golangci.yml`, no `-race`, no container build, and **no `ci-success` job and no branch ruleset**, so nothing gates its CI. Adding an aggregator is the right fix but is a separate decision; raise it rather than assuming.

**One known trap:** the `govulncheck@v1.3.0` pins are invisible to Renovate — `go install pkg@version` inside a `run:` block matches no manager. All five are four minor versions behind (current is v1.7.0). Once the version moves into the justfile as a `# renovate:`-annotated `:=` assignment, it becomes managed. That is a real benefit of this migration, not incidental.
---
<!-- COMMENTS:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Migrated the repository to the frozen just task surface, taught workflow contracts to resolve recipes transitively, converted CI and Helm to SHA-pinned setup-just with just 1.58.0, updated contributor documentation and Backlog Definition of Done, and deleted the absorbed module-verifier script. Local evidence is green and idempotent; hosted CI and Helm are green at the workflow checkpoint. Task remains Parked only because AC #8 demands an empty repository-wide historical grep that conflicts with the commissioned task text and out-of-scope history.

## Closeout (2026-08-29, main session)

AC #8 was AMENDED before checking, because as written it was unsatisfiable — the park was correct, not a shortfall.

Two independent defects in the old wording. `grep -rn --exclude-dir=docs/superpowers` never excluded anything: `--exclude-dir` matches a directory BASENAME, so it searched the very directory it claimed to skip (7 files there). And it dropped the "outside the deleted file itself" qualifier that this task's own section 8 carries, so it necessarily matched `archive/` (32 hits in the frozen issue export), `backlog/` (this task quotes the grep command in its own text) and untracked `.claude/worktrees`. Rob chose the `git grep` form on 2026-08-29: it uses real pathspec exclusions and, seeing only tracked files, sidesteps the worktrees and gitignored `codex/` structurally rather than by enumeration.

Verified in this session, not carried over from the wave report:

- `git grep -n 'verify-modules' -- ':!backlog' ':!archive' ':!docs/superpowers'` — no output.
- `just check` run twice consecutively, both exit 0, `git status --porcelain` empty after the second (AC #2 and AC #4 re-proven at the current head, not at the checkpoint).
- `just --list` shows all seven mandatory recipes; `just --groups` lists exactly the six allowed groups; `just --dump --dump-format json` and `just --fmt --check` both exit 0.
- `ci: check snapshot image smoke` — conformant with the fleet standard ratified in 9684f86, so this repo's shape is the ratified one rather than a recorded deviation.

TERMINAL CI: run 33249452889 at d447e90 completed `success` with `ci-success: success`. The wave's own final SHA (c13f382) never reached a terminal conclusion — four consecutive runs were cancelled by a concurrent session's pushes — so this is the green the closeout waited for. Helm does not run at d447e90 (path filters; that commit touched only auto-rc.yml) and is green at its last triggering SHAs: 33244634292 at 4180c7a and 33249272618 at 0a3dacf.

DoD #3 is checked on evidence rather than on the conditional: no generated input changed, AND `gen-check` regenerated inside both `just check` runs and left no diff.

CORRECTION for anyone reading the wave-2 handover: govulncheck is NOT newly Renovate-managed by this migration. `justfile:21` holds `govulncheck_version := "v1.3.0"` with NO `# renovate:` annotation, `renovate.json` has no justfile matcher, and the justfile's own comment at line 17 says the pin TRACKS the `go install` line in the CI jobs. The version moved; its invisibility to Renovate did not change. It remains four minor versions behind (current v1.7.0). Making it managed is real, unstarted work — do not record it as a gain of this task.

Concurrency note: this task was edited by two sessions. The fleet-campaign session appended 9684f86 and d54b388 as comments only, touching neither the description nor the acceptance criteria, and handed the closeout over explicitly before this session wrote to it.
<!-- SECTION:FINAL_SUMMARY:END -->
