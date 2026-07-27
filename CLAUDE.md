# tailscale2otel

Polls the Tailscale API (and optionally receives its log stream / webhooks) and exports
**OpenTelemetry-native metrics + logs** over OTLP, optimized for Grafana Cloud. Single static Go
binary. See `README.md` for the user-facing pitch and `docs/metrics.md` for the signal catalog.

## Commands

```sh
go build ./cmd/tailscale2otel        # build the binary (or: go build ./...)
go test -race ./...                  # unit + integration tests (race detector always on in CI)
go vet ./...
golangci-lint run                    # lint (v2; config in .golangci.yml)
golangci-lint fmt                    # apply gofmt + goimports (there is no separate gofmt step)
go generate ./...                    # regenerate portservice data + install the git hooks (see below)
./tailscale2otel -config config.yaml # run; set otlp.protocol: stdout for local debug w/o a backend
```

`govulncheck` is a CI gate: `go install golang.org/x/vuln/cmd/govulncheck@v1.3.0 && govulncheck ./...`.

### Regenerate generated artifacts (required before commit when you touch them)

Seven files are committed but **generated** — each a pure function of its inputs and each gated in CI
by a `fail-on-diff` check (forgetting to regenerate is the classic red build, e.g. bumping `Chart.yaml`
without the README). `scripts/regen-generated.sh` reproduces them all locally, byte-for-byte with CI:

```sh
scripts/regen-generated.sh tools  # ONCE PER MACHINE: install the pinned helm tools (see below)
scripts/regen-generated.sh        # all (pass helm / helm-docs / helm-schema / metrics / envref to scope)
go run -C tools/metricscatalog . -write -file "$PWD/docs/metrics.md"   # just docs/metrics.md
go test ./internal/config -run TestEnvReferenceDocInSync -update       # just docs/env-vars.md
```

| generated file | inputs | tool |
| --- | --- | --- |
| `docs/metrics.md` | the in-code telemetry catalog | `tools/metricscatalog` |
| `docs/env-vars.md` | `config.example.yaml` (keys, defaults, inline comments) | `TestEnvReferenceDocInSync -update` (root module; no separate tool) |
| `docs/signal-coverage.md` | `internal/catalog/signal_dispositions.json` | `TestSignalCoverageDocInSync -update` (root module; no separate tool) |
| `deploy/helm/tailscale2otel/README.md` | `Chart.yaml` + `values.yaml` + `README.md.gotmpl` | `helm-docs` **v1.14.2** |
| `deploy/helm/tailscale2otel/values.schema.json` | `values.yaml` (draft 7) | `helm-values-schema-json` **v2.5.0** |
| `deploy/grafana/tailscale2otel.json` | `deploy/grafana/gen/build.py` | `python3 build.py --out …` (stdlib only) |
| `deploy/alerts/tailscale2otel.grafana-rules.yaml` | `deploy/alerts/gen/build_rules.py` | `python3 build_rules.py --out …` (stdlib only) |

> **The two helm tools are version-pinned — install them with `scripts/regen-generated.sh tools`.**
> CI pins the *actions*, and each action installs one specific tool binary; a local tool of any other
> version generates **different output**, which lands as unrelated churn or a red `fail-on-diff`. The
> script now verifies the installed version against the pin and **loudly SKIPs rather than writing a
> wrong file**, so a mismatch can no longer silently corrupt an artifact. The pins live at the top of
> `scripts/regen-generated.sh` — **when Renovate bumps `losisin/helm-docs-github-action` or
> `losisin/helm-values-schema-json-action` in `.github/workflows/helm.yml`, update them to match**
> (the action version ≠ the tool version: action `v3.0.1` installs tool `v2.5.0`).
>
> Gotcha worth knowing: a plain `go install …/helm-docs@v1.14.2` yields a binary that reports **no
> version**, because helm-docs reads its version from a build-time ldflag rather than Go build info.
> The README template's version footer is guarded by `{{ if .HelmDocsVersion }}`, so that binary
> silently drops the footer — a plausible-but-wrong README. The `tools` target passes the ldflag
> goreleaser would (`-X main.version=1.14.2`); don't hand-install these tools without it.

> **The pre-commit hook installs itself.** Git can't run anything on clone (by design), so once per
> clone run `go generate ./...` (or `scripts/setup.sh`) — either points `core.hooksPath` at `.githooks`
> via `cmd/tailscale2otel/generate.go`. CI never runs `go generate`, so this never fires there.
> `.githooks/pre-commit` then regenerates *only* the artifacts your staged changes touch and re-stages
> them; it's a silent no-op otherwise. A missing tool is a loud SKIP, never a block (CI's fail-on-diff
> stays the hard backstop); bypass a run with `git commit --no-verify`.

> Gotcha: `tools/metricscatalog` and `tools/configcheck` are **separate Go modules** (own `go.mod`,
> `replace ../..`). `go run ./tools/metricscatalog` from the repo root **fails** ("main module does
> not contain package") despite what the tool's own help text says. Use `go run -C tools/metricscatalog .`
> with an absolute `-file`, or build first (`cd tools/metricscatalog && go build -o /tmp/mc .`) then
> run `/tmp/mc -check` from the repo root (the default `docs/metrics.md` path is CWD-relative).

CI re-validates all seven via `fail-on-diff` (the Helm pair in GitHub Actions, see `deploy/CLAUDE.md`;
`docs/metrics.md` via `metricscatalog -check`; `docs/env-vars.md` and `docs/signal-coverage.md` via
`TestEnvReferenceDocInSync` / `TestSignalCoverageDocInSync` in the
normal `go test -race ./...` run — no extra workflow step; the two Grafana artifacts via ci.yml's
`dashboards-drift` job, which runs `scripts/regen-generated.sh dashboards` and then
`git diff --exit-code`). The local tools above are installed on this machine.

> **`signal_dispositions.json` is the one generated-adjacent file you do NOT blindly regenerate.**
> `scripts/regen-generated.sh coverage` rebuilds only the *page* from the manifest. The manifest itself
> is updated by hand-running `go test ./internal/catalog -run TestSignalDispositionsInSync -update`,
> which is deliberately **non-silencing**: it derives `visualized`/`alertable`/`recorded` from the actual
> dashboard and rule artifacts and prunes dead rows, but leaves a new signal's disposition EMPTY — and an
> empty disposition always fails the gate. Choosing `raw_only` vs `omitted`, with an honest note, is a
> human decision. Regenerating after changing the dashboards or rules is expected and correct.

> **`deploy/alerts/tailscale2otel.rules.yaml` and the four legacy standalone dashboards are
> HAND-MAINTAINED, not generated** — do not add them to `regen_dashboards` or the drift gate.
>
> Separately, `internal/catalog/dashboardrefs_test.go` checks every metric name the generated dashboard
> and both rule files QUERY against the in-code catalog's normalized Prometheus spellings. Nothing else
> connects those artifacts to the catalog, so a renamed metric leaves a panel silently empty — it still
> loads, it just shows "No data". That test found the flagship dashboard grouping by
> `tailscale_user_invite_accepted`, a label emitted nowhere (#438). Note it has to subtract the catalog's
> LABEL and log-attribute names too: labels share the `tailscale_` prefix, so a text scan cannot tell a
> metric from a label by shape.

## Development methodology

- **Work directly on `main`:** this repo does not use feature branches — commit straight to `main`
  (and push only when the user asks) unless the user explicitly directs otherwise. Don't create
  branches or worktrees for changes on your own; the default "branch first" reflex is overridden here.
- **Specs & plans are local-only, never committed:** brainstorming design specs and implementation
  plans live under `docs/superpowers/` (gitignored) — written to disk for reference but never entered
  into git history. Always **adversarially self-review** a spec or plan before acting on it: scan for
  placeholders, contradictions, hidden assumptions, and scope creep, and treat your own plan as
  something to attack rather than rubber-stamp.
- **TDD is the rule:** failing test → watch it fail for the right reason → minimal code → green →
  refactor. Standard-library `testing` only — **no testify**.
- **Assert telemetry, not internals:** every collector/processor test drives the code against
  `internal/telemetrytest.Recorder` (an in-memory OTEL reader) and asserts the emitted metrics/logs.
- After every change run `go build ./... && go vet ./... && go test -race ./...` and keep
  `golangci-lint run` clean; commit a **green** state between units of work.
- Go 1.26 toolchain — `testing/synctest` (fake clock) is used for time-dependent tests
  (`internal/app/heartbeat_test.go`); prefer it over real sleeps.
- **Confirm any `tsclient`/`tsapi` field or method with `go doc` before using it** — the client
  surface has non-obvious shapes (see the gotchas below). gopls/LSP reports stale "undefined method"
  diagnostics after a `go.mod` bump; trust the compiler (`go build`, `go doc`), not the editor.
- Collectors depend only on the frozen contracts (`telemetry.Emitter`, the collector interfaces,
  `enrich.DeviceCache`, `tsapi.Client`, the flow/audit processors); each declares a **narrow** client
  interface it can fake in tests. The `telemetry.Emitter` facade is the only thing touching OTLP — keep
  it that way so OTLP never leaks into collectors.

## Module / package layout

Five modules, **no `go.work`**: the root module (`github.com/rknightion/tailscale2otel/v3`) plus four
CI-only tool modules. `go build ./...` and `go test ./...` only cover the root module — the tools
are linted/run separately (CI uses a matrix over `.`, `tools/configcheck`, `tools/metricscatalog`,
`tools/apidrift`, `tools/promqlcheck`).

> `tools/promqlcheck` is the one tool module with **no `replace ../..`** — it needs nothing from the
> root module. It pins `golang.org/x/text` explicitly: the version prometheus v0.313.1 pulls in
> transitively is vulnerable (`GO-2026-5970`), so **Renovate must keep that pin in step with the root
> module's**. Invoke it as `go run -C tools/promqlcheck . -root "$PWD"` — `go run ./tools/promqlcheck`
> from the root fails, per the separate-module gotcha above.

- `cmd/tailscale2otel/main.go` — thin entrypoint: load config, build slog logger, `app.New` → `Run`.
  `version` is injected via `-ldflags -X main.version=...`.
- `internal/app/` — **composition root**. `app.New` resolves the configured tailnets and builds one
  `*tailnetRuntime` per tailnet — its own provider/client, enrich cache, flow/audit processors, and
  collector registry+scheduler — fanning into a `telemetry.ProviderSet` (see `internal/telemetry/CLAUDE.md`),
  or a single Headscale-backed runtime when `provider: headscale` (`internal/hsapi` + `internal/provider`).
  It also builds the shared checkpoint store, the reverse-DNS cache (`internal/rdns`), the release/update-check
  fetchers (`internal/release`), the receivers, the admin HTTP server (probes + status page + opt-in pprof),
  the opt-in Prometheus pull-endpoint server (a **second**, separate listener, default `:2112`), and the
  opt-in Pyroscope profiler; `collectors.go` registers/gates each collector per runtime. Start here to
  understand how everything connects. The admin status page (`/` HTML + `/api/status.json`) is assembled
  in `status.go`/`admin_status.go` from
  `internal/app/statusdata/` DTOs, rendered via the embedded template in `internal/app/statushtml/`
  (self-contained — no CDN/external assets, so it renders on an air-gapped tailnet).
- `internal/appcatalog/` — the app layer's self-obs metric descriptors (`tailscale2otel.up`,
  `api.requests`, `api.retries`). A deliberate **leaf** package so `internal/catalog` can aggregate
  these without importing `internal/app` (which imports `internal/catalog` for the status page — see the
  import-cycle gotcha below).
- `internal/collector/` — scheduler + registry + checkpoints, and one subpackage per source
  (devices, flowlogs, auditlogs, users, keys, settings, acl, dns, nodemetrics, contacts, services,
  webhooks, postureintegrations, logstream). See `internal/collector/CLAUDE.md` for the "add a
  collector" recipe.
- `internal/telemetry/`, `internal/semconv/`, `internal/metricdoc/`, `internal/catalog/` — the OTEL
  facade and the code-as-docs metrics catalog. See `internal/telemetry/CLAUDE.md`.
- `internal/tsapi/` — Tailscale API client + log "doers" (auth: OAuth preferred, or API key).
- `internal/provider/` — abstracts the control plane (Tailscale or Headscale) behind one `ControlPlane`
  interface + capability set, so collectors and app wiring stay provider-agnostic; `*tsapi.Client`
  satisfies it directly, a Headscale adapter (`internal/hsapi`) satisfies the same interface for the
  feature subset Headscale exposes.
- `internal/hsapi/` — minimal read-only HTTP/JSON client for the Headscale control-plane API
  (`/api/v1/*`, Bearer auth) plus the adapter mapping its types onto `provider.ControlPlane`.
- `internal/stream/` (Splunk-HEC receiver), `internal/webhook/` (HMAC-verified), `internal/dedup/`
  (bounded FIFO failsafe) — alternate ingestion paths that feed the **same** processors as the pollers.
- `internal/flowlog/`, `internal/audit/` — record types + shared processors (used by both poll & stream).
- `internal/enrich/` — in-memory device cache (IP/nodeID → name) populated by the devices collector.
- `internal/rdns/` — best-effort, non-blocking reverse-DNS (PTR) cache enriching external IPs seen in
  flow logs; bounded, with positive/negative TTLs, shared process-wide across tailnet runtimes.
- `internal/release/` — cached, fail-open "latest version" fetcher + version parse/compare, shared by
  the self update-available check and per-device version-skew metrics.
- `internal/config/` — layered config loader (defaults → YAML → `TS2OTEL_*` env), `Validate()` and advisory `Warnings()`.
- `tools/metricscatalog/` (docs/metrics.md generator), `tools/configcheck/` (validates config via the
  real `config.Load`, catching cross-field rules JSON Schema can't express), `tools/promqlcheck/`
  (parses every dashboard/rule expression with the real `prometheus/promql/parser`).
- `deploy/` — Dockerfiles, docker-compose, Helm chart, Grafana dashboards, Prometheus alert rules. See `deploy/CLAUDE.md`.

## CI gates (a PR must pass all of these)

Root module: `go vet` · `go build` · `go test -race` · `golangci-lint` · `govulncheck` ·
`docs/metrics.md` in sync (`metricscatalog -check`) · GoReleaser snapshot build · Docker image build.
The Helm workflow additionally gates: `helm lint`/`template`, `values.schema.json` drift, `helm-docs`
drift, and `configcheck` on both `config.example.yaml` and the chart-rendered config.

> **`go test -race ./...` at the root is NOT "the test suite" — there is no `go.work`, so it stops at
> the root module boundary.** Each tool module is a separate `go.mod` on purpose (so it never affects
> the main module's build), which means nothing run from the repo root reaches it. The
> **`module-verify`** matrix job in `ci.yml` covers the three tool modules with `go build` · `go vet` ·
> `go test -race` · **`go mod tidy` diff** · `govulncheck`, and is in `ci-success.needs`; the separate
> `lint` matrix runs `golangci-lint` across all four. Before that job existed the tool modules were
> lint-only, and `tools/configcheck/go.sum` had silently drifted 82 lines out of tidy (#437).
>
> **Run `scripts/verify-modules.sh` locally** — it mirrors those legs for every module (discovered by
> walking for `go.mod`, so a new module is covered the day it is added) and SKIPs loudly rather than
> silently passing when `golangci-lint`/`govulncheck` are absent. `internal/ci/workflowcontract_test.go`
> fails if a module is missing from either CI matrix, or if `module-verify` stops running a leg.

> **API drift CI** (see README "API drift CI" + `internal/oas`, `internal/tsapi/contract`,
> `tools/apidrift`): the PR-time schema-driven **decode tests** and `oas` classifier tests run inside
> `go test -race ./...` and **do** gate PRs. The separate **`fuzz` JOB** (exploratory `go test -fuzz`)
> is deliberately NOT in `ci-success.needs` — finding a new crasher is nondeterministic, so gating it
> would let an unrelated PR randomly block merges; each target's seed corpus rides the gated leg, so a
> known crasher still blocks. Don't conflate the two: "decode fuzz gates" is true of the tests and
> false of the job. The three *scheduled* lanes (`api-drift.yml` **daily**, `clientlib-main.yml`
> **weekly**, `live-contract.yml` **daily**) are advisory — on detection they open a deduped tracking
> issue + fail the scheduled run, but never block PRs. `internal/ci/workflowcontract_test.go` asserts
> these cadences and the gating split against the workflow files, because the README stated two of
> them wrong for months while nothing failed (#436). The live lane does NOT use GitHub OIDC — it mints a short-lived
> Tailscale API token via OAuth client-credentials, using a read-only (`all:read`) OAuth client.
>
> **The credentials are GitHub secrets on a standard `ubuntu-latest` runner.** An earlier design kept
> them in the environment of a dedicated **self-hosted** runner (label `tailscale-api`) — that runner
> was **never provisioned**, so the lane queued forever and was auto-cancelled at 24h every week,
> producing no signal at all (#160). Standing a self-hosted runner up on a PUBLIC repo is also a risk
> GitHub warns against, since a fork PR can target it. Secrets are safe here precisely because the lane
> is `schedule` + `workflow_dispatch` only, so a fork PR can never reach them; worst case on a leak is
> "can read the tailnet". **Do not "restore" the self-hosted runner** — read the header comment in
> `.github/workflows/live-contract.yml` first.

> **The vendored OpenAPI spec is `spec/tailscale-api.json`** (~286 KB; 58 paths / 90 operations, 34 of
> them GET), with provenance in `spec/README.md`. It is NOT at the repo root, and there is no `.yaml`
> copy. Refreshing it is manual and is how you *acknowledge* a detected drift — the daily
> `api-drift.yml` fetches the live spec to `/tmp` for comparison but never writes back.
> `internal/oas.ParseSpec` keeps **only** `get` operations and silently drops every other verb, so
> `Spec.Ops` never contains a POST/PUT/PATCH/DELETE.

## Config & secrets

- Configuration is **layered**: built-in defaults < optional YAML file < environment variables. The
  YAML file is optional — passing no `-config` flag runs from defaults + env alone (handy for
  containers). See `docs/configuration.md` for the full reference.
- **`TS2OTEL_*` env convention:** every config field is settable via `TS2OTEL_` + the dotted key
  path, with `__` between levels (e.g. `tailscale.auth.oauth.client_secret` →
  `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET`). Env overrides the file. **Keep secrets in env
  vars — they never need to appear in YAML.**
- `config.example.yaml` is the committed starter config showing the common knobs; the full key-by-key
  reference is `docs/configuration.md`.
- `config.local.yaml`, `config.smoke.yaml`, `config.lowlog.yaml`, `.env.local`, and `.secrets/` are
  **gitignored** — never commit credentials. `/checkpoints.json` and `/.capture/` (captured real-tailnet
  fixtures) are also ignored.
- Prefer OAuth (`auth.method: oauth`, auto-refreshing) over API keys (expire ≤90d, user-bound — config
  WARNs about this).

## Project-wide gotchas

- **Generated docs:** never hand-edit content between `<!-- BEGIN GENERATED -->` / `<!-- END GENERATED -->`
  in `docs/metrics.md`; regenerate (command above). Prose outside the markers is safe to edit.
- **OTLP→Prometheus naming:** queries use the *normalized* name, not the OTEL source name. Dots→underscores,
  monotonic counters get `_total`, units suffix (`By`→`_bytes`, `s`→`_seconds`, `d`→`_days`), and a
  **unit-`"1"` gauge gets `_ratio` — even plain integer counts** (e.g. `tailscale_devices_count_ratio`).
- **poll vs. stream:** for `flowlogs`/`auditlogs` pick exactly ONE ingestion path per log type
  (`source: poll` *or* `stream`). `both` (or running the receiver while a collector still polls)
  double-counts; cross-source dedup is a best-effort failsafe and the app WARNs at startup.
- **Device enrichment depends on the `devices` collector:** flow/audit IP→name resolution silently
  degrades to `unknown`/`external` if `devices` is disabled.
- **Pinned deps — don't casually `go get`/`go mod tidy`:** OTEL core (`go.opentelemetry.io/otel`
  v1.44.0) and the log SDK (`go.opentelemetry.io/otel/log` v0.20.0) are version-locked and must move
  **together** (Renovate batches them into one lockstep PR) or the build breaks. The two tool modules
  use `replace ../..` and are CI-only — never runtime deps.
- **`internal/catalog` must not import `internal/app`:** the admin status page (in `internal/app`)
  imports `internal/catalog` to render the metric/log tables, so the app layer's own self-obs
  descriptors live in the leaf package `internal/appcatalog` to keep the dependency one-way. Put new
  app-layer descriptors there, not in `internal/app`; `internal/app/catalog_test.go` guards them against
  their emit sites (`heartbeat.go`, `selfobs.go`).
- **Profiling is opt-in and admin-coupled:** `/debug/pprof` mounts on the admin server, so
  `profiling.pprof.enabled` requires `admin.enabled` (`Validate()` errors otherwise). The Pyroscope push
  agent needs `profiling.pyroscope.server_address`; a `grafana.net` target also needs
  `basic_auth_password` (a `profiles:write` access-policy token), which `Warnings()` flags. Mutex/block
  profiles stay empty unless `mutex_profile_fraction`/`block_profile_rate` are set.
- **Tailscale wire-format quirks — decode defensively:** flow-log `proto` is a *number* on the wire
  (`flowlog.transportName` maps IANA→name), and audit `old`/`new` are polymorphic
  (string|object|array|null), so both are `json.RawMessage`/typed loosely. Rich device data (online,
  per-DERP latency, routes, os.version, nodeId, tags) comes from `tsapi.DevicesRich()` (raw
  `GET /devices?fields=all`), **not** the flat `tsclient.Device`. Synthetic fixtures miss these —
  validate record-type changes against real captures in `.capture/`.
- **OTLP/HTTP endpoint path is used as-is:** the otlphttp exporter does NOT append `/v1/{metrics,logs}`
  — `internal/telemetry.otlpHTTPURL()` does. A bare gateway URL 404s silently without it.
- **Live-tailnet verification:** keep lab-specific names, addresses, identifiers, credentials, and
  observability captures out of tracked files. Store secrets and raw captures only in ignored local
  paths. `gcx metrics|logs query` needs BOTH `--from` and `--to`; `auto_configure` must NEVER target a
  real/production tailnet.
- **Conventional Commits:** commit messages follow `type(scope): subject` (see `git log`); Renovate and
  release tooling assume it.
- **A breaking change (`!`) that cuts a new MAJOR needs the Go module path moved first.** release-please
  does not maintain it, and a major tagged against a stale `/vN` path fails the GoReleaser binaries job
  (this really happened at v2.0.0 — #174). Run `scripts/bump-module-major.sh` and land it on `main`
  before merging the release PR; `TestModulePathMatchesReleaseVersion` fails the release PR if you
  forget. See `deploy/CLAUDE.md`.
