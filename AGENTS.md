# tailscale2otel

Polls the Tailscale API (and optionally receives its log stream / webhooks) and exports
**OpenTelemetry-native metrics + logs** over OTLP, optimized for Grafana Cloud. Single static Go
binary. See `README.md` for the user-facing pitch and `docs/metrics.md` for the signal catalog.

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

`just run` starts the exporter with `config.yaml`; set `otlp.protocol: stdout` for local debug without a backend.

`govulncheck` is a CI gate: run `just vuln`; `just setup` installs the pinned binary.

> **A clean local `actionlint` does NOT mean the actionlint CI lane will pass.** actionlint shells
> out to whatever `shellcheck` is on PATH, and its findings depend on that version. A local
> shellcheck **0.11.0** does not emit **SC2015** (`A && B || C` is not if-then-else) at all, while
> the runner's older shellcheck does — so a workflow edit can pass locally on both actionlint
> 1.7.7 and the CI-pinned 1.7.12 and still fail CI. Confirmed 2026-07-28: `[ -n "$x" ] && [ "$x" !=
> "null" ] || { …; }` in `live-contract.yml` was clean under both local actionlint versions and
> failed the lane. The version gap is the reverse of the usual one — local is NEWER and reports
> LESS — so "my tool is up to date" is not reassurance here. When a workflow's `run:` block is
> edited, prefer a plain `if` over `A && B || C` rather than trusting the local run.

### Regenerate generated artifacts (required before commit when you touch them)

Eleven artifact families are committed but **generated** — each a pure function of its inputs and each gated in CI
by a `fail-on-diff` check (forgetting to regenerate is the classic red build, e.g. bumping `Chart.yaml`
without the README). `just gen` reproduces them all locally, byte-for-byte with CI:

```sh
just gen-tools                         # ONCE PER MACHINE: install the pinned helm tools (see below)
just gen                               # all
just --list                            # THE TARGET LIST: one `gen-*` recipe per family, in group `gen`
just gen-helm                          # the chart README.md + values.schema.json
just gen-dashboards gen-promrules gen-counts   # dashboards, rules + capability counts
just gen-metrics                       # docs/metrics.md
just gen-envref                        # docs/env-vars.md
just gen-config-schema                 # the ROOT config.schema.json (NOT the chart's values.schema.json)
```

`just gen <target>...` is the same thing spelled the older way and still works, so the
`just gen counts` in a test failure message needs no translation. There is no target list in
`scripts/regen-generated.sh` any more: each family is a `regen_<target>` function there and a
`gen-<target>` recipe here, and `just --list` is the only place the set is written down.

| generated file | inputs | tool |
| --- | --- | --- |
| `docs/metrics.md` | the in-code telemetry catalog | `tools/metricscatalog` |
| `docs/env-vars.md` | `config.example.yaml` (keys, defaults, inline comments) | `TestEnvReferenceDocInSync -update` (root module; no separate tool) |
| `config.schema.json` | the `Config` struct + `config.example.yaml` | `TestConfigSchemaInSync -update` (root module; no separate tool). The repo-ROOT schema for `config.yaml` — not the chart's `values.schema.json` below |
| `docs/signal-coverage.md` | `internal/catalog/signal_dispositions.json` | `TestSignalCoverageDocInSync -update` (root module; no separate tool) |
| `deploy/helm/tailscale2otel/README.md` | `Chart.yaml` + `values.yaml` + `README.md.gotmpl` | `helm-docs` **v1.14.2** |
| `deploy/helm/tailscale2otel/values.schema.json` | `values.yaml` (draft 7) | `helm-values-schema-json` **v2.6.0** |
| `deploy/grafana/tailscale2otel-{tailnet,health}.json` | `deploy/grafana/gen/build.py` + `gen/dashboards.py` | `python3 build.py --out-dir …` (stdlib only) |
| `deploy/alerts/grafana-managed/` | `deploy/alerts/gen/build_rules.py` | `python3 build_rules.py --out …` (stdlib only) |
| `deploy/alerts/prometheus/tailscale2otel.rules.yaml` | `deploy/alerts/gen/build_rules.py` | `python3 build_rules.py --prom-out …` (stdlib only) |
| `docs/alert-profiles.md` | `deploy/alerts/gen/build_rules.py` | `python3 build_rules.py --docs-out …` (stdlib only) |
| `internal/catalog/capability_counts.json` | in-code catalog + shipped dashboard/rule artifacts | `just gen-counts` (`check-capability-counts.py`, stdlib only) |

> **The two helm tools are version-pinned — install them with `just gen-tools` (or `just setup`).**
> CI pins the *actions*, and each action installs one specific tool binary; a local tool of any other
> version generates **different output**, which lands as unrelated churn or a red `fail-on-diff`. The
> script now verifies the installed version against the pin and **loudly SKIPs rather than writing a
> wrong file**, so a mismatch can no longer silently corrupt an artifact. The pins live in the generation
> task — **when Renovate bumps `losisin/helm-docs-github-action` or
> `losisin/helm-values-schema-json-action` in `.github/workflows/helm.yml`, update them to match**
> (the action version ≠ the tool version: action `v3.2.0` installs tool `v2.6.0`).
>
> Gotcha worth knowing: a plain `go install …/helm-docs@v1.14.2` yields a binary that reports **no
> version**, because helm-docs reads its version from a build-time ldflag rather than Go build info.
> The README template's version footer is guarded by `{{ if .HelmDocsVersion }}`, so that binary
> silently drops the footer — a plausible-but-wrong README. The `tools` target passes the ldflag
> goreleaser would (`-X main.version=1.14.2`); don't hand-install these tools without it.

> **The pre-commit hook installs itself.** Git can't run anything on clone (by design), so once per
> clone run `just setup`, `go generate ./...` (or `scripts/setup.sh`) — either points `core.hooksPath` at `.githooks`
> via `cmd/tailscale2otel/generate.go`. CI never runs `go generate`, so this never fires there.
> `.githooks/pre-commit` then regenerates *only* the artifacts your staged changes touch and re-stages
> them; it's a silent no-op otherwise. **It shells out to the `just gen-<family>` recipes, never to a
> script** —
> `just` is this repo's only supported task surface, and a hook calling a generator directly is a
> second, divergent definition of how an artifact is built. A missing tool — `just` included — is a
> loud SKIP, never a block (CI's fail-on-diff stays the hard backstop); bypass a run with
> `git commit --no-verify`.

> Gotcha: `tools/metricscatalog` and `tools/configcheck` are **separate Go modules** (own `go.mod`,
> `replace ../..`). `go run ./tools/metricscatalog` from the repo root **fails** ("main module does
> not contain package") despite what the tool's own help text says. Use `go run -C tools/metricscatalog .`
> with an absolute `-file`, or build first (`cd tools/metricscatalog && go build -o /tmp/mc .`) then
> run `/tmp/mc -check` from the repo root (the default `docs/metrics.md` path is CWD-relative).

CI re-validates all eleven artifact families via `fail-on-diff` (the Helm pair in GitHub Actions, see `deploy/CLAUDE.md`;
`docs/metrics.md` via `metricscatalog -check`; `docs/env-vars.md`, `docs/signal-coverage.md` and
`config.schema.json` via `TestEnvReferenceDocInSync` / `TestSignalCoverageDocInSync` /
`TestConfigSchemaInSync` in the
normal `go test -race ./...` run — no extra workflow step; the two Grafana artifacts via ci.yml's
`dashboards-drift` job, which runs `just gen-check` — `gen-dashboards gen-promrules gen-counts` then
`git diff --exit-code`, including the shipped Prometheus rules, `docs/alert-profiles.md` and the
public capability-count source).
The local tools above are installed on this machine.

> **`signal_dispositions.json` is the one generated-adjacent file you do NOT blindly regenerate.**
> `just gen-coverage` rebuilds only the *page* from the manifest. The manifest itself
> is updated by hand-running `go test ./internal/catalog -run TestSignalDispositionsInSync -update`,
> which is deliberately **non-silencing**: it derives `visualized`/`alertable`/`recorded`/
> `drives_a_variable` from the actual dashboard and rule artifacts and prunes dead rows, but leaves a
> new signal's disposition EMPTY — and an
> empty disposition always fails the gate. **There is no value a human may assign** — all three
> dispositions are derived — so a signal on no surface cannot be settled by editing the manifest,
> only by giving it a panel. Regenerating after changing the dashboards or rules is expected and
> correct; regenerating to make a red gate green is not, and will not work.
>
> #526 removed the three escapes that used to exist. `raw_only` and `omitted` let an awkward signal
> be re-labelled instead of panelled, and 35 had accrued between them; `pending_panel` replaced
> them as an explicitly transitional shrink-only ledger and was deleted with its last row. Do not
> reintroduce any of them. The only exemptions are the three **structural** classes in
> `catalog.StructuralExemptions()`, each an individually justified entry.
>
> **`visualized` means a PANEL, and `drives_a_variable` is a separate value on purpose (#527).** A
> presence sentinel's `label_values()` call is as real a reference as a panel query and shows nobody
> anything; while both fed one value a signal could clear the coverage bar while being invisible.
> `tailscale.subnet_routes.advertised` did, and `tailscale.key.expiring` cleared it merely because
> its name is an option VALUE in a dropdown. Do not fold the two back together.

> **Nothing under `deploy/grafana` or `deploy/alerts` is hand-maintained any more.** The four legacy
> classic-schema dashboards and the former hand-maintained Prometheus-ruler
> `deploy/alerts/tailscale2otel.rules.yaml` were **deleted**
> (#394) — sitting outside the drift gate is exactly why they rotted. The project is **Grafana v2 /
> Grafana 13+ only and will never ship a Classic export**; do not reintroduce a v1 path (v1 cannot
> express `conditionalRendering`, so every feature-gated tab would render permanently EMPTY rather
> than hiding). Alert rules are **Grafana-managed `rules.alerting.grafana.app/v0alpha1` manifests**,
> one JSON per rule, pushed with `gcx resources push -p deploy/alerts/grafana-managed`.
>
> Two format traps in those manifests, both silent: **`noDataState` and `execErrState` BOTH spell
> the OK state `"Ok"`** — the API accepts only `["Error", "Ok", "Alerting", "KeepLast"]` for either
> — and durations are Go-style strings (`"30m0s"`, not `"5m"`).
>
> **CORRECTED 2026-07-27, by an actual push.** This file previously stated that `execErrState`
> spelled its own value `"OK"`. It does not, and `"OK"` is rejected outright. That wrong belief was
> encoded in five places at once — here, `build_rules.py`, `validate_manifests.py`, `test_rules.py`
> and three docs — so **every offline gate agreed with itself and all 19 `advisory` rules failed at
> push time** with `spec.execErrState: Invalid value: "OK"`. A validator written from the same
> assumption as the generator cannot catch that assumption being wrong.
>
> The durable lesson is not the casing: **`gcx resources validate` does not validate the spec** (it
> says so — "does not support server-side dry-run … did not validate the spec"), and neither
> `validate_manifests.py` nor promtool exercises the real schema. **Only a real
> `gcx resources push` proves a rule is deployable**, and pushing is pre-authorized here — so push
> and read the result rather than trusting a green offline run.
>
> `build_rules.py --prom-out` renders the same catalogue as the committed, supported Prometheus rule
> artifact at `deploy/alerts/prometheus/tailscale2otel.rules.yaml`. CI drift-checks that file and
> `promtool test rules` executes it against the fixtures in `deploy/alerts/tests/`. Parsing proves an
> expression is well-formed; only execution proves it fires when it should.
>
> Separately, `internal/catalog/dashboardrefs_test.go` checks every metric name the generated dashboard
> and the rule manifests QUERY against the in-code catalog's normalized Prometheus spellings. Nothing else
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
- After every change run `just check`; commit a **green** state between units of work.
- Go 1.27 toolchain — `testing/synctest` (fake clock) is used for time-dependent tests
  (`internal/app/heartbeat_test.go`); prefer it over real sleeps.
- **Confirm any `tsclient`/`tsapi` field or method with `go doc` before using it** — the client
  surface has non-obvious shapes (see the gotchas below). gopls/LSP reports stale "undefined method"
  diagnostics after a `go.mod` bump; trust the compiler (`go build`, `go doc`), not the editor.
- Collectors depend only on the frozen contracts (`telemetry.Emitter`, the collector interfaces,
  `enrich.DeviceCache`, `tsapi.Client`, the flow/audit processors); each declares a **narrow** client
  interface it can fake in tests. The `telemetry.Emitter` facade is the only thing touching OTLP — keep
  it that way so OTLP never leaks into collectors.

## Task tracking — Backlog.md

Open work lives in `backlog/`, driven **only** through the `backlog` CLI. `backlog task list --plain`
is the queue; `backlog doc list --plain` lists the durable docs. GitHub Issues was retired for this
repo on **2026-08-14**, and the 401 issues we had filed were archived and then **deleted from
GitHub** — so `gh issue view <N>` 404s. Historical work is still cited as `#NNN`: the *Closed GitHub
issues* doc is the index, and `archive/github-issues-2026-08-14.json` holds every body and reply
(redacted; `archive/README.md` has the placeholder mapping). New work is `tso-NNNN`. Two ID spaces,
no overlap.

**The GitHub tracker is still open, deliberately** — external contributors can file issues, and
Renovate's dependency dashboard still lives there. Anything arriving that way becomes a `tso-NNNN`
task; the board, not the issue, is where it is worked.

Read the **Agent fan-out protocol (canonical)** doc before designing a wave, and the **Wave operating
model** doc for this project's own rules. Docs load on demand via `backlog doc view <id> --plain`, so
neither costs context until something reads it. The protocol is harness-neutral — it routes lanes by
**role**, and its Appendix A (Codex) or Appendix B (Claude Code) resolves a role into a concrete
model and reasoning depth. Name the harness in the run contract and resolve every lane from that
profile; the two harnesses differ in kind, not just in model names.

- **`backlog/` is committed, so no real identifiers in tasks or docs.** No email addresses, handles,
  usernames, account IDs, device names, addresses, or credential values — write the shape, not the
  instance ("the live deployment host", not its name). Aggregate counts, timings and structural
  findings are fine. A tracker *feels* private, which is exactly why this breaks by accident.
- **Never use `--notes` or `--plan` bare** — they *silently replace* the whole section, destroying
  another session's writes with no warning. Use `--append-notes` and `--append-plan`.
- **Finalize in one call**, so an interrupted run cannot leave finished work looking unfinished:
  `backlog task edit tso-0007 --check-ac 1 --check-ac 2 -s Done`. Checking criteria at one step and
  setting status several steps later leaves the task inconsistent if anything interrupts between.
- **Never hand-edit task, draft, doc, decision or milestone markdown.** Section boundaries are
  HTML-comment markers; break one and the section is *silently dropped* at exit 0 — the data is still
  in the file but invisible, until the next write destroys it for real. There is no repair command;
  `backlog doctor` only fixes duplicate task IDs. `backlog/config.yml` is the one file edited by hand,
  because list-valued keys cannot be set through `backlog config set`.
- **Never let two agents edit the same task.** The v1.50 concurrency fix covers the edit funnel but
  *not* reorder, draft saves, the TUI edit path, `doc update` or decision updates.
- **`Parked` is a real status**, not a synonym for To Do: attempted, blocked, and left with a concrete
  resume boundary. Flattening it loses the most valuable thing a long autonomous run produces.
- **Do not build on decisions, and do not use the MCP surface.** Decisions are half-built upstream —
  no `edit`, `view` or `update`, no supersede mechanism, no validation — so durable reference goes in
  **docs** and tasks stay the unit. MCP is frozen upstream and costs 10-50k tokens of permanent
  context against 1-2k for the CLI.

## Module / package layout

Five modules, **no `go.work`**: the root module (`github.com/rknightion/tailscale2otel/v5`) plus four
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

The local gate is `just check`: it runs `just vet` · `just build` · `just test` · `just lint` · `just vuln` ·
`docs/metrics.md` in sync (`just docs-check`) and the other repository checks. `just ci` adds the
GoReleaser snapshot build and Docker image + smoke legs.
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
> **Run `just check` locally** — it mirrors those legs for every module, with the justfile's module list
> kept complete by `internal/ci`'s coverage contract. `internal/ci/workflowcontract_test.go`
> fails if a module is missing from either CI matrix, or if `module-verify` stops running a leg.
>
> **It also runs `promqlcheck` against the ARTIFACTS, which is a different question from the module
> legs and easy to conflate.** Building and unit-testing `tools/promqlcheck` proves nothing about the
> dashboards and rules the repo ships; the artifact run parses every expression and resolves each
> `$variable` against what is in scope where the panel actually sits. In CI it lives in the
> "generated dashboards and rules are in sync" job, not in `module-verify`. #526 landed **65 real
> failures on CI with every local gate green** for exactly this reason, which is why the leg exists.

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
- **Pushing dashboards and alert rules to Grafana is PRE-AUTHORIZED — do not ask.** Standing
  permission from Rob (2026-07-27): `gcx resources push -p deploy/alerts/grafana-managed`, dashboard
  pushes, and deleting a rule the repo no longer ships are all his own stack and his call already
  made. Just do it and report what changed. **`gcx resources push` is ADDITIVE** — it creates and
  updates but never deletes, so a rule removed from the repo keeps evaluating forever until deleted
  by hand. Run `just verify-deploy` (read-only; exit 0 in sync, 1 drift, 2
  unreachable) after any push, and to find orphans. This permission covers Grafana only — it does
  NOT extend to mutating the tailnet itself.
- **Do NOT push DASHBOARDS with `gcx` — they are delivered by GitSync.** `.github/workflows/
  grafana-sync.yml` commits `deploy/grafana/*.json` into `m7kni/gc-gitsync-m7kni`, which is a
  Grafana GitSync source, and Grafana writes UI saves back into that repo. A direct API push is an
  out-of-band edit and leaves the repo and the stack disagreeing with no way to tell which is right.
  Only RULES go via `gcx resources push`. Deleting a dashboard through the API is likewise undone by
  the next sync, which re-creates it from whatever file is still in the GitSync repo — retire one by
  deleting it from `deploy/grafana/`, and the workflow prunes the far side.
- **A green `grafana-sync` run is not proof it published anything.** Its commit step used
  `git diff --quiet`, which sees only TRACKED files. When #526 renamed the single dashboard artifact
  into a tailnet/health pair, both new filenames were untracked, so the check read "already matches"
  and exited 0 — **three consecutive successful runs copied two files and published neither**
  (fixed in `f167a1c`: stage first, then diff the index). If you change what
  `deploy/grafana/` produces, verify the far side by listing
  `repos/m7kni/gc-gitsync-m7kni/git/trees/main`, not by the workflow's conclusion.
- **Live-tailnet verification:** keep lab-specific names, addresses, identifiers, credentials, and
  observability captures out of tracked files. Store secrets and raw captures only in ignored local
  paths. `gcx metrics|logs query` needs BOTH `--from` and `--to`; `auto_configure` must NEVER target a
  real/production tailnet.
- **Kubernetes lab context:** use `robknight.saga-turtle.ts.net` for normal lab reads and writes; it
  reaches the same cluster environment as the direct EKS context. Do not probe or refresh AWS SSO as
  routine preflight. Use the direct AWS/EKS context and touch AWS SSO only when the task genuinely
  cannot be completed correctly through the proxy, such as an explicit ServiceAccount impersonation
  or RBAC proof.
- **Conventional Commits:** commit messages follow `type(scope): subject` (see `git log`); Renovate and
  release tooling assume it.
- **A breaking change (`!`) that cuts a new MAJOR needs the Go module path moved first.** release-please
  does not maintain it, and a major tagged against a stale `/vN` path fails the GoReleaser binaries job
  (this really happened at v2.0.0 — #174). Run `just bump-major` and land it on `main`
  before merging the release PR; `TestModulePathMatchesReleaseVersion` fails the release PR if you
  forget. See `deploy/CLAUDE.md`.

<!-- BACKLOG.MD GUIDELINES START -->
<!-- backlog.md-instructions-version: 1.50.1 -->
<CRITICAL_INSTRUCTION>

## Backlog.md Workflow

This project uses Backlog.md for task and project management.

**For every user request in this project, run `backlog instructions overview` before answering or taking action.**

Use the overview to decide whether to search, read, create, or update Backlog tasks.

Before task lifecycle actions, read the matching detailed guide:
- `backlog instructions task-creation` before creating or splitting tasks
- `backlog instructions task-execution` before planning, changing status or assignee, adding a plan or implementation notes, or implementing task work
- `backlog instructions task-finalization` before checking acceptance criteria, writing final summaries, or moving tasks to terminal statuses

Use `backlog <command> --help` before running unfamiliar commands. Help shows options, fields, and examples.

Do not edit Backlog task, draft, document, decision, or milestone markdown files directly. Use the `backlog` CLI so metadata, relationships, and history stay consistent.

</CRITICAL_INSTRUCTION>
<!-- BACKLOG.MD GUIDELINES END -->
