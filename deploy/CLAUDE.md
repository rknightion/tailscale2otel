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
- `grafana/tailscale2otel.json` — the **flagship** dashboard, and since #394 the **only** one:
  one comprehensive multi-tab dashboard using the Grafana **v2 schema**
  (`dashboard.grafana.app/v2`, Grafana 13+) with conditional rendering. **Generated** from
  `grafana/gen/` — edit the generator, not the JSON; regenerate with `python3
  grafana/gen/build.py --out grafana/tailscale2otel.json`. The generator is modular:
  `builder.py` (primitives + the sentinel registry), `variables.py`, `maps.py`, `tabs/*.py`
  (one module per tab), `build.py` (orchestrator). See `grafana/README.md`.
- `alerts/grafana-managed/` — **Grafana-managed** alert and recording rules as
  `rules.alerting.grafana.app/v0alpha1` manifests plus a folder manifest, pushed with
  `gcx resources push`. **Generated** from `alerts/gen/build_rules.py`. See `alerts/README.md`.

> **v2 only, Grafana 13+ only, and that is a hard product decision — do not add a v1/Classic
> path.** The four legacy classic-schema dashboards
> (`tailscale-{fleet,network,audit-events,exporter-health}.json`) were **deleted** in #394, and
> the Prometheus-ruler-format `alerts/tailscale2otel.rules.yaml` was deleted with the move to
> Grafana-managed rules. Both were hand-maintained and excluded from every drift gate, so they
> rotted unnoticed. A classic-schema copy of the flagship is not merely redundant: v1 cannot
> express `conditionalRendering`, so every feature-gated tab would render permanently EMPTY
> instead of hiding, which is worse than not shipping it. This is also why #409
> (publish to the Grafana community catalog, which requires a Classic export) closed as
> unsupported.

## Helm chart — config is single-source

Since chart **0.2.0** the entire app config lives under `values.yaml` `config:`. This is deliberate:
there is **no separate chart-specific config schema to keep in sync** — edit `config:` in
`values.yaml`, not the template. Secrets come from `secret:`/`existingSecret` and are injected as
`TS2OTEL_*` env vars that override the corresponding config fields at runtime (no `${VAR}`
placeholders).

**Since chart 0.14.0 the rendered `config.yaml` auto-routes** (SEC-07 / #470): credential-free →
ConfigMap `<fullname>`; any credential-bearing key set inline under `config:` (or
`configStorage.mode: secret`) → Secret `<fullname>-config`, the same mechanism multi-tailnet mode
already used. The authoritative key list is `tailscale2otel.credentialPaths` in
`templates/_helpers.tpl` — **add any new credential field in the app config to it**, nothing derives
it from the Go struct. `configStorage.mode: configmap` plus an inline credential makes
`helm template` fail on purpose. One helper, `tailscale2otel.configStoresSecret`, owns the decision
and both `fail` guards; every template routes through it. `deploy/helm/tests/render-tests.sh`
(bash + helm + yq, no cluster) asserts the whole contract, including that no credential reaches any
ConfigMap, annotation, label or arg — run it after touching the chart's config/secret plumbing.

**`rolloutTrigger`** (SEC-06 / #469) is the documented way to pick up a rotated externally managed
`existingSecret`: credentials arrive via `envFrom` and Kubernetes never refreshes env in a running
container, so the pod must be replaced. It is an opaque operator value, never derived from secret
content. Stakater Reloader via `podAnnotations` is the automated alternative. Config hot reload is
parked (#486) — do not add a SIGHUP handler or a reload route to make a config-reloader sidecar work.

**Since chart 0.5.0** the secret keys follow the systematic `TS2OTEL_` prefix + `__`-separated nesting
convention (e.g. `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET`). This is a BREAKING rename from the
old `TS_*`/`GC_*`/`ADMIN_TOKEN`/`PYROSCOPE_*` keys.

**Checkpoint persistence (chart 0.5.1+):** `config.checkpoint.store` defaults to `file`; the
checkpoint directory `/var/lib/tailscale2otel` is pre-seeded in the image (owned by uid 65532) and
mounted via an `emptyDir` by default. Set `persistence.enabled=true` to create a PVC for durable
storage across pod rescheduling. The app gracefully falls back to in-memory if the path is not
writable (a WARN is logged), so no crash occurs on misconfiguration.

**The same volume also backs the opt-in persistent flow store (#294).** `config.flows.store.directory`
is empty by default (memory-only). Pointed at a path under that mount — e.g.
`/var/lib/tailscale2otel/flows` — it stores one SQLite row per connection so `/flows` survives a
restart. It needs `persistence.enabled=true` to be durable, and the PVC must be sized for it ON TOP
of the checkpoint/WAL figure: the 64Mi default suits checkpoints alone. `fsGroup: 65532` already
makes a fresh PVC writable by the container, so no extra template change is needed. Unlike the
checkpoint store it does NOT fall back to memory when the path is unwritable — the flow view is
switched off instead, so an operator who asked for history is never shown one that looks like it.

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
- **A MAJOR bump needs the module path moved first — release-please will NOT do it.**
  `release-type: go` updates the manifest and changelog and nothing else, but Go requires a module
  released at v2+ to have a path ending `/vN`. Tagging a major against a stale path fails the
  GoReleaser `binaries` job (it proxies the tagged module under `gomod.proxy: true`), which is
  exactly how **v2.0.0 shipped with no archives, checksums, signatures or SBOMs** — image, chart and
  notices published fine, and re-running could not help because the *tag* carried the wrong path
  (#174). Run **`scripts/bump-module-major.sh`** (no argument infers the target from the manifest)
  and land it on `main` **before** merging the release PR; it rewrites every import across all four
  modules, fixes the tool modules' `require`/`replace`, runs `go mod tidy` everywhere, and leaves
  `CHANGELOG.md` alone (past releases really did ship under the old path).
  `TestModulePathMatchesReleaseVersion` (`internal/config`) is the backstop: the release PR is what
  bumps `.release-please-manifest.json`, so on that branch the manifest reads the new major while
  `go.mod` still reads the old one and the test fails **there** — on the one PR that must not merge
  unnoticed — instead of after the tag is cut. It tolerates the module leading the last release by
  exactly one major, which is the normal state between the pre-bump and the release.
- `cosign-installer` is pinned to `@v4.1.2` (no moving `v4` tag exists) and installs `cosign-release: v3.0.6`.

GoReleaser config is `/.goreleaser.yaml`; CI's `goreleaser-snapshot` job (`ci.yml`) runs
`release --snapshot --skip=publish,sign,sbom` so the image step is skipped on PRs.
