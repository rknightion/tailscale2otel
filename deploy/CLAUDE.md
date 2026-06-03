# deploy

Packaging, deployment, and observability assets. None of this ships in the Go binary; it's all
consumed by operators or by the release pipelines.

## Layout

- `Dockerfile` — runtime image (built/smoke-tested in CI as `tailscale2otel:ci`).
  `Dockerfile.goreleaser` — the variant GoReleaser uses for the published multi-arch image.
- `docker-compose.yaml` — local/single-host run (this is how it's deployed on `node-a`).
- `helm/tailscale2otel/` — Helm chart (see below).
- `grafana/*.json` — four importable Grafana 13 dashboards (fleet, network, audit-events,
  exporter-health). Datasource-agnostic via `${DS_PROM}`/`${DS_LOKI}` vars. See `grafana/README.md`.
- `alerts/tailscale2otel.rules.yaml` — Prometheus/Grafana alert rules. See `alerts/README.md`.

## Helm chart — config is single-source

Since chart **0.2.0** the entire app config lives under `values.yaml` `config:` (it is rendered
verbatim into the ConfigMap's `config.yaml`). This is deliberate: there is **no separate chart-specific
config schema to keep in sync** — edit `config:` in `values.yaml`, not the template. Secrets come from
`secret:`/`existingSecret` and are injected as env vars (the config references them via `${ENV}`).

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

## Release / publish pipelines

- `release.yml` (on tag) — GoReleaser builds binaries + a multi-arch image to `ghcr.io`, **cosign**
  keyless-signs (GitHub OIDC) the image + checksums, generates SBOMs (syft), and pushes the Helm chart
  as an OCI artifact to `oci://ghcr.io/rknightion/charts`.
- `main-publish.yml` (on push to main) — the snapshot equivalent: publishes `:main`-ish image + chart.
- `cosign-installer` is pinned to `@v4.1.2` (no moving `v4` tag exists) and installs `cosign-release: v3.0.6`.

GoReleaser config is `/.goreleaser.yaml`; CI's `goreleaser-snapshot` job (`ci.yml`) runs
`release --snapshot --skip=publish,sign,sbom` so the image step is skipped on PRs.
