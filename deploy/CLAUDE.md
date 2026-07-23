# deploy

Packaging, deployment, and observability assets. None of this ships in the Go binary; it's all
consumed by operators or by the release pipelines.

## Layout

- `Dockerfile` — runtime image (built/smoke-tested in CI as `tailscale2otel:ci`), and the
  Dockerfile the **published** multi-arch GHCR image is built from (via `publish.yml`'s `image`
  job → the shared `container-publish.yml` reusable). There is no separate GoReleaser Dockerfile —
  a `Dockerfile.goreleaser` + `.goreleaser.yaml` `dockers_v2`/`docker_signs` pair existed
  previously but was dead code (unreachable on every real CI path) and was removed; GoReleaser
  now only builds cross-compiled binaries (see Release/publish pipelines below).
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

Two files in the chart are **generated and drift-checked in CI** (the `Helm` workflow) — regenerate
with `scripts/regen-generated.sh helm`, do not hand-edit:

- `values.schema.json` — JSON Schema **draft-07** (Helm only validates draft-07), generated from
  `values.yaml` by `losisin/helm-values-schema-json-action` (which installs tool **v2.5.0**).
- `README.md` — generated from `README.md.gotmpl` + value annotations by `helm-docs` (**v1.14.2**).

**Both tools are version-pinned** — a different local version silently generates different output.
Install the pinned pair once per machine with `scripts/regen-generated.sh tools`; the script verifies
the version before regenerating and SKIPs loudly rather than writing a wrong file. See the root
`CLAUDE.md` (and the script header) for the pins and the helm-docs ldflag gotcha.

CI also runs `configcheck` over the chart-rendered config, so a `values.yaml` `config:` change that
violates a cross-field rule (e.g. poll+stream on one log type) fails the Helm workflow, not just the app.

Local sanity checks:
```sh
helm lint deploy/helm/tailscale2otel
helm template t deploy/helm/tailscale2otel | less
```

Bump `Chart.yaml` `version` on any chart change; `appVersion` tracks the app version the chart defaults to.

## No Kubernetes Service (deliberate)

The chart intentionally ships **no `Service`**. tailscale2otel is a singleton poller whose normal
traffic is **outbound only** (it polls the Tailscale API and pushes OTLP); nothing needs to reach it
in the default deployment. Every inbound listener — `admin` (probes/status), `prometheus` (`/metrics`),
`streaming` (HEC receiver), `webhook` — is **opt-in and off by default**. The `webhook` receiver still
fails *open* (an empty `webhook.secret` skips HMAC verification), and `prometheus` serves every series
unauthenticated when `prometheus.auth.token` is empty. The `streaming` receiver and the `admin` status
page now fail *closed* on a non-loopback bind with no credential (403), but a Service that exposed
the webhook or Prometheus port would still risk publishing an unauthenticated endpoint, so the safe
default is to expose nothing. Liveness/readiness use the admin port directly (no Service needed). Operators who enable a
listener should expose **only that one** via their own `Service`/`Ingress`/`ServiceMonitor` (and set
the matching `*.auth.token` / `*.secret`). A future opt-in, per-listener `service.enabled` block could
be added if demand warrants — but it must default off and never map a receiver port without its
credential.

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
  changelog in `/CHANGELOG.md`), authored via a PAT (`RELEASE_PLEASE_TOKEN`) so its CI runs
  unattended. Merging that PR creates the GitHub Release + a `vX.Y.Z` tag and sets
  `release_created=true`, which gates two follow-on jobs in the **same** workflow (so the default
  `GITHUB_TOKEN` suffices for those two — no PAT/App, no second workflow to trigger):
  - **`publish`** calls `publish.yml` (`release_tag` set) → the shared `container-publish.yml`
    reusable builds + pushes the multi-arch `deploy/Dockerfile` image to `ghcr.io` (native
    amd64+arm64, cosign keyless signing, provenance, syft SBOM, Trivy) and pushes the Helm chart
    as an OCI artifact to `oci://ghcr.io/rknightion/charts`; `publish.yml`'s `notices` job also
    generates + uploads `THIRD_PARTY_NOTICES.md` to the release.
  - **`binaries`** calls the shared `binaries.yml` reusable, which runs THIS `.goreleaser.yaml`
    with `--skip=docker` — GoReleaser only builds the cross-compiled archives, `SHA256SUMS`, and
    per-archive SBOMs, cosign-signs the checksums, and uploads them to the release-please release
    (it does not overwrite the release notes — release-please owns the changelog). GoReleaser has
    **no docker pipeline** in this repo (a `dockers_v2`/`docker_signs` pair was removed as dead
    code — see `.goreleaser.yaml`'s header); the image is built exclusively by `publish`/`edge`.
  - **`edge`** (when `release_created` is NOT true, i.e. every other push to main) calls the same
    `publish.yml` with an empty `release_tag`, publishing a `:main`-ish snapshot image + chart.
    This replaces the old, now-deleted `main-publish.yml`.
  **There is no manual tagging** — never `git tag`/push a `v*` tag by hand.
- `cosign-installer` is pinned to `@v4.1.2` (no moving `v4` tag exists) and installs `cosign-release: v3.0.6`.

GoReleaser config is `/.goreleaser.yaml`; CI's `goreleaser-snapshot` job (`ci.yml`) runs
`release --snapshot --skip=publish,sign,sbom` so the image step is skipped on PRs.
