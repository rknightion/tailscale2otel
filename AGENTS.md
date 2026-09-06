# tailscale2otel

Polls the Tailscale (or Headscale) control plane and exports OpenTelemetry-native metrics and logs
over OTLP, tuned for Grafana Cloud. Single static Go binary. `README.md` is the user-facing pitch;
`docs/` is the published site.

This repository is PUBLIC. Keep lab-specific names, addresses, identifiers, credentials and
observability captures out of every tracked file, `backlog/` included - write the shape, not the
instance.

## Task interface

`just check` is the gate. `just ci` adds the goreleaser cross-compile and the container image +
smoke legs. `just --list` is the authoritative recipe list; `just --show <recipe>` is what one runs.

- `just setup` once per clone: pinned `golangci-lint`, `govulncheck`, the two Helm generators, and
  `core.hooksPath` at `.githooks`. Git cannot run anything on clone, so nothing installs it for you.
- `just lint` and `just vuln` assert the invoked binary against the justfile pin before running. A
  local lint result is evidence only when that assertion passed; `just setup` repairs a stale tool.
- `prune-rules` and `bump-major` are `[confirm]` recipes that mutate outside the tree. Never pass
  `--yes` or `JUST_YES=1`; run `just` with stdin from `/dev/null`.
- `just gen <family>...` and `just gen-<family>` are the same thing. Test failure messages and CI
  job names use the spaced spelling.
- `just review-sharded` is the CodeRabbit path here - a whole-repo review exceeds the transport
  limit. See `docs/coderabbit-sharded-review.md`.

## Generated artifacts

Every committed generated artifact has a `gen-<family>` recipe and a CI fail-on-diff gate. `just gen`
reproduces the set; the `gen` group in `just --list` is the only place the set is written down.
`.githooks/pre-commit` regenerates only what your *staged* changes invalidate and re-stages it.

- Never hand-edit between `<!-- BEGIN GENERATED -->` and `<!-- END GENERATED -->` in
  `docs/metrics.md`. Prose outside the markers is safe.
- `internal/catalog/signal_dispositions.json` is the one generated-adjacent file you do NOT blindly
  regenerate, and regenerating it cannot turn a red coverage gate green.

## Grafana delivery

- **Pushing alert rules to Rob's Grafana stack is pre-authorized - do not ask.** That covers
  `gcx resources push -p deploy/alerts/grafana-managed` and deleting a rule the repo no longer
  ships. It does NOT extend to mutating the tailnet itself.
- **Do NOT push DASHBOARDS with `gcx`.** They are delivered into `m7kni/gc-gitsync-m7kni` by
  `.github/workflows/grafana-sync.yml`; an API push is an out-of-band edit the next sync undoes.
- Nothing under `deploy/grafana` or `deploy/alerts` is hand-maintained. The project is Grafana v2 /
  Grafana 13+ only and will never ship a Classic export.

## Modules and CI

Root module `github.com/rknightion/tailscale2otel/v5` plus four CI-only tool modules
(`tools/{configcheck,metricscatalog,apidrift,promqlcheck}`). **No `go.work`**, deliberately, so a
tool module can never affect the main module's build.

- `go test -race ./...` at the root is NOT "the test suite": it stops at the root module boundary.
  ci.yml's `module-verify` matrix covers the tool modules (build, vet, race test, `go mod tidy`
  diff, `govulncheck`) and is in `ci-success.needs`; the `lint` matrix covers all five modules.
  `internal/ci/workflowcontract_test.go` fails if a module drops out of either matrix, or if
  `module-verify` stops running a leg.
- `tools/promqlcheck` is the one tool module with no `replace ../..` - it needs nothing from the
  root module - and it pins `golang.org/x/text` against a transitive vulnerability, so Renovate must
  keep that in step with the root module's. Invoke it as `go run -C tools/promqlcheck . -root "$PWD"`.
- A breaking change that cuts a new MAJOR needs the Go module path moved first: run `just bump-major`
  and land it on `main` before merging the release PR. release-please does not maintain the path,
  and a major tagged against a stale `/vN` fails the GoReleaser binaries job.
  `TestModulePathMatchesReleaseVersion` (`internal/config/modulepath_test.go`) catches it.
- Conventional Commits: Renovate and the release tooling assume `type(scope): subject`.

## Config and secrets

- Layered: built-in defaults < optional YAML file < environment. Passing no `-config` flag runs from
  defaults + env alone. `docs/configuration.md` is the full key reference; `config.example.yaml` is
  the committed starter.
- Env convention is `TS2OTEL_` + the dotted key path with `__` between levels, e.g.
  `tailscale.auth.oauth.client_secret` -> `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET`. Env
  overrides the file. Keep secrets in env vars; they never need to appear in YAML.
- Prefer OAuth (`auth.method: oauth`, auto-refreshing) over API keys (expire in 90 days or less,
  user-bound; config WARNs about this).
- `config.local.yaml`, `config.smoke.yaml`, `config.lowlog.yaml`, `.env*`, `.secrets/`,
  `checkpoints.json` and `.capture/` are gitignored.
- `auto_configure` must NEVER target a real tailnet. `gcx metrics|logs query` needs BOTH `--from`
  and `--to`.

## Code conventions and gotchas

- Standard-library `testing` only, no testify. Collector and processor tests drive the code against
  `internal/telemetrytest.Recorder` (an in-memory OTEL reader) and assert the emitted metrics and
  logs rather than internals. `testing/synctest` is the fake clock for time-dependent tests
  (`internal/app/heartbeat_test.go`); prefer it over real sleeps.
- The `telemetry.Emitter` facade is the only thing touching OTLP. Collectors depend only on the
  frozen contracts and each declares a narrow client interface it can fake, which is what keeps OTLP
  out of collectors.
- Confirm any `tsclient`/`tsapi` field or method with `go doc` before using it - the client surface
  has non-obvious shapes, and gopls reports stale "undefined method" diagnostics after a `go.mod`
  bump. Trust the compiler, not the editor.
- **`internal/catalog` must not import `internal/app`.** The admin status page lives in
  `internal/app` and imports `internal/catalog` to render its tables, so the app layer's own
  self-obs descriptors live in the leaf package `internal/appcatalog`. Put new app-layer descriptors
  there; `internal/app/catalog_test.go` guards them against their emit sites.
- OTLP-to-Prometheus naming: queries use the *normalized* name, not the OTEL source name. Dots to
  underscores, monotonic counters get `_total`, units suffix (`By`->`_bytes`, `s`->`_seconds`,
  `d`->`_days`), and a unit-`"1"` gauge gets `_ratio` even for plain integer counts
  (`tailscale_devices_count_ratio`).
- The otlphttp exporter does NOT append `/v1/{metrics,logs}` - `internal/telemetry.otlpHTTPURL()`
  does. A bare gateway URL 404s silently without it.
- For `flowlogs` and `auditlogs` pick exactly ONE ingestion path per log type (`source: poll` or
  `stream`). `both`, or running the receiver while a collector still polls, double-counts;
  cross-source dedup is a best-effort failsafe and the app WARNs at startup.
- Flow/audit IP-to-name enrichment silently degrades to `unknown`/`external` when the `devices`
  collector is disabled.
- OTEL core and the OTEL log SDK are version-locked and must move **together** (Renovate batches
  them into one lockstep PR) or the build breaks. Don't casually `go get` or `go mod tidy`.
- Tailscale wire format needs defensive decoding: flow-log `proto` is a *number* on the wire, and
  audit `old`/`new` are polymorphic (string|object|array|null). Rich device data (online, per-DERP
  latency, routes, os.version, nodeId, tags) comes from `tsapi.DevicesRich()` (raw
  `GET /devices?fields=all`), **not** the flat `tsclient.Device`. Synthetic fixtures miss these;
  validate record-type changes against the real captures in `.capture/`.
- Profiling is opt-in and admin-coupled: `/debug/pprof` mounts on the admin server, so
  `profiling.pprof.enabled` requires `admin.enabled` (`Validate()` errors otherwise). Mutex and
  block profiles stay empty unless `mutex_profile_fraction`/`block_profile_rate` are set. The
  Prometheus pull endpoint is a **second**, separate listener (default `127.0.0.1:2112`).
- `internal/app` is the composition root: start at `app.New` to see how everything connects.

## Task tracking

Open work is `backlog/`, driven only through the `backlog` CLI. GitHub Issues was retired here and
its issues deleted, so `gh issue view <N>` 404s; historical `#NNN` citations resolve through
`archive/github-issues-2026-08-14.json`. New work is `TSO-NNNN`. The GitHub tracker stays open
deliberately for external contributors and Renovate's dependency dashboard; anything arriving there
becomes a `TSO-NNNN` task.

## Deeper references

- `reference/generated-artifacts.md` - read before regenerating an artifact, changing a `gen-*`
  recipe, bumping a Helm generator pin, or touching `signal_dispositions.json`.
- `reference/grafana-delivery.md` - read before pushing, deleting or debugging anything on the live
  Grafana stack, or editing an artifact under `deploy/grafana` or `deploy/alerts`.
- `reference/ci-gates.md` - read before editing a workflow or diagnosing a red check that
  `just check` does not reproduce.
- `reference/backlog-workflow.md` - read before writing to the board or running a fan-out wave.
- `deploy/` carries its own instructions - read them before touching the Helm chart, the
  Dockerfiles, Compose or the release packaging.
- `internal/collector/` - read before adding or changing a collector.
- `internal/telemetry/` - read before changing the OTEL facade, semconv or the metrics catalog.
- `docs/coderabbit-sharded-review.md` - read before running a repo-wide CodeRabbit review.
- `spec/README.md` - read before refreshing the vendored OpenAPI spec.

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
