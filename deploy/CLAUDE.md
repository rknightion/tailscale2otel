# deploy

Packaging, deployment, and observability assets. None of this ships in the Go binary; it's all
consumed by operators or by the release pipelines.

## Layout

- `Dockerfile` — runtime image (built/smoke-tested in CI as `tailscale2otel:ci`).
  `Dockerfile.goreleaser` — the variant GoReleaser uses for the published multi-arch image.
- `docker-compose.yaml` — local/single-host run (this is how it's deployed on `node-a`).
- `helm/tailscale2otel/` — Helm chart (see below).
- `grafana/tailscale2otel.json` — the **flagship** dashboard: one comprehensive multi-tab
  dashboard using the Grafana **v2 schema** (`dashboard.grafana.app/v2`, Grafana 13+) with
  conditional rendering. **Generated** from `grafana/gen/build.py` (dashboards-as-code, stdlib
  Python) — edit the generator, not the JSON; regenerate with `python3 grafana/gen/build.py --out
  grafana/tailscale2otel.json`. Plus four **legacy** standalone classic-schema dashboards
  (`tailscale-{fleet,network,audit-events,exporter-health}.json`, datasource-agnostic via
  `${DS_PROM}`/`${DS_LOKI}`). See `grafana/README.md`.
- `alerts/tailscale2otel.rules.yaml` — Prometheus/Grafana alert rules. See `alerts/README.md`.

## Helm chart — config is single-source

Since chart **0.2.0** the entire app config lives under `values.yaml` `config:` (it is rendered
verbatim into the ConfigMap's `config.yaml`). This is deliberate: there is **no separate chart-specific
config schema to keep in sync** — edit `config:` in `values.yaml`, not the template. Secrets come from
`secret:`/`existingSecret` and are injected as `TS2OTEL_*` env vars that override the corresponding
config fields at runtime — secrets never appear in the ConfigMap (no `${VAR}` placeholders).

**Since chart 0.5.0** the secret keys follow the systematic `TS2OTEL_` prefix + `__`-separated nesting
convention (e.g. `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET`). This is a BREAKING rename from the
old `TS_*`/`GC_*`/`ADMIN_TOKEN`/`PYROSCOPE_*` keys.

**Checkpoint persistence (chart 0.5.1+):** `config.checkpoint.store` defaults to `file`; the
checkpoint directory `/var/lib/tailscale2otel` is pre-seeded in the image (owned by uid 65532) and
mounted via an `emptyDir` by default. Set `persistence.enabled=true` to create a PVC for durable
storage across pod rescheduling. The app gracefully falls back to in-memory if the path is not
writable (a WARN is logged), so no crash occurs on misconfiguration.

Two files in the chart are **generated and drift-checked in CI** (the `Helm` workflow) — regenerate by
matching the actions in `.github/workflows/helm.yml`, do not hand-edit:

- `values.schema.json` — JSON Schema **draft-07** (Helm only validates draft-07), generated from
  `values.yaml` by `losisin/helm-values-schema-json-action`.
- `README.md` — generated from `README.md.gotmpl` + value annotations by `helm-docs`.

CI also runs `configcheck` over the chart-rendered config, so a `values.yaml` `config:` change that
violates a cross-field rule (e.g. poll+stream on one log type) fails the Helm workflow, not just the app.

Local sanity checks:
```sh
helm lint deploy/helm/tailscale2otel
helm template t deploy/helm/tailscale2otel | less
```

Bump `Chart.yaml` `version` on any chart change; `appVersion` tracks the app version the chart defaults to.

## Admin & profiling endpoints

The binary's admin server (chart `config.admin`) serves `/healthz` + `/readyz` probes, a human status
landing page at `/` (+ machine-readable `/api/status.json`) when `admin.landing_page` is true (default),
and `/debug/pprof` when `profiling.pprof.enabled` (pprof mounts on the admin server, so it requires
`admin.enabled`). Two profiling paths for an o11y backend, both opt-in/off by default:
**pull** — point Grafana Alloy's `pyroscope.scrape` at the admin `/debug/pprof`; or
**push** — set `config.profiling.pyroscope` (Grafana Cloud Profiles needs `basic_auth_user` = the
profiles instance ID and `basic_auth_password` = a `profiles:write` access-policy token).

## Release / publish pipelines

- `release-please.yml` (on push to main) — **release-please** maintains a release PR from the
  Conventional Commit history (config: `/release-please-config.json` + `/.release-please-manifest.json`,
  changelog in `/CHANGELOG.md`). Merging that PR creates the GitHub Release + a `vX.Y.Z` tag and sets
  `release_created=true`, which gates two follow-on jobs in the **same** workflow (so the default
  `GITHUB_TOKEN` suffices — no PAT/App, no second workflow to trigger): **GoReleaser** builds the
  binaries + multi-arch image to `ghcr.io`, **cosign** keyless-signs (GitHub OIDC) the image + checksums,
  generates SBOMs (syft), and **uploads the binaries to the release-please release** (it does not
  overwrite the notes — release-please owns the changelog); then the Helm chart is pushed as an OCI
  artifact to `oci://ghcr.io/rknightion/charts`. **There is no manual tagging** — never `git tag`/push a
  `v*` tag by hand.
- `main-publish.yml` (on push to main) — the snapshot equivalent: publishes `:main`-ish image + chart.
- `cosign-installer` is pinned to `@v4.1.2` (no moving `v4` tag exists) and installs `cosign-release: v3.0.6`.

GoReleaser config is `/.goreleaser.yaml`; CI's `goreleaser-snapshot` job (`ci.yml`) runs
`release --snapshot --skip=publish,sign,sbom` so the image step is skipped on PRs.
