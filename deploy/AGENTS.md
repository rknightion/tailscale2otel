# deploy

Packaging, deployment and observability assets. None of it ships in the Go binary.

How the generated `grafana/` and `alerts/` trees reach the live stack is
`reference/grafana-delivery.md` at the repo root.

## Helm chart

The whole app config lives under `values.yaml` `config:`. There is no chart-specific config schema
to keep in sync - edit `config:`, never the template. `TestHelmValuesCoverEveryKey` fails when that
block drifts from `config.Default()`.

- The rendered `config.yaml` auto-routes: credential-free goes to ConfigMap `<fullname>`, anything
  credential-bearing set inline (or `configStorage.mode: secret`) goes to Secret
  `<fullname>-config`. **The authoritative key list is `tailscale2otel.credentialPaths` in
  `templates/_helpers.tpl` - add every new credential field in the app config to it.** Nothing
  derives it from the Go struct, so a missed key silently publishes a credential in a ConfigMap.
  One helper, `tailscale2otel.configStoresSecret`, owns the decision and both `fail` guards.
- `helm/tests/render-tests.sh` (bash + helm + yq, no cluster) asserts the whole config/secret
  contract, including that no credential reaches any ConfigMap, annotation, label or arg. Run it
  after touching that plumbing; `just helm-lint` runs it.
- No pod-template annotation may hash secret material (GHSA-825f-hph6-x65w): a workload-read
  principal can verify offline guesses against it. `checksum/config` renders only for a
  credential-free ConfigMap-backed config; there is no `checksum/secret`.
- `rolloutTrigger` is the supported way to pick up any secret-bearing change (a rotated
  `existingSecret`, a changed inline `secret:`, an edited secret-backed config). Kubernetes never
  refreshes env vars or Secret mounts in a running container. It is an opaque operator value, never
  derived from secret content. Stakater Reloader via `podAnnotations` is the automated alternative.
- Config hot reload is parked. Do not add a SIGHUP handler or a reload route to make a
  config-reloader sidecar work.
- Secret keys are `TS2OTEL_` + the dotted config path with `__` between levels.
- `values.schema.json` (JSON Schema **draft-07** - Helm validates nothing newer) and `README.md`
  are generated and drift-checked; `just gen-helm` rebuilds them. Chart-authored objects are
  `additionalProperties: false`, and root strictness lives in the `just gen-helm-schema` flags plus
  `.github/workflows/helm.yml`, not in a `values.yaml` annotation - those two must move together or
  local regeneration drifts from CI.
- **The chart README deliberately has no AppVersion badge - do not "restore" it.** It uses
  `chart.versionBadge` + `chart.typeBadge`, not `chart.badgesSection`. An AppVersion badge bakes a
  release-managed version into a generated file: release-please bumps `Chart.yaml` `appVersion` but
  cannot run helm-docs, so the fail-on-diff gate goes red on the release PR itself, every release.
  Annotating the badge does not fix it - the version appears twice on the line and release-please's
  `VERSION_REGEX` has no `g` flag. `Chart.yaml` is what ArtifactHub and `helm show chart` read.
- Bump `Chart.yaml` `version` on any chart change. `appVersion` is release-please's.
- `configcheck` runs over the chart-rendered config, so a `values.yaml` `config:` change that
  violates a cross-field rule fails the Helm workflow, not just the app.

### Persistence

`config.checkpoint.store` controls poll cursors and `config.checkpoint.evidence_store`
independently controls ACL provenance; both default to `file` and share
`config.checkpoint.file_path`. `/var/lib/tailscale2otel` is pre-seeded in the image owned by
65532 and mounted as an `emptyDir` unless `persistence.enabled=true`. `fsGroup: 65532` already
makes a fresh PVC writable, so a new persistent path needs no template change.

The same volume backs the opt-in persistent flow store (`config.flows.store.directory`, empty by
default) and the receiver ingress WAL. **Size the PVC for those on top of checkpoints** - the
default suits checkpoints alone.

The checkpoint store falls back to memory with a WARN when its path is unwritable. The flow store
does not: the flow view is switched off instead, so an operator who asked for history is never
shown one that looks complete.

## Listeners, Services and credentials

Every inbound listener - `admin`, `prometheus`, `streaming` (HEC), `webhook` - is opt-in and off by
default except `admin`. Normal traffic is outbound only.

- Services are per-listener and every one defaults off. **Never an aggregate Service**: it would map
  ports whose listeners carry different credentials, so enabling one surface publishes the others.
  `type` defaults to `ClusterIP` for all four; a `LoadBalancer` default would put a receiver on the
  public internet from one `enabled: true`. Ingress and HTTPRoute exist for `streaming` and
  `webhook` only, never for `admin` or `prometheus` at any value.
- A network-reachable bind with no credential fails closed, at config `Validate()` for `webhook` and
  `streaming` and with a 403 for `admin` and `prometheus`. The one acknowledged escape is
  `prometheus.auth.allow_unauthenticated`, for an in-cluster scraper behind a NetworkPolicy; it
  covers the no-token case only. On loopback the same combination is a warning, not an error.
- Liveness/readiness use the admin port directly and need no Service.
- A metrics monitor (`PodMonitor`/`ServiceMonitor`) over an unauthenticated network-reachable
  `/metrics` fails to render, because it would scrape a 403 forever and read as a broken exporter.

## Profiling

Two paths to an o11y backend, both opt-in: **pull** - point Alloy's `pyroscope.scrape` at the admin
`/debug/pprof`; **push** - `config.profiling.pyroscope`, where Grafana Cloud Profiles needs
`basic_auth_user` set to the profiles instance ID.

## Release and publish

`release-please.yml` maintains the release PR from Conventional Commits and, on merge, cuts the
release and tag in the same workflow. Its token is a per-run GitHub App installation token minted
from the OpenBao broker over the tailnet, so a failure in that step is infrastructure, not the
commit. The follow-on jobs run on the default `GITHUB_TOKEN`.

- `publish` (release) and `edge` (every other push to main) both call `publish.yml`, which builds
  the multi-arch image from `deploy/Dockerfile` and pushes the chart as an OCI artifact.
  **`edge` carries `if: always()`** - without it a broker outage skipped the job and `:main` silently
  stopped being rebuilt while every check stayed green.
- `binaries` runs `.goreleaser.yaml` with `--skip=docker`. **`.goreleaser.yaml` deliberately has no
  docker pipeline**, and there is no `Dockerfile.goreleaser`; the image is built exclusively by
  `publish`/`edge`. Do not re-add one.
- **There is no manual tagging.** Never `git tag` or push a `v*` tag by hand.
- `verify-release` reads the published release back and fails when assets are missing. It runs under
  `always()` on purpose: a failed uploader is exactly when completeness needs reporting.

## Deeper references

- `reference/dashboard-generator.md` - read before editing anything under `grafana/gen/` or
  `alerts/gen/`, renaming a panel, or adding an alert `panel=` link.
