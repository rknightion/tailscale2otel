# Loop: tailscale2otel
tier: guarded
gate: just check
ci-required: ci-success, helm-success
release-on-push: yes
deploy-on-push: yes
grafana-stack: robknight
tracker: github
landing: pr

Public repository: no lab name, address, identifier, credential or observability capture in any
tracked file, issue or pull request. Run `just` with stdin from `/dev/null`. `just setup` once per
clone installs the pinned tools; a local lint or vuln result counts only when its pin assertion
passed. `just ci` adds the goreleaser cross-compile and the image and smoke legs.

The tracker working model is `~/repos/agent-docs/docs/github-tracker.md` (maintainers only).

## Queue

- `just tracker queue` lists open issues at Project Status Ready on the rknightion Project; Parked
  work sits at Needs input. A charter selects by labels (`type:task`, `type:bug`, `type:feature`,
  `needs-triage`) and Project fields; never queue a `needs-triage` issue.
- Only the root writes issues and the Project, through `just tracker`; a lane returns the status
  and AC ticks it wants.
- Name issues as `#N`. A legacy id (`TSO-0001`) resolves with `just issue <ID>`
  (`.github/tracker/id-map.json`). A bare `#N` below 795 is a pull request, a bot issue or a
  deleted pre-board issue (`archive/README.md`); imported tasks start at #795.
- Drift workflows file and auto-close bot issues by their own lane labels: never put such a label on
  a task, never close a bot issue by hand.

## Credentials

- Pushing alert rules to the Grafana stack is pre-authorized: `gcx resources push -p
  deploy/alerts/grafana-managed`, and deleting a rule the repo no longer ships. That never extends
  to mutating the tailnet.
- Never push dashboards with `gcx`; `grafana-sync` delivers them into the GitSync repository.
- `prune-rules` and `bump-major` are confirm-gated and mutate outside the tree: never pass `--yes`
  or `JUST_YES=1`.
- Tailscale and Pyroscope credentials are environment-only. `auto_configure` must never target a
  real tailnet.

## Traps

- A Go patch release can turn `just vuln` red on a standard-library vulnerability: raise the `go`
  directive in all five modules and upgrade local Go; never weaken the gate.

- `golangci-lint` and `govulncheck` install into the shared `~/go/bin`, and another repo's
  `just setup` can overwrite them with a different pin. Run `just setup </dev/null` before any
  composed or post-land gate and again if `just lint` or `just vuln` fails its version assertion;
  never edit the pin or assertion. A mise shim precedes `~/go/bin` on PATH, so run the gate as
  `PATH="$HOME/go/bin:$PATH" just check </dev/null`.

- A `.go` file in gitignored `codex/` outside a nested `go.mod` joins the root module and reddens
  `fmt-check` and `vet`. Run every composed or post-land gate in a fresh detached worktree of the
  exact SHA, save Go evidence as `.go.txt`, never exclude `codex/` in `.golangci.yml` or the justfile.

- `go test -race ./...` at the root stops at the module boundary. The four tool modules are covered
  by the `module-verify` and `lint` matrices in ci.yml, and a workflow contract test fails if one
  drops out.
- A breaking change that cuts a new major needs `just bump-major` landed on `main` before the
  release PR merges; release-please does not maintain the module path.
- Never hand-edit a generated dashboard or rule file under `deploy/grafana` or `deploy/alerts`
  (the two `README.md` files and the `gen/` and `tests/` sources there are hand-maintained), or text between
  `<!-- BEGIN GENERATED -->` markers; run the matching `just gen-<family>`. Regenerating
  `internal/catalog/signal_dispositions.json` cannot turn a red coverage gate green.
- OpenTelemetry modules (core, metric and trace SDKs, `sdk/log` at v1.x, and the 0.x log
  exporters) move together in one Renovate PR; never bump one alone or run a casual `go get` or
  `go mod tidy`.
- Queries use the normalized Prometheus name: dots to underscores, `_total` on monotonic counters,
  unit suffixes, `_ratio` on unit-`1` gauges.
- Pick exactly one ingestion path per log type (`poll` or `stream`); both double-counts.
- Validate record-type changes against real captures in `.capture/`, not synthetic fixtures.
- Code slices require the combined Kodus and independent local exact-candidate review in
  the maintainer code-review contract. Include generated and new files in candidate coverage;
  missing or oversized review is no verdict, never clean.
- `helm-success` exists only for pushes touching `helm.yml`'s push paths (`deploy/helm/**`,
  `config.example.yaml`, `internal/config/**`, `tools/configcheck/**`, `helm.yml` itself; no
  `workflow_dispatch`);
  any other push to `main` never gets one. Record it as absent, not a pass,
  and do not make a source edit to trigger it. Pull requests always report it.
- The live spec endpoint emits YAML, but `spec/tailscale-api.json` stays JSON: re-vendor with
  `curl ... | yq -o=json '.'` (`spec/README.md`). `internal/oas.ParseSpec` keeps only GET operations.
- More traps in `reference/ci-gates.md`: dashboards-drift, gen-check staging, the Tailscale client
  path, grafana-sync rules.
- Scheduled workflows (live contract, API drift, IANA freshness) can fail on upstream change alone.
- Wall-clock flakes, auto-rc, reruns, release posture, RBAC proofs: `reference/ci-gates.md`.

## Mutexes

- gate: check | just setup </dev/null && PATH="$HOME/go/bin:$PATH" just check </dev/null
- One `grafana-sync` writes the stack at a time and is never cancelled mid-write.
- Pass the context to every `gcx` call (`--context m7kni`, `just verify-deploy m7kni`): a bare call
  may pick another stack. Live Grafana queries are read-only here: `gcx metrics|logs query` needs both `--from` and `--to`;
  an auth failure means use the stored token, not a gcx OAuth retry.

## Lane brief

- Forbid `git reset`, `git stash`, `git clean` and `go clean` on the shared tree by name, not just
  commit and push: lanes once destroyed others' uncommitted work (recover with
  `git fsck --lost-found`, tagging the dangling commit first).
- A lane adding a map or list config shape also owns the schema, Helm and env-loader seams, or the
  brief says the root does (`reference/generated-artifacts.md`).
