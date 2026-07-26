# Changelog

## 0.14.5 — Optional durable receiver acceptance

- New `config.ingress_wal` values expose the disabled-by-default, bounded local
  write-ahead log for authenticated streaming and webhook request bodies.
- The chart documents the local-only, at-least-once acknowledgement boundary:
  successful receiver ACK does not mean OTLP/backend acknowledgement, and crash
  replay can duplicate exported data.
- The existing process-state volume is reused. `emptyDir` does not survive pod
  replacement or rescheduling; use a PVC and request at least `512Mi` with the
  default 256 MiB encoded WAL ceiling.

## 0.14.3 — Receiver TLS and flow reporter trust

- `config.webhook.tls.cert_file` and `key_file` can terminate HTTPS in the
  exporter; both files are validated together. Leaving both empty keeps the
  existing reverse-proxy deployment path.
- `config.collectors.flowlogs.trusted_reporter_node_ids` and
  `trusted_reporter_tags` configure bounded reporter-trust diagnostics. Tag
  trust comes only from authoritative device metadata.
- The existing `extraVolumes`/`extraVolumeMounts` path now documents mounting
  one TLS Secret for either receiver. No certificate issuance or ACME behavior
  is added.

## 0.14.0 — Inline credentials never land in a ConfigMap; a supported rotation path

Two security fixes, neither requiring operator action on upgrade.

### Credentials auto-route to a Secret (#470)

A ConfigMap is readable by anyone holding `get configmaps` in the namespace, which is
routinely granted far more widely than `get secrets`. Previously any single-tailnet
config was rendered into a ConfigMap verbatim — credentials included — even though the
config surface carries OAuth secrets, API keys, OTLP headers, object-store credentials
and several tokens.

Now, if any credential-bearing key is set inline under `config:`, the whole rendered
`config.yaml` moves into Secret `<fullname>-config` and the pod mounts it from there.
This reuses the mechanism multi-tailnet mode already used, so the object name, mount
path and container args are unchanged. Credential-free configs keep the ConfigMap.

### What changed

- New `configStorage.mode` (`auto` | `secret` | `configmap`, default `auto`). `auto`
  picks the ConfigMap for credential-free configs and a Secret otherwise. `configmap`
  combined with an inline credential makes `helm template` fail and names the offending
  keys rather than writing them somewhere too readable.
- New `rolloutTrigger` (default `""`), surfaced as pod annotation
  `tailscale2otel.m7kni.io/rollout-trigger` when set. This is the supported way to pick
  up a rotated externally managed `existingSecret` (#469): credentials arrive via
  `envFrom`, and Kubernetes never refreshes environment variables in a running
  container, so the pod must be replaced. Set it to any opaque value that changes —
  never a secret value or a hash of one. Clusters running Stakater Reloader can instead
  set `podAnnotations."reloader.stakater.com/auto"="true"`.
- New `config.tailscale.max_response_bytes` (4 MiB) and
  `config.tailscale.max_log_response_bytes` (32 MiB), tracking the app's new response
  decode budgets.
- `deploy/helm/tests/render-tests.sh` asserts the whole contract without a cluster.

### Upgrading

No action required. Operators who had credentials inline will see the `<fullname>`
ConfigMap disappear and a `<fullname>-config` Secret appear; the pod sees byte-identical
config at the same path. To keep the old placement, set `configStorage.mode: configmap`
— but that render now fails if credentials are inline, which is the point.

## 0.5.1 — Checkpoint persistence out of the box

The app now defaults `checkpoint.store` to `file` (was `memory`), persisting window
cursors atomically at `/var/lib/tailscale2otel/checkpoints.json`. This chart release
mirrors that default and wires up the volume:

### What changed

- `config.checkpoint.store` now defaults to `file` in `values.yaml` (matching the
  app default). If a restart loses the directory the app logs a WARN and falls back
  to in-memory checkpoints gracefully — it never crashes on a missing/unwritable path.
- A new top-level `persistence:` block controls the checkpoint volume:
  - `persistence.enabled: false` (default) — mounts an `emptyDir` at
    `/var/lib/tailscale2otel`. Checkpoints survive container restarts within a pod but
    are lost if the pod is rescheduled onto a different node.
  - `persistence.enabled: true` — creates a `PersistentVolumeClaim` (or uses
    `persistence.existingClaim`) and mounts it at the same path for fully durable
    checkpoint storage across rescheduling.
- `templates/pvc.yaml` creates the PVC when `persistence.enabled` and no
  `existingClaim` is set.
- The Dockerfile pre-seeds `/var/lib/tailscale2otel` owned by `65532:65532`
  (distroless `nonroot`) so the default checkpoint path is writable inside the
  container even with no mounted volume.

### Migration

No action required for existing installs: the `emptyDir` behaviour is equivalent to
the previous `checkpoint.store: memory` default. To enable durable checkpoints:

```yaml
persistence:
  enabled: true
  size: 64Mi          # checkpoints are a few KB; this is generous
  # storageClass: ""  # leave empty for cluster default
```

Or keep the default `emptyDir` and just let the app fall back to in-memory on
rescheduling (initial_lookback re-runs once, no data loss on the OTLP side).

## 0.5.0 — BREAKING: environment-driven config, no more `${ENV}` placeholders

The app moved to a layered config loader (built-in defaults < the `config:`
ConfigMap < `TS2OTEL_*` environment variables) and dropped `${ENV}` expansion
inside YAML. The chart was updated to match.

### What changed

- The `config:` block in `values.yaml` mirrors the restructured schema:
  `cardinality` is now nested as `cardinality.flow.*` and `cardinality.per_entity.*`
  (and the flow rollup top-N moved to `cardinality.flow.rollup_top_n`), and every
  collector is fully nested per-field. The block no longer contains any `${ENV}`
  secret placeholders.
- The `secret:` block keys are renamed to the systematic `TS2OTEL_*` names (e.g.
  `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET`, `TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN`,
  `TS2OTEL_ADMIN__AUTH__TOKEN`). They are still injected via the Kubernetes Secret +
  `envFrom`; because the env layer overrides the file, secrets never appear in the
  ConfigMap.

### Migration

- Rename your `secret:` keys (and any `existingSecret` keys) to the `TS2OTEL_*`
  convention. No other action is required: the ConfigMap is regenerated from the
  restructured `config:` block.

## 0.2.0 — BREAKING: config now lives under `config:`

The entire application config now lives under a single top-level `config:` key in
`values.yaml` and is rendered verbatim into the ConfigMap. This is the single
source of truth (kept in sync with `config.example.yaml`), eliminating the
chart<->config drift that existed when the `tailscale2otel.config` helper
hand-rolled a fallback config from individual values.

### What changed

- The `tailscale2otel.config` helper is now **unconditional**: it always renders
  `.Values.config | toYaml`. The hand-rolled `else` fallback branch (which
  duplicated config keys and drifted from `config.example.yaml`) was deleted.
- `values.yaml` now carries the FULL config map under `config:`, with `${ENV}`
  placeholders for secrets. The existing Secret + `envFrom` injection still
  expands them at runtime — no change to how credentials are supplied.
- `values.schema.json` (JSON Schema **draft-07**, the only draft Helm validates)
  was added as a baseline. CI regenerates it with
  [losisin/helm-values-schema-json](https://github.com/losisin/helm-values-schema-json)
  in fail-on-diff mode.

### Migration

If you previously overrode config via the old per-field values
(`tailscale.tailnet`, `tailscale.authMethod`, `otlp.protocol`, `otlp.endpoint`,
`otlp.metricInterval`, `selfObservability.enabled`), move those under `config:`
using the real config keys. Helm deep-merges maps, so you only need to set the
keys you change. Examples:

```yaml
# OLD (pre-0.2.0)
otlp:
  endpoint: "https://my-otlp.example/otlp"
  metricInterval: 30s
selfObservability:
  enabled: false

# NEW (0.2.0+)
config:
  otlp:
    endpoint: "https://my-otlp.example/otlp"
    metric_interval: 30s
  self_observability:
    enabled: false
```

Or via `--set`:

```sh
helm upgrade ... \
  --set config.otlp.endpoint=https://my-otlp.example/otlp \
  --set config.otlp.metric_interval=30s \
  --set config.self_observability.enabled=false
```

Secrets are unchanged: set `secret.TS_OAUTH_CLIENT_ID`, etc., or point
`existingSecret` at your own Secret.
