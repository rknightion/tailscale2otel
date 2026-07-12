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

Four files are committed but **generated** — each a pure function of its inputs and each gated in CI
by a `fail-on-diff` check (forgetting to regenerate is the classic red build, e.g. bumping `Chart.yaml`
without the README). `scripts/regen-generated.sh` reproduces all four locally, byte-for-byte with CI:

```sh
scripts/regen-generated.sh        # all (pass helm / helm-docs / helm-schema / metrics / envref to scope)
go run -C tools/metricscatalog . -write -file "$PWD/docs/metrics.md"   # just docs/metrics.md
go test ./internal/config -run TestEnvReferenceDocInSync -update       # just docs/env-vars.md
```

| generated file | inputs | tool |
| --- | --- | --- |
| `docs/metrics.md` | the in-code telemetry catalog | `tools/metricscatalog` |
| `docs/env-vars.md` | `config.example.yaml` (keys, defaults, inline comments) | `TestEnvReferenceDocInSync -update` (root module; no separate tool) |
| `deploy/helm/tailscale2otel/README.md` | `Chart.yaml` + `values.yaml` + `README.md.gotmpl` | `helm-docs` |
| `deploy/helm/tailscale2otel/values.schema.json` | `values.yaml` (draft 7) | `helm-values-schema-json` |

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

CI re-validates all four via `fail-on-diff` (the Helm pair in GitHub Actions, see `deploy/CLAUDE.md`;
`docs/metrics.md` via `metricscatalog -check`; `docs/env-vars.md` via `TestEnvReferenceDocInSync` in the
normal `go test -race ./...` run — no extra workflow step). The local tools above are installed on this
machine.

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

Four modules, **no `go.work`**: the root module (`github.com/rknightion/tailscale2otel`) plus three
CI-only tool modules. `go build ./...` and `go test ./...` only cover the root module — the tools
are linted/run separately (CI uses a matrix over `.`, `tools/configcheck`, `tools/metricscatalog`,
`tools/apidrift`).

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
  real `config.Load`, catching cross-field rules JSON Schema can't express).
- `deploy/` — Dockerfiles, docker-compose, Helm chart, Grafana dashboards, Prometheus alert rules. See `deploy/CLAUDE.md`.

## CI gates (a PR must pass all of these)

`go vet` · `go build` · `go test -race` · `golangci-lint` (root + **three** tool modules:
`configcheck`, `metricscatalog`, `apidrift`) · `docs/metrics.md` in sync (`metricscatalog -check`) ·
`govulncheck` · GoReleaser snapshot build · Docker image build. The Helm workflow additionally gates:
`helm lint`/`template`, `values.schema.json` drift, `helm-docs` drift, and `configcheck` on both
`config.example.yaml` and the chart-rendered config. Match these locally before claiming work is done.

> **API drift CI** (see README "API drift CI" + `internal/oas`, `internal/tsapi/contract`,
> `tools/apidrift`): the PR-time **decode-fuzz** and `oas` classifier tests run inside `go test -race ./...`
> and **do** gate PRs. The three *scheduled* lanes (`api-drift.yml`, `clientlib-main.yml`,
> `live-contract.yml`) are advisory — on detection they open a deduped tracking issue + fail the
> scheduled run, but never block PRs. The live lane does NOT use GitHub OIDC — it mints a short-lived
> Tailscale API token via OAuth client-credentials, using a read-only (`all:read`) OAuth client whose
> credentials live in the environment of a dedicated self-hosted runner (label `tailscale-api`), not
> in GitHub secrets (see `.github/workflows/live-contract.yml`).

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
