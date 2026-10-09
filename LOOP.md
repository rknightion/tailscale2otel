# Loop: tailscale2otel
tier: guarded
gate: just check
ci-required: ci-success, helm-success
release-on-push: yes
deploy-on-push: yes
receiver: https://loopwatch.m7kni.com
grafana-stack: robknight

Public repository: no lab name, address, identifier, credential or observability capture in any
tracked file, `backlog/` included. Run `just` with stdin from `/dev/null`. `just setup` once per
clone installs the pinned tools; a local lint or vuln result counts only when its pin assertion
passed. `just ci` adds the goreleaser cross-compile and the image and smoke legs.

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

- Go patch releases can turn `just check` red through `just vuln` for standard-library
  vulnerabilities. Raise the `go` directive in all five modules and upgrade local Go to the
  patched release; never weaken or edit the gate to bypass the vulnerability.

- `go test -race ./...` at the root stops at the module boundary. The four tool modules are covered
  by the `module-verify` and `lint` matrices in ci.yml, and a workflow contract test fails if one
  drops out.
- A breaking change that cuts a new major needs `just bump-major` landed on `main` before the
  release PR merges; release-please does not maintain the module path.
- Never hand-edit anything under `deploy/grafana` or `deploy/alerts`, or text between
  `<!-- BEGIN GENERATED -->` markers; run the matching `just gen-<family>`. Regenerating
  `internal/catalog/signal_dispositions.json` cannot turn a red coverage gate green.
- OpenTelemetry modules (core, metric and trace SDKs, `sdk/log` at v1.x, and the 0.x log
  exporters) move together in one Renovate PR; never bump one alone or run a casual `go get` or
  `go mod tidy`.
- Queries use the normalized Prometheus name: dots to underscores, `_total` on monotonic counters,
  unit suffixes, `_ratio` on unit-`1` gauges.
- Pick exactly one ingestion path per log type (`poll` or `stream`); both double-counts.
- Validate record-type changes against real captures in `.capture/`, not synthetic fixtures.
- A whole-repo CodeRabbit review exceeds the transport limit: use `just review-sharded`.
- Scheduled workflows (live contract, API drift, IANA freshness) can fail on upstream change alone.

## Mutexes

- One `grafana-sync` writes the stack at a time and is never cancelled mid-write.
