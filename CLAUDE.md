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
./tailscale2otel -config config.yaml # run; set otlp.protocol: stdout for local debug w/o a backend
```

`govulncheck` is a CI gate: `go install golang.org/x/vuln/cmd/govulncheck@v1.3.0 && govulncheck ./...`.

### Regenerate generated artifacts (required before commit when you touch them)

```sh
# docs/metrics.md — regenerated from the in-code telemetry catalog (NOT hand-edited).
go run -C tools/metricscatalog . -write -file "$PWD/docs/metrics.md"
```

> Gotcha: `tools/metricscatalog` and `tools/configcheck` are **separate Go modules** (own `go.mod`,
> `replace ../..`). `go run ./tools/metricscatalog` from the repo root **fails** ("main module does
> not contain package") despite what the tool's own help text says. Use `go run -C tools/metricscatalog .`
> with an absolute `-file`, or build first (`cd tools/metricscatalog && go build -o /tmp/mc .`) then
> run `/tmp/mc -check` from the repo root (the default `docs/metrics.md` path is CWD-relative).

The Helm `values.schema.json` and chart `README.md` are also generated, but in CI by GitHub Actions
(see `deploy/CLAUDE.md`); there is no local Go command for them.

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

Three modules, **no `go.work`**: the root module (`github.com/rknightion/tailscale2otel`) plus two
CI-only tool modules. `go build ./...` and `go test ./...` only cover the root module — the tools
are linted/run separately (CI uses a matrix over `.`, `tools/configcheck`, `tools/metricscatalog`).

- `cmd/tailscale2otel/main.go` — thin entrypoint: load config, build slog logger, `app.New` → `Run`.
  `version` is injected via `-ldflags -X main.version=...`.
- `internal/app/` — **composition root**. `app.New` builds the telemetry provider, Tailscale client,
  checkpoint store, shared flow/audit processors, receivers, the collector registry, the admin HTTP
  server (probes + status page + opt-in pprof), and the opt-in Pyroscope profiler; `collectors.go` is
  where each collector is registered/gated. Start here to understand how everything connects. The admin
  status page (`/` HTML + `/api/status.json`) is assembled in `status.go`/`admin_status.go` from
  `internal/app/statusdata/` DTOs, rendered via the embedded template in `internal/app/statushtml/`
  (self-contained — no CDN/external assets, so it renders on an air-gapped tailnet).
- `internal/appcatalog/` — the app layer's self-obs metric descriptors (`tailscale2otel.up`,
  `api.requests`, `api.retries`). A deliberate **leaf** package so `internal/catalog` can aggregate
  these without importing `internal/app` (which imports `internal/catalog` for the status page — see the
  import-cycle gotcha below).
- `internal/collector/` — scheduler + registry + checkpoints, and one subpackage per source
  (devices, flowlogs, auditlogs, users, keys, settings, acl, dns, nodemetrics). See
  `internal/collector/CLAUDE.md` for the "add a collector" recipe.
- `internal/telemetry/`, `internal/semconv/`, `internal/metricdoc/`, `internal/catalog/` — the OTEL
  facade and the code-as-docs metrics catalog. See `internal/telemetry/CLAUDE.md`.
- `internal/tsapi/` — Tailscale API client + log "doers" (auth: OAuth preferred, or API key).
- `internal/stream/` (Splunk-HEC receiver), `internal/webhook/` (HMAC-verified), `internal/dedup/`
  (bounded FIFO failsafe) — alternate ingestion paths that feed the **same** processors as the pollers.
- `internal/flowlog/`, `internal/audit/` — record types + shared processors (used by both poll & stream).
- `internal/enrich/` — in-memory device cache (IP/nodeID → name) populated by the devices collector.
- `internal/config/` — YAML config with `${ENV}` expansion, defaults, `Validate()` and advisory `Warnings()`.
- `tools/metricscatalog/` (docs/metrics.md generator), `tools/configcheck/` (validates config via the
  real `config.Load`, catching cross-field rules JSON Schema can't express).
- `deploy/` — Dockerfiles, docker-compose, Helm chart, Grafana dashboards, Prometheus alert rules. See `deploy/CLAUDE.md`.

## CI gates (a PR must pass all of these)

`go vet` · `go build` · `go test -race` · `golangci-lint` (root + both tool modules) ·
`docs/metrics.md` in sync (`metricscatalog -check`) · `govulncheck` · GoReleaser snapshot build ·
Docker image build. The Helm workflow additionally gates: `helm lint`/`template`, `values.schema.json`
drift, `helm-docs` drift, and `configcheck` on both `config.example.yaml` and the chart-rendered config.
Match these locally before claiming work is done.

## Config & secrets

- `config.example.yaml` is the committed canonical config (all keys documented). Copy to `config.yaml`.
- All string values support `${ENV}` expansion — **keep secrets in env vars, not in YAML**.
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
