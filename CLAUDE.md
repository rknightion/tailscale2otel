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

## Module / package layout

Three modules, **no `go.work`**: the root module (`github.com/rknightion/tailscale2otel`) plus two
CI-only tool modules. `go build ./...` and `go test ./...` only cover the root module — the tools
are linted/run separately (CI uses a matrix over `.`, `tools/configcheck`, `tools/metricscatalog`).

- `cmd/tailscale2otel/main.go` — thin entrypoint: load config, build slog logger, `app.New` → `Run`.
  `version` is injected via `-ldflags -X main.version=...`.
- `internal/app/` — **composition root**. `app.New` builds the telemetry provider, Tailscale client,
  checkpoint store, shared flow/audit processors, receivers, and the collector registry; `collectors.go`
  is where each collector is registered/gated. Start here to understand how everything connects.
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
- **Conventional Commits:** commit messages follow `type(scope): subject` (see `git log`); Renovate and
  release tooling assume it.
