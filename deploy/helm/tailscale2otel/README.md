# tailscale2otel

![Version: 0.31.0](https://img.shields.io/badge/Version-0.31.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square)

Tailscale exporter for OpenTelemetry and Prometheus — device fleet, network flow logs and audit logs over OTLP. Grafana Cloud ready. Headscale supported.

**Homepage:** <https://m7kni.io/tailscale2otel/>

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| rknightion |  | <https://github.com/rknightion> |

## Source Code

* <https://github.com/rknightion/tailscale2otel>

## Configuration

The entire application config lives under a single top-level `config:` key in
`values.yaml` (the single source of truth, kept in sync with
`config.example.yaml`). It is rendered verbatim as `config.yaml` — into a ConfigMap
while it is credential-free, and into a Secret as soon as it is not (see below).
The supported path is to leave the credential fields empty and inject them as
`TS2OTEL_*` environment variables from the Secret.

Create the credential Secret before installing the chart. This keeps credential values out of the
Helm release, shell history, and process list:

```sh
kubectl create secret generic ts2otel-creds --from-env-file=creds.env

helm install t deploy/helm/tailscale2otel \
  --set-string existingSecret=ts2otel-creds \
  --set-string config.log_level=debug
```

Helm deep-merges maps, so non-secret single-key overrides work without restating the rest. Never put
a credential in an inline `--set secret.<KEY>` value.

See [CHANGELOG.md](./CHANGELOG.md) for the breaking 0.2.0 migration (config moved
under `config:`) and the 0.5.0 migration (secret keys renamed to `TS2OTEL_*`,
`${VAR}` placeholders removed from config).

### Receiver WAL durability and storage

`config.ingress_wal.enabled` is `false` by default, so receiver acceptance stays
stateless unless you opt in. When enabled, a successful receiver ACK means the
accepted payload has been stored durably by the local WAL: the raw authenticated
webhook body or fully validated decompressed streaming body. It does not mean OTLP
export or backend acknowledgement. Replay is at-least-once, so a crash after export
but before the local completion commit can duplicate data.

The existing `/var/lib/tailscale2otel` state mount holds both checkpoints and the
WAL. With `persistence.enabled=false` it is an `emptyDir`: data survives container
restarts within the same pod, but is lost when the pod is replaced, rescheduled,
or lost with its node. Set `persistence.enabled=true` to create a PVC, or combine
it with `persistence.existingClaim`, when WAL data must survive those events. WAL
entries contain sensitive raw or decompressed receiver payloads; do not expose,
share, or back them up without the same access controls as the source data.

The existing `persistence.size: 64Mi` default is retained for checkpoint-only
deployments. It is too small for the default 256 MiB encoded WAL ceiling. For that
WAL setting, request at least `512Mi` to leave room for entries, staging files,
and metadata:

```yaml
config:
  ingress_wal:
    enabled: true
  streaming:
    enabled: true
    max_body_bytes: 67108864
persistence:
  enabled: true
  size: 512Mi
```

### Credentials and the rendered config

A ConfigMap is readable by anyone holding `get configmaps` in the namespace, which
in practice is granted far more widely than `get secrets`. So the chart never puts
a credential in one: if any credential-bearing key is set inline under `config:`,
the **whole** rendered `config.yaml` moves into Secret `<fullname>-config` and the
pod mounts it from there instead. Nothing else changes — same file, same path, same
`-config` argument — and no credential value, digest or fragment is ever placed in a
pod annotation, label or command line.

The keys that trigger this:

| Key under `config:` | |
| --- | --- |
| `tailscale.auth.oauth.client_secret` | Tailscale OAuth client secret |
| `tailscale.auth.apikey` | Tailscale API key |
| `headscale.api_key` | Headscale bearer key |
| `otlp.grafana_cloud.token` | Grafana Cloud OTLP token |
| `otlp.headers` | any raw OTLP header (this is where an `Authorization` header goes) |
| `otlp.metrics.headers` / `otlp.logs.headers` / `otlp.traces.headers` | per-signal OTLP credentials |
| `collectors.flowlogs.objectstore.access_key_id` / `.secret_access_key` / `.session_token` | object-store credentials |
| `collectors.auditlogs.objectstore.access_key_id` / `.secret_access_key` / `.session_token` | object-store credentials |
| `collectors.k8s_audit.objectstore.access_key_id` / `.secret_access_key` / `.session_token` | object-store credentials |
| `streaming.token` | HEC receiver token |
| `webhook.secret` | webhook HMAC secret |
| `prometheus.auth.token` | `/metrics` token |
| `admin.auth.token` | admin/pprof token |
| `profiling.pyroscope.basic_auth_password` | Pyroscope password |
| `profiling.pyroscope.headers` | Pyroscope gateway credentials |
| `grafana_annotations.token` | Grafana annotation token |
| `enrichment.geoip.download.license_key` | MaxMind license key |
| `tailnets[]` (any entry) | multi-tailnet inline auth — the list is file-only, there is no `TS2OTEL_*` path for it |
| `collectors.node_metrics.targets[]` with `bearer_token` or `headers` | per-target scrape credentials |
| `streaming.routes[]` with `token` / `webhook.routes[]` with `secret` | per-route receiver credentials |

Identifiers are *not* on that list — `tailscale.auth.oauth.client_id`,
`tailscale.auth.workload_identity.client_id`, `otlp.grafana_cloud.instance_id` and
`profiling.pyroscope.basic_auth_user` are the identity halves of credential pairs,
not secrets, so setting them keeps the ConfigMap path. Nor are the `*_file` keys:
those are paths to material mounted from elsewhere, which is the cleanest option of
all (`readOnlyRootFilesystem` aside, pair them with `extraVolumes`).

`configStorage.mode` overrides the decision: `secret` always uses the Secret,
`configmap` always uses the ConfigMap — and makes `helm template` **fail**, naming
the offending keys, if a credential is set inline. That failure is deliberate: it is
the one combination the chart cannot make safe.

### Rotating credentials

The chart deliberately publishes no checksum of Secret material in the pod template:
anyone with workload-read could use such a digest to test guesses offline. Therefore
an inline `secret:`, a Secret-backed `config.yaml`, and an **`existingSecret` you
manage yourself** all require an explicit rollout when their values change. Kubernetes
does not refresh a running container's environment, and file-backed reload does not
cover environment variables. Two supported rollout paths:

```sh
# Manual: change rolloutTrigger to anything new. It is an opaque operator-chosen
# token surfaced as a pod annotation, so the pod template changes and the Deployment
# does a Recreate rollout. Never put a secret value (or a digest of one) here.
kubectl create secret generic ts2otel-creds --from-env-file=creds.env --dry-run=client -o yaml | kubectl apply -f -
helm upgrade t deploy/helm/tailscale2otel --reuse-values --set rolloutTrigger="$(date +%s)"
```

```yaml
# Automated: with Stakater Reloader running in the cluster, it watches the referenced
# Secret and issues a rollout RESTART when it changes — which is what env-injected
# credentials require.
existingSecret: ts2otel-creds
podAnnotations:
  reloader.stakater.com/auto: "true"
  # or, to watch one specific Secret:
  # secret.reloader.stakater.com/reload: ts2otel-creds
```

The `prometheus-config-reloader`/`configmap-reload` sidecar family does not fit
here: those watch a mounted *volume* and signal the process, which needs
file-mounted credentials and an in-process reload trigger. tailscale2otel reads its
configuration once at startup and handles only SIGINT/SIGTERM, so a restart *is* the
reload.

A replacement credential that turns out to be wrong is visible without exposing any
value: the exporter keeps running, its Tailscale API calls fail, and that shows up as
`tailscale2otel.api.requests` with an error outcome, collector scrape errors, and a
stale `tailscale2otel.enrich.cache_age` — plus the per-collector health on the admin
status page (`config.admin.landing_page`). Alert on those rather than assuming a
green rollout means a working credential.

### Mounting a TLS cert for a receiver

`readOnlyRootFilesystem: true` means there is no writable path inside the pod to
place arbitrary files, so `config.streaming.tls.cert_file`/`key_file` and
`config.webhook.tls.cert_file`/`key_file` need an
explicit mount. Use `extraVolumes`/`extraVolumeMounts` to wire in a `kubernetes.io/tls`
Secret (or any other file-backed Secret/ConfigMap) without forking the chart:

```yaml
config:
  streaming:
    enabled: true
    tls:
      cert_file: /etc/tailscale2otel/tls/tls.crt
      key_file: /etc/tailscale2otel/tls/tls.key
  webhook:
    enabled: true
    tls:
      cert_file: /etc/tailscale2otel/tls/tls.crt
      key_file: /etc/tailscale2otel/tls/tls.key

extraVolumes:
  - name: streaming-tls
    secret:
      secretName: tailscale2otel-streaming-tls

extraVolumeMounts:
  - name: streaming-tls
    mountPath: /etc/tailscale2otel/tls
    readOnly: true
```

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` | Affinity rules for pod scheduling. |
| config.admin.auth.token | string | `""` | Shared secret gating the status page and pprof (HTTP Basic password or "Authorization: Bearer <token>"); /healthz and /readyz stay open. Set via TS2OTEL_ADMIN__AUTH__TOKEN (secret). Required when profiling.pprof.enabled. |
| config.admin.auth.token_file | string | `""` | Read admin.auth.token from this path instead of an inline value (mounted-Secret style). Set the value or the file, not both; the file's content is whitespace-trimmed. |
| config.admin.enabled | bool | `true` | Enable the admin probe server. |
| config.admin.landing_page | bool | `true` | Serve the human status page at / and machine-readable JSON at /api/status.json. |
| config.admin.listen | string | `":9091"` | Address the admin server binds; serves /healthz and /readyz. Bind to loopback/tailnet for defense-in-depth. |
| config.admin.status_refresh_interval | string | `"5s"` | How often the status page's JS re-polls /api/status.json to patch the live view. The page's 1s freshness ticker is independent of this. |
| config.admin.tls.cert_file | string | `""` | HTTPS certificate for the admin server. Set together with key_file (both-or-neither); leaving both empty serves plain HTTP. The chart's liveness/readiness probes follow this automatically: setting both files renders them with scheme: HTTPS (#342). Setting admin TLS through TS2OTEL_ADMIN__TLS__* env vars instead is invisible to the chart and leaves the probes on HTTP — use these config keys. |
| config.admin.tls.key_file | string | `""` | HTTPS private key paired with cert_file. Both paths must exist and be readable at startup. |
| config.cardinality.critical_threshold | int | `8000` | Status-page cardinality view flags a metric critically at/above this count (>= warning_threshold; <= metric_limit when set; 0 disables). |
| config.cardinality.derp_region_rollup | bool | `true` | Emit per-DERP-region rollup gauges (tailscale.derp.region.*) on the devices collector. |
| config.cardinality.flow.collapse_external | bool | `true` | Bucket unresolved IPs as external/unknown to cap cardinality. Affects flow LOGS and, when node_dims is true, the src/dst node labels on flow METRICS. |
| config.cardinality.flow.destination_port | bool | `false` | Add destination.port to flow METRICS. INERT under metrics_mode: rollup, where tailscale.dst.service is the bounded stand-in (and is always emitted, no toggle). |
| config.cardinality.flow.exit_node_attribution | bool | `true` | Emit the bounded tailscale.exit_node.io/packets counters attributing exit traffic to the relaying node (bounded by exit-node count); independent of metrics_mode. |
| config.cardinality.flow.geo_dims | bool | `false` | Add source/destination geo.country.iso_code and geo.continent.code to flow METRICS. Requires enrichment.geoip.enabled. Nearly free under metrics_mode: rollup, which is top-N bounded whatever the key carries; on the RAW families with collapse_external on it splits the single "external" series up to ~250 ways. The AS number/organization and any city-level fields NEVER reach a metric; flow LOGS carry all of them regardless of this toggle. |
| config.cardinality.flow.identity_dims | bool | `false` | Add the per-flow endpoint identity — tailscale.{src,dst}.user, .tags and .os — to flow METRICS, on the raw AND rollup families. Read from the node blocks the control plane embeds in each flow record, so it costs no extra API call. Identity is a property of the NODE, so with node_dims on this widens the label set without multiplying the series count. REQUIRES node_dims and is ignored without it, since identity would then be the only dimension splitting the metric. On the rollup families the __other__ remainder drops identity: the fold is many nodes, so it has no single user to report. Off by default only because `user` is an email address; pii_filter.emails still applies. Flow LOGS carry these regardless. |
| config.cardinality.flow.metrics_mode | string | `"rollup"` | Which flow metric families to emit: rollup (default) | all | both. rollup = bounded top-N *.rollup families (busiest src/dst node pairs by bytes; remainder folds into an __other__ series so totals are preserved; no L4 ports; adds tailscale.network.unique.* gauges). all = raw per-connection tailscale.network.io/packets shaped by the toggles below. both = emit both (~2x series; summing double-counts). |
| config.cardinality.flow.node_dims | bool | `true` | Include tailscale.src.node/tailscale.dst.node on flow metrics — who talked to whom. Off keeps totals accurate but drops the per-peer breakdown, and suppresses the tailscale.network.unique.* gauges (keyed by source node, so they would reintroduce exactly the cardinality this removes). The single biggest cardinality lever here. |
| config.cardinality.flow.rollup_top_n | int | `500` | Rollup only: busiest src/dst node pairs kept per flush; the rest fold into __other__. 0 = default (500). |
| config.cardinality.flow.source_port | bool | `false` | Add source.port to flow METRICS. INERT under metrics_mode: rollup (raw families only), and the most expensive knob in this block — ephemeral source ports are effectively unbounded. Ports are always present on flow LOGS regardless. |
| config.cardinality.label_value_sample_cap | int | `100` | Distinct values retained per (metric,label) for the label-cardinality views; beyond it the label is capped and examples truncated (0 disables label capture). |
| config.cardinality.metric_limit | int | `10000` | Hard per-metric series cap (0/negative = unlimited). |
| config.cardinality.per_entity.device | bool | `true` | Emit per-device gauges (online/last_seen/key.expiry/derp/routes); false emits only the aggregate tailscale.devices.count rollup. |
| config.cardinality.per_entity.key | bool | `true` | Emit the per-key expiry gauge; false emits only tailscale.keys.count (the key-expiry warning log still fires). |
| config.cardinality.per_entity.service | bool | `true` | Emit the per-service ports/hosts gauges; false emits only tailscale.services.count. |
| config.cardinality.per_entity.user | bool | `true` | Emit per-user gauges (devices/connected/last_seen); false emits only tailscale.users.count. |
| config.cardinality.per_entity.webhook | bool | `true` | Emit the per-endpoint webhook subscriptions gauge; false emits only tailscale.webhook_endpoints.count. |
| config.cardinality.subnet_route_rollup | bool | `true` | Emit the per-CIDR tailscale.subnet_routes.routers redundancy gauge (one series per subnet CIDR); the fleet exit/subnet count aggregates emit regardless. |
| config.cardinality.warning_threshold | int | `2000` | Status-page cardinality view flags a metric at/above this active-series count (self-obs only; 0 disables). |
| config.checkpoint.evidence_store | string | `"file"` | Semantic-evidence store: memory \| file. Independent of poll cursors; keep file to preserve ACL revision/audit provenance across restarts even in streamed deployments. |
| config.checkpoint.file_path | string | `"/var/lib/tailscale2otel/checkpoints.json"` | Shared state path when either store is file (mount a writable persistent volume here). |
| config.checkpoint.store | string | `"file"` | Poll-cursor store: memory \| file. "memory" loses window cursors on restart (re-does initial_lookback); "file" persists them atomically (needs a writable volume at file_path). |
| config.collectors.acl.enabled | bool | `true` | Enable the ACL/policy collector (acl.last_changed, acl.size, acl.rules by section). |
| config.collectors.acl.interval | string | `"600s"` | Poll interval. |
| config.collectors.acl.validate | bool | `true` | Validate the active policy each tick via the non-mutating `POST /acl/validate` (needs only `policy_file:read`). Set false to keep the API client strictly GET-only. |
| config.collectors.auditlogs.enabled | bool | `true` | Enable the configuration-audit-logs collector. |
| config.collectors.auditlogs.initial_lookback | string | `"5m"` | Cold-start lookback on first run. |
| config.collectors.auditlogs.interval | string | `"60s"` | Poll interval. |
| config.collectors.auditlogs.lag | string | `"60s"` | Tail-hazard lag (see flowlogs.lag). |
| config.collectors.auditlogs.max_window | string | `"6h"` | Maximum width of a single poll window. |
| config.collectors.auditlogs.objectstore.access_key_id | string | `""` | Static S3 access key ID. Set via the TS2OTEL_* secret, not here. |
| config.collectors.auditlogs.objectstore.access_key_id_file | string | `""` | Read the S3 access key ID from this mounted file instead of an inline value. Set the value or the file, never both. |
| config.collectors.auditlogs.objectstore.allow_insecure_http | bool | `false` | Permit plaintext HTTP to a remote object-store endpoint. Loopback HTTP works without this flag for local MinIO development. Enabling it exposes signing credentials and temporary session tokens to the network; prefer HTTPS. |
| config.collectors.auditlogs.objectstore.bucket | string | `""` | Bucket Tailscale exports configuration logs into. REQUIRED when source is objectstore. |
| config.collectors.auditlogs.objectstore.endpoint | string | `""` | Service URL of the S3-compatible store holding Tailscale's CONFIGURATION-log export, e.g. https://s3.eu-west-2.amazonaws.com or a MinIO address. REQUIRED when auditlogs.source is objectstore. This is a SEPARATE export from the flow one: nothing here is inherited from collectors.flowlogs.objectstore, and two destinations may not name the same bucket+prefix. |
| config.collectors.auditlogs.objectstore.initial_lookback | string | `"6h"` | Cold-start reach-back, so a first run against a long history does not ingest all of it. CAPPED IN EFFECT AT 14 DAYS under layout: partitioned — the reader enumerates at most 14 day partitions newest-first and the cursor only moves forward, so a larger value silently ingests only the most recent 14 days (warned about at startup). Use layout: flat, which has no partitions to cap, to reach further back. |
| config.collectors.auditlogs.objectstore.interval | string | `"60s"` | How often the bucket is listed. |
| config.collectors.auditlogs.objectstore.layout | string | `"partitioned"` | How objects are arranged under prefix: `partitioned` (the default, and what Tailscale's own export writes: objects under prefix/YYYY/MM/DD/) or `flat` (a COPIED or mirrored export whose self-contained YYYY-MM-DD-HH-MM-SS basenames sit directly under prefix). Flat also finds partitioned objects, but has no partitions to bound re-listing, so it costs more LIST requests; each cycle is still bounded and resumable. Not autodetected. |
| config.collectors.auditlogs.objectstore.lookback | string | `"1h"` | How far back past the cursor each listing reaches, so an object that arrived late is still found. Keep it >= interval, or an object landing between two cycles can be missed. |
| config.collectors.auditlogs.objectstore.max_cycle_decompressed_bytes | int | `268435456` | Maximum decompressed bytes processed in one cycle. Untouched objects are deferred. Must be at least max_object_decompressed_bytes. |
| config.collectors.auditlogs.objectstore.max_cycle_records | int | `500000` | Maximum records processed in one cycle. Untouched objects are deferred. Must be at least max_object_records. |
| config.collectors.auditlogs.objectstore.max_cycle_wire_bytes | int | `536870912` | Maximum GET response bytes read in one cycle. The current and untouched objects are deferred. Must be at least max_object_wire_bytes. |
| config.collectors.auditlogs.objectstore.max_object_decompressed_bytes | int | `33554432` | Maximum decompressed bytes accepted from one object. A breach quarantines that object. |
| config.collectors.auditlogs.objectstore.max_object_records | int | `100000` | Maximum records accepted from one object. A breach quarantines that object. |
| config.collectors.auditlogs.objectstore.max_object_wire_bytes | int | `67108864` | Maximum GET response bytes read from one object. A breach quarantines that object. |
| config.collectors.auditlogs.objectstore.max_objects | int | `200` | Objects ingested per cycle. The remainder is counted, logged and picked up next cycle. |
| config.collectors.auditlogs.objectstore.path_style | bool | `false` | Address as <endpoint>/<bucket>/<key> rather than <bucket>.<endpoint>/<key>. Required by most non-AWS implementations; getting it backwards is a DNS failure. |
| config.collectors.auditlogs.objectstore.prefix | string | `""` | The export's root within the bucket, above the YYYY/MM/DD partitions. Use a distinct prefix when the flow and configuration exports share one bucket. |
| config.collectors.auditlogs.objectstore.region | string | `""` | Bucket region. REQUIRED when source is objectstore: it is part of the request signature, so a wrong value fails every request with HTTP 403. |
| config.collectors.auditlogs.objectstore.secret_access_key | string | `""` | Static S3 secret access key. Set via the TS2OTEL_* secret, not here. |
| config.collectors.auditlogs.objectstore.secret_access_key_file | string | `""` | Read the S3 secret access key from this mounted file instead of an inline value. Set the value or the file, never both. |
| config.collectors.auditlogs.objectstore.session_token | string | `""` | Static S3 session token (temporary credentials only). Set via the TS2OTEL_* secret, not here. |
| config.collectors.auditlogs.objectstore.session_token_file | string | `""` | Read the temporary S3 session token from this mounted file instead of an inline value. Set the value or the file, never both. |
| config.collectors.auditlogs.source | string | `"poll"` | Ingestion source: poll | stream | both | objectstore. Pick ONE method per log type (see flowlogs.source): `both` risks double-counting and de-dup is only a best-effort failsafe (WARNed at startup). Set `stream` when config.streaming.enabled is true. |
| config.collectors.contacts.enabled | bool | `true` | Enable the contacts collector (account/support/security contact verification; no emails emitted). |
| config.collectors.contacts.interval | string | `"600s"` | Poll interval. |
| config.collectors.devices.attribute_namespaces | list | `["intune","jamf","kandji","crowdstrike","sentinelone","kolide","ip"]` | Posture-attribute namespace prefixes promoted to the tailscale.device.attribute{,.info} metrics (needs collect_posture). `["*"]` = every namespace incl. node/custom; `[]` = disabled. |
| config.collectors.devices.collect_connectivity | bool | `true` | Emit per-device NAT/connectivity health (tailscale.device.connectivity.*) plus the fleet connectivity rollups (tailscale.devices.hard_nat/direct_capable/client_supports) from the rich device payload (no extra API call). Per-device gauges additionally gated by per_entity.device. |
| config.collectors.devices.collect_device_invites | bool | `true` | Inventory outstanding device-share invites via GET /device/{id}/device-invites (one API call per device, N+1; needs the device_invites:read OAuth scope, covered by all:read). Emits tailscale.device_invites.count; per-device fetch failures are non-fatal. |
| config.collectors.devices.collect_posture | bool | `false` | Emit per-device posture attributes as log records (gated; requires posture identity on). |
| config.collectors.devices.collect_routes | bool | `false` | Also collect advertised/enabled subnet routes and per-DERP latency via the rich GET /devices?fields=all endpoint. |
| config.collectors.devices.collect_tag_rollup | bool | `true` | Emit the tailscale.devices.by_tag distribution gauge (one series per ACL tag). false keeps the other fleet-hygiene aggregates (untagged/ephemeral/by_version/key_expiry). |
| config.collectors.devices.enabled | bool | `true` | Enable the devices collector (device.online/last_seen/key_expiry/update_available). |
| config.collectors.devices.interval | string | `"60s"` | Poll interval. |
| config.collectors.devices.posture_log_mode | string | `"changes"` | How the tailscale.device.posture log behaves when collect_posture is on: changes (full dump on the first scrape, then deltas only) | always (every scrape) | off (suppress the log; the posture gauge metric is still emitted). `always` on a large fleet is a lot of log volume. |
| config.collectors.devices.tag_rollup_limit | int | `50` | Cap on distinct tag series for by_tag: busiest N tags keep their own series, the rest fold into tailscale.tag="__other__". 0 or negative = unlimited. |
| config.collectors.dns.enabled | bool | `true` | Enable the DNS collector (nameservers/search-paths/split-zones counts, MagicDNS). |
| config.collectors.dns.interval | string | `"600s"` | Poll interval. |
| config.collectors.flowlogs.enabled | bool | `true` | Enable the network-flow-logs collector (aggregated metrics + full-fidelity logs). |
| config.collectors.flowlogs.initial_lookback | string | `"5m"` | Cold-start lookback on first run when no checkpoint exists. |
| config.collectors.flowlogs.interval | string | `"60s"` | Poll interval. |
| config.collectors.flowlogs.lag | string | `"120s"` | Tail-hazard lag: never poll closer than this to now, so a window only closes once Tailscale has finished writing it (avoids missing late-arriving records). |
| config.collectors.flowlogs.log_mode | string | `"per_connection"` | Flow-log verbosity: per_connection | per_record | off (off = metrics only, no logs). |
| config.collectors.flowlogs.max_log_records_per_window | int | `0` | Cap on flow LOG records per poll window (0 = unlimited). Excess is counted into tailscale.network.flow.logs_dropped; METRICS are never capped, only logs. |
| config.collectors.flowlogs.max_window | string | `"1h"` | Maximum width of a single poll window (caps catch-up after downtime). |
| config.collectors.flowlogs.objectstore.access_key_id | string | `""` | Static S3 access key ID. Set via the TS2OTEL_* secret, not here. |
| config.collectors.flowlogs.objectstore.access_key_id_file | string | `""` | Read the S3 access key ID from this mounted file instead of an inline value. Set the value or the file, never both. |
| config.collectors.flowlogs.objectstore.allow_insecure_http | bool | `false` | Permit plaintext HTTP to a remote object-store endpoint. Loopback HTTP works without this flag for local MinIO development. Enabling it exposes signing credentials and temporary session tokens to the network; prefer HTTPS. |
| config.collectors.flowlogs.objectstore.bucket | string | `""` | Bucket Tailscale exports into. REQUIRED when source is objectstore. |
| config.collectors.flowlogs.objectstore.endpoint | string | `""` | Service URL of the S3-compatible store holding Tailscale's flow-log export, e.g. https://s3.eu-west-2.amazonaws.com or a MinIO address. REQUIRED when source is objectstore; never derived from the region, because a non-AWS implementation would be derived wrong. Leave "" for any other source. |
| config.collectors.flowlogs.objectstore.initial_lookback | string | `"6h"` | Cold-start reach-back, so a first run against a long history does not ingest all of it. CAPPED IN EFFECT AT 14 DAYS under layout: partitioned — the reader enumerates at most 14 day partitions newest-first and the cursor only moves forward, so a larger value silently ingests only the most recent 14 days (warned about at startup). Use layout: flat, which has no partitions to cap, to reach further back. |
| config.collectors.flowlogs.objectstore.interval | string | `"60s"` | How often the bucket is listed. |
| config.collectors.flowlogs.objectstore.layout | string | `"partitioned"` | How objects are arranged under prefix: `partitioned` (the default, and what Tailscale's own export writes: objects under prefix/YYYY/MM/DD/) or `flat` (a COPIED or mirrored export whose self-contained YYYY-MM-DD-HH-MM-SS basenames sit directly under prefix). Flat also finds partitioned objects, but has no partitions to bound re-listing, so it costs more LIST requests; each cycle is still bounded and resumable. Not autodetected. |
| config.collectors.flowlogs.objectstore.lookback | string | `"1h"` | How far back past the cursor each listing reaches, so an object that arrived late is still found. Keep it >= interval, or an object landing between two cycles can be missed. |
| config.collectors.flowlogs.objectstore.max_cycle_decompressed_bytes | int | `268435456` | Maximum decompressed bytes processed in one cycle. Untouched objects are deferred. Must be at least max_object_decompressed_bytes. |
| config.collectors.flowlogs.objectstore.max_cycle_records | int | `500000` | Maximum records processed in one cycle. Untouched objects are deferred. Must be at least max_object_records. |
| config.collectors.flowlogs.objectstore.max_cycle_wire_bytes | int | `536870912` | Maximum GET response bytes read in one cycle. The current and untouched objects are deferred. Must be at least max_object_wire_bytes. |
| config.collectors.flowlogs.objectstore.max_object_decompressed_bytes | int | `33554432` | Maximum decompressed bytes accepted from one object. A breach quarantines that object. |
| config.collectors.flowlogs.objectstore.max_object_records | int | `100000` | Maximum records accepted from one object. A breach quarantines that object. |
| config.collectors.flowlogs.objectstore.max_object_wire_bytes | int | `67108864` | Maximum GET response bytes read from one object. A breach quarantines that object. |
| config.collectors.flowlogs.objectstore.max_objects | int | `200` | Objects ingested per cycle. The remainder is counted, logged and picked up next cycle. |
| config.collectors.flowlogs.objectstore.path_style | bool | `false` | Address as <endpoint>/<bucket>/<key> rather than <bucket>.<endpoint>/<key>. Required by most non-AWS implementations; getting it backwards is a DNS failure. |
| config.collectors.flowlogs.objectstore.prefix | string | `""` | The export's root within the bucket, above the YYYY/MM/DD partitions. No leading slash: it becomes part of this feed's durable checkpoint identity, so removing one later reads as a brand-new feed and re-emits already-ingested objects. |
| config.collectors.flowlogs.objectstore.region | string | `""` | Bucket region. REQUIRED when source is objectstore: it is part of the request signature, so a wrong value fails every request with HTTP 403. |
| config.collectors.flowlogs.objectstore.secret_access_key | string | `""` | Static S3 secret access key. Set via the TS2OTEL_* secret, not here. |
| config.collectors.flowlogs.objectstore.secret_access_key_file | string | `""` | Read the S3 secret access key from this mounted file instead of an inline value. Set the value or the file, never both. |
| config.collectors.flowlogs.objectstore.session_token | string | `""` | Static S3 session token (temporary credentials only). Set via the TS2OTEL_* secret, not here. |
| config.collectors.flowlogs.objectstore.session_token_file | string | `""` | Read the temporary S3 session token from this mounted file instead of an inline value. Set the value or the file, never both. |
| config.collectors.flowlogs.replay_overlap | string | `"5m"` | Reread this much before the durable high-water mark so records that became available after the first completed query can still arrive. 0 disables; maximum 1h. |
| config.collectors.flowlogs.replay_seen_capacity | int | `131072` | Maximum durable SHA-256 connection identities retained to suppress the intentional replay across restart. Must be 1..1048576 while replay_overlap is enabled. |
| config.collectors.flowlogs.source | string | `"poll"` | Ingestion source: poll | stream | objectstore | both. PICK ONE method per log type: `both` runs poll AND the `streaming` receiver and risks double-counting — cross-source de-duplication is a best-effort FAILSAFE, not a guarantee. The exporter logs a WARN at startup when streaming is enabled while this collector still polls or reads the export bucket. Set `stream` (not poll/both) when config.streaming.enabled is true; set `objectstore` and fill in the objectstore block below to read Tailscale's S3 export instead of calling the API. |
| config.collectors.flowlogs.trusted_reporter_node_ids | list | `[]` | Verified FlowLog.NodeID values classified as `configured`. Empty together with trusted_reporter_tags leaves reporter trust `unconfigured`. |
| config.collectors.flowlogs.trusted_reporter_tags | list | `[]` | Authoritative device tags that classify a verified reporter as `tagged`. Tags embedded in the flow record are unverified and never grant trust; unmatched reporters are `untrusted`. |
| config.collectors.k8s_audit.enabled | bool | `false` | Enable the Kubernetes API-audit collector, which reads the events Tailscale's tsrecorder writes for requests proxied through the Kubernetes operator's API-server proxy. Requires `enableEvents` in the tailscale.com/cap/kubernetes ACL grant, which is BETA upstream. NOTE: the source data carries no response status, latency or byte count, so allowed-vs-denied, error rates and latency are NOT derivable from this feed. |
| config.collectors.k8s_audit.objectstore.access_key_id | string | `""` | Static S3 access key ID. Set via the TS2OTEL_* secret, not here. |
| config.collectors.k8s_audit.objectstore.access_key_id_file | string | `""` | Read the S3 access key ID from this mounted file instead of an inline value. Set the value or the file, never both. |
| config.collectors.k8s_audit.objectstore.allow_insecure_http | bool | `false` | Permit plaintext HTTP to a remote object-store endpoint. Loopback HTTP works without this flag for local MinIO development. Enabling it exposes signing credentials and temporary session tokens to the network; prefer HTTPS. |
| config.collectors.k8s_audit.objectstore.bucket | string | `""` | Bucket tsrecorder writes recordings into. REQUIRED. |
| config.collectors.k8s_audit.objectstore.endpoint | string | `""` | Service URL of the S3-compatible store tsrecorder writes to, e.g. https://s3.eu-west-1.amazonaws.com or a MinIO address. REQUIRED when k8s_audit is enabled. This bucket is NEVER inherited from the flow or configuration-log destinations: it is a separate bucket with its own key layout. There is deliberately no `source` key here — object storage is the only surface tsrecorder exposes. |
| config.collectors.k8s_audit.objectstore.initial_lookback | string | `"6h"` | Cold-start reach-back, so a first run against a long history does not ingest all of it. The 14-day partition cap that applies to layout: partitioned does NOT apply here: the recorder layout enumerates one flat prefix and has no day partitions to bound. |
| config.collectors.k8s_audit.objectstore.interval | string | `"60s"` | How often the bucket is listed. |
| config.collectors.k8s_audit.objectstore.layout | string | `"recorder"` | Object key layout. `recorder` is the only accepted value for this collector; `partitioned` and `flat` are REFUSED, because tsrecorder writes no YYYY/MM/DD partitions and its RFC3339Nano basenames are variable-width, so they do not sort like an export's. |
| config.collectors.k8s_audit.objectstore.lookback | string | `"1h"` | How far back past the cursor each listing reaches, so an object that arrived late is still found. Keep it >= interval. |
| config.collectors.k8s_audit.objectstore.max_cycle_decompressed_bytes | int | `268435456` | Maximum decompressed bytes processed in one cycle. Untouched objects are deferred. Must be at least max_object_decompressed_bytes. |
| config.collectors.k8s_audit.objectstore.max_cycle_records | int | `500000` | Maximum records processed in one cycle. Untouched objects are deferred. Must be at least max_object_records. |
| config.collectors.k8s_audit.objectstore.max_cycle_wire_bytes | int | `536870912` | Maximum GET response bytes read in one cycle. The current and untouched objects are deferred. Must be at least max_object_wire_bytes. |
| config.collectors.k8s_audit.objectstore.max_object_decompressed_bytes | int | `33554432` | Maximum decompressed bytes accepted from one object. A breach quarantines that object. RAISE THIS if you record long terminal sessions: only the .cast header line is read for meaning, but the whole object is still streamed, and an oversized one is quarantined rather than partially read. |
| config.collectors.k8s_audit.objectstore.max_object_records | int | `100000` | Maximum records accepted from one object. A breach quarantines that object. |
| config.collectors.k8s_audit.objectstore.max_object_wire_bytes | int | `67108864` | Maximum GET response bytes read from one object. A breach quarantines that object. |
| config.collectors.k8s_audit.objectstore.max_objects | int | `200` | Objects ingested per cycle. The remainder is counted, logged and picked up next cycle. tsrecorder writes ONE event per object, so a busy cluster needs a higher value here than a flow or configuration-log export does. |
| config.collectors.k8s_audit.objectstore.path_style | bool | `false` | Address as <endpoint>/<bucket>/<key> rather than <bucket>.<endpoint>/<key>. Required by most non-AWS implementations; getting it backwards is a DNS failure. |
| config.collectors.k8s_audit.objectstore.prefix | string | `""` | Root within the bucket. Usually EMPTY: tsrecorder keys are <stableID>/events/<ts>.event and <stableID>/<ts>.cast, and <stableID> differs per recorder replica, so it cannot be pinned in a prefix. |
| config.collectors.k8s_audit.objectstore.region | string | `""` | Bucket region. REQUIRED: it is part of the request signature, so a wrong value fails every request with HTTP 403. |
| config.collectors.k8s_audit.objectstore.secret_access_key | string | `""` | Static S3 secret access key. Set via the TS2OTEL_* secret, not here. |
| config.collectors.k8s_audit.objectstore.secret_access_key_file | string | `""` | Read the S3 secret access key from this mounted file instead of an inline value. Set the value or the file, never both. |
| config.collectors.k8s_audit.objectstore.session_token | string | `""` | Static S3 session token, for temporary credentials only. Set via the TS2OTEL_* secret. |
| config.collectors.k8s_audit.objectstore.session_token_file | string | `""` | Read the S3 session token from this mounted file instead of an inline value. Set the value or the file, never both. |
| config.collectors.keys.enabled | bool | `true` | Enable the auth/API keys collector (key.expiry, keys.count). |
| config.collectors.keys.expiry_warn | string | `"168h"` | Emit a tailscale.key.expiring WARN log when a key expires within this window. |
| config.collectors.keys.interval | string | `"300s"` | Poll interval. |
| config.collectors.log_stream.enabled | bool | `true` | Enable the log-streaming delivery-health collector. Self-gates to configured=0 (no error) when no SIEM sink is configured for a log type. |
| config.collectors.log_stream.interval | string | `"600s"` | Poll interval. |
| config.collectors.node_metrics.discovery.address_order | string | `"ipv4"` | Preferred address family: ipv4 | ipv6 (falls back to the other). |
| config.collectors.node_metrics.discovery.enabled | bool | `false` | Turn on dynamic target discovery. |
| config.collectors.node_metrics.discovery.exclude_external | bool | `true` | Skip shared/external devices. |
| config.collectors.node_metrics.discovery.exclude_tags | list | `[]` | Devices carrying any of these tags are skipped (wins over include_tags). |
| config.collectors.node_metrics.discovery.include_host_labels | bool | `true` | Attach host.name/host.id so scraped series join with tailscale.device.* metrics. |
| config.collectors.node_metrics.discovery.include_tags | list | `[]` | If non-empty, only devices carrying one of these tags, e.g. ["tag:server"]. |
| config.collectors.node_metrics.discovery.include_tags_label | bool | `true` | Attach the tailscale.tags label to scraped series. |
| config.collectors.node_metrics.discovery.instance_source | string | `"name"` | Source of the node-identity label: name (MagicDNS short name — unique per tailnet and human-friendly, the default) | address (Tailscale host:port, always unique) | hostname (OS hostname — NOT unique; collisions are auto-suffixed with the address plus a WARN). |
| config.collectors.node_metrics.discovery.interval | string | `"300s"` | How often the devices API is polled for targets (independent of the scrape interval). |
| config.collectors.node_metrics.discovery.max_targets | int | `1000` | Cap on emitted discovered targets per refresh (one per selected port, not one per device). |
| config.collectors.node_metrics.discovery.online_only | bool | `true` | Only devices currently connected to the control plane. |
| config.collectors.node_metrics.discovery.path | string | `"/metrics"` | Metrics path on each device. |
| config.collectors.node_metrics.discovery.port | int | `5252` | Metrics port on each device (tailscaled's default is 5252). |
| config.collectors.node_metrics.discovery.port_overrides | object | `{}` | Map ACL tags to replacement metrics-port lists. A matching tag replaces `port`; multiple matches use their sorted, deduplicated union. File-only in the standalone config. |
| config.collectors.node_metrics.discovery.scheme | string | `"http"` | Metrics-endpoint scheme applied to each discovered device: http | https. |
| config.collectors.node_metrics.drop_labels | list | `[]` | Label keys stripped from every forwarded series (the instance label is never dropped). |
| config.collectors.node_metrics.enabled | bool | `false` | Enable the node-metrics scraper. Requires at least one entry in `targets`. |
| config.collectors.node_metrics.interval | string | `"60s"` | Scrape interval for every target. |
| config.collectors.node_metrics.max_distinct_metrics | int | `2000` | Cap on DISTINCT forwarded metric NAMES over the process lifetime. A target chooses its own metric names and each unseen name creates an instrument that is never released, so max_samples alone does not bound them. 0 = 2000 default, <0 = unlimited; names beyond the budget are dropped and counted rather than silently ignored. |
| config.collectors.node_metrics.max_response_bytes | int | `4194304` | Cap on the response body read from one target per scrape, in bytes (4 MiB). Bounds memory: an over-cap body fails that one scrape rather than buffering unbounded. |
| config.collectors.node_metrics.max_samples | int | `50000` | Cap on samples parsed from one target per scrape. Bounds cardinality: an over-cap target fails that one scrape rather than forwarding the whole set. Must be > 0 when enabled. |
| config.collectors.node_metrics.metric_allow | list | `[]` | Passthrough allow-list: anchored regex on the forwarded metric NAME; if non-empty, only matching names are forwarded. |
| config.collectors.node_metrics.metric_deny | list | `[]` | Passthrough deny-list: anchored regex; names matching any are dropped (after metric_allow). |
| config.collectors.node_metrics.targets | list | `[]` | List of scrape targets. Each: {url, instance, labels{}, bearer_token, bearer_token_file, headers{}, tls{insecure,ca_file,cert_file,key_file}}. The "instance" label is the node identity. |
| config.collectors.node_metrics.timeout | string | `"10s"` | Per-scrape HTTP timeout. |
| config.collectors.oauth_apps.enabled | bool | `true` | Enable the OAuth-application inventory collector (count, per-app scope/node-attribute gauges). Alpha API — idles silently, with no error, on tailnets that do not have it. |
| config.collectors.oauth_apps.interval | string | `"300s"` | Poll interval. |
| config.collectors.posture_integrations.enabled | bool | `true` | Enable the device-posture-integration collector (MDM/EDR matched counts + last_sync staleness). |
| config.collectors.posture_integrations.interval | string | `"600s"` | Poll interval. |
| config.collectors.services.collect_hosts | bool | `false` | Also collect per-service backing-host detail (approval/configured state) — one extra API call per service (N+1). Off by default. |
| config.collectors.services.enabled | bool | `true` | Enable the Tailscale Services (VIP) collector (services.count + per-service ports/hosts). |
| config.collectors.services.interval | string | `"600s"` | Poll interval. |
| config.collectors.settings.enabled | bool | `true` | Enable the tailnet-settings collector (setting.enabled flags, key-duration). |
| config.collectors.settings.interval | string | `"600s"` | Poll interval (settings change rarely). |
| config.collectors.users.enabled | bool | `true` | Enable the users collector (users.count, per-user devices/connected/last_seen). |
| config.collectors.users.interval | string | `"300s"` | Poll interval (user data changes slowly). |
| config.collectors.webhooks.desired_events | list | `[]` | Optional expected webhook event categories (e.g. `["nodeCreated","userSuspended"]`); empty means no expectation is checked. |
| config.collectors.webhooks.enabled | bool | `true` | Enable the webhook-endpoint inventory collector (count + per-endpoint subscriptions; no url/secret). |
| config.collectors.webhooks.interval | string | `"600s"` | Poll interval. |
| config.delivery | object | `{"mode":"otlp"}` | Process-wide telemetry topology. otlp preserves historical push-only delivery; prometheus serves /metrics while suppressing inherited OTLP metrics, logs, and traces; dual enables both. An explicit otlp.<signal>.endpoint opts only that signal back in under prometheus. |
| config.delivery.mode | string | `"otlp"` | Delivery mode: otlp \| prometheus \| dual. |
| config.enrichment.cache_ttl | string | `"5m"` | Staleness alarm threshold for the device-enrichment cache (drives the tailscale2otel.enrich.cache_age self-obs gauge); does not evict entries. |
| config.enrichment.geoip.acknowledge_cardinality | bool | `false` | Acknowledge the cardinality cost of cardinality.flow.geo_dims on the RAW flow families and silence the startup advisory. Set true once cardinality.metric_limit is sized for it. |
| config.enrichment.geoip.asn_database | string | `""` | Path to a GeoLite2/GeoIP2 ASN .mmdb. The AS number and organization ride flow LOGS only. |
| config.enrichment.geoip.country_database | string | `""` | Path to a GeoLite2/GeoIP2 Country .mmdb. A City database also works and additionally fills locality, region and coordinates on flow LOGS. Left empty when download.enabled is set, it defaults to where the downloader installs the file. |
| config.enrichment.geoip.download.account_id | string | `""` | MaxMind account ID (a free GeoLite2 account is enough). |
| config.enrichment.geoip.download.directory | string | `""` | Where downloaded databases are installed. Empty uses the platform state directory, which in the container image is the same volume as the checkpoint file — mount one, or the databases are re-downloaded on every restart. |
| config.enrichment.geoip.download.editions | list | `["GeoLite2-Country","GeoLite2-ASN"]` | MaxMind edition IDs to fetch; each installs as <directory>/<edition>.mmdb. Swap GeoLite2-Country for GeoLite2-City to add locality and coordinates to flow logs. |
| config.enrichment.geoip.download.enabled | bool | `false` | Fetch databases from MaxMind directly, so no geoipupdate sidecar is needed. Leave off if something else supplies the files. Note the databases are held in memory: budget roughly 9 MB for Country plus 12 MB for ASN on top of the container's normal footprint. |
| config.enrichment.geoip.download.endpoint | string | `"https://download.maxmind.com/geoip/databases"` | Download API base. Override only for a local mirror. |
| config.enrichment.geoip.download.interval | string | `"24h"` | How often to ask MaxMind for a newer build. Each check is a conditional request, so an unchanged database costs a 304 and no download quota. |
| config.enrichment.geoip.download.license_key | string | `""` | MaxMind license key. Prefer existingSecret / an env var over putting it in values.yaml. |
| config.enrichment.geoip.download.license_key_file | string | `""` | Read the license key from a file instead (mounted Kubernetes secret). |
| config.enrichment.geoip.download.timeout | string | `"5m"` | Per-edition download timeout. |
| config.enrichment.geoip.enabled | bool | `false` | Opt-in geolocation and autonomous-system enrichment of EXTERNAL flow addresses, from MaxMind .mmdb files on local disk. Lookups never touch the network. Tailnet addresses (the CGNAT range 100.64.0.0/10 and the ULA fd7a:115c:a1e0::/48) are never geolocated. |
| config.enrichment.geoip.reload_interval | string | `"6h"` | Re-stat the database paths and hot-swap a changed file. This is what makes an external updater (a geoipupdate CronJob, an init container, a mounted volume) work. 0 disables. Not the same clock as download.interval, which asks MaxMind for a newer build. |
| config.enrichment.reverse_dns.acknowledge_cardinality | bool | `false` | Acknowledge the flow-metric cardinality cost of enabled+node_dims and silence the startup advisory (config_warnings_ratio). Set true once cardinality.metric_limit is sized for it. |
| config.enrichment.reverse_dns.cache_ttl | string | `"24h"` | How long a resolved name is cached (PTRs rarely change, so a long TTL keeps resolver load low). |
| config.enrichment.reverse_dns.enabled | bool | `false` | Opt-in reverse-DNS (PTR) enrichment of EXTERNAL flow addresses; resolved names replace the "external" bucket in tailscale.src/dst.node (flow logs always; flow metrics when cardinality.flow.node_dims is on). On flow METRICS this can add ~one series per external IP. |
| config.enrichment.reverse_dns.max_entries | int | `50000` | Cache size bound; new external IPs beyond this are not resolved (~150 bytes/entry). |
| config.enrichment.reverse_dns.negative_ttl | string | `"5m"` | How long a failed lookup is remembered (suppresses retries). |
| config.enrichment.reverse_dns.server | string | `""` | Resolver to query as "ip" or "ip:port" (default port 53); empty = system/container resolver. |
| config.enrichment.reverse_dns.stale_ttl | string | `"1h"` | Keep serving a resolved name this long past cache_ttl while one background refresh runs. Stops the flow label flapping to "external" at every TTL expiry. 0 disables stale serving. |
| config.enrichment.reverse_dns.timeout | string | `"2s"` | Per-lookup timeout. |
| config.events.enabled | bool | `true` | Build the bounded audit/webhook event store and serve /events. No effect without admin.enabled + admin.landing_page. |
| config.events.max_events | int | `5000` | How many individual audit+webhook events /events can see (100–100000). A plain count, not a time span — oldest evicted first. |
| config.flows.capacity_profile | string | `"default"` | Trade memory for fidelity on every per-bucket dimension and the raw-connection ring: `compact` (~half the default footprint), `default` (today's limits, unchanged), or `expanded` (~double). Fixed, hard-coded presets only — never a raw number. |
| config.flows.enabled | bool | `true` | Build the flow store and serve /flows. No effect without admin.enabled + admin.landing_page. |
| config.flows.max_future_skew | string | `"5m"` | Local-view admission only: reject records further ahead of this process clock (0–1h). OTLP emission is unchanged. |
| config.flows.retention | string | `"6h"` | How far back /flows can see, as a ring of one-minute buckets (1m–24h). Sizes pod memory; each tailnet keeps its own store. |
| config.flows.store.batch_size | int | `512` | Rows written per transaction by the background writer. |
| config.flows.store.directory | string | `""` | Directory to hold this tailnet's flows-<tailnet>.db. Empty (default) = disabled. Point it at the persistence PVC's mount, e.g. /var/lib/tailscale2otel/flows, and set persistence.enabled=true — see the sizing note on that block below. Must be writable; if it cannot be opened the flow view is switched OFF (and /flows 404s) rather than silently falling back to memory, so an operator who asked for history is never shown a view that looks like it. OTLP export is unaffected. Rows carry user identities (emails) and land on disk and in backups — the configured pii_filter is applied before a row is written, same as the OTLP export path. |
| config.flows.store.flush_interval | string | `"5s"` | How often a partial batch is forced to disk, so a quiet tailnet's last few connections do not sit in memory indefinitely between flushes. |
| config.flows.store.max_export_rows | int | `50000` | Cap on how many rows a single CSV/JSON export may read, so an export cannot try to materialise the whole retained window in one request. |
| config.flows.store.max_rows | int | `5000000` | Hard cap on retained rows, enforced independently of retention so a traffic flood cannot fill the disk before the next sweep runs. |
| config.flows.store.query_timeout | string | `"15s"` | Timeout on a single read from the store. A window scan that exceeds it fails honestly rather than hanging the admin page. |
| config.flows.store.queue_size | int | `8192` | Bound on the write-behind queue between the emit path and the disk writer. A full queue drops the observation and counts it rather than blocking OTLP export — visible in the admin API. |
| config.flows.store.retention | string | `"720h"` | How far back the on-disk store keeps rows before the retention sweep deletes them. Separate from flows.retention above, which only sizes the in-memory ring and is capped at 24h — this bound is unrelated and can be much longer. |
| config.flows.store.sweep_interval | string | `"1h"` | How often the retention window and the row cap are enforced. |
| config.grafana_annotations.categories.config_change.enabled | bool | `true` | ACL edits, device approval and churn, key lifecycle, user role changes, DNS and tailnet settings — the curated audit-log subset. Needs collectors.auditlogs. |
| config.grafana_annotations.categories.config_change.rollup | bool | `true` | Roll the category up into one region annotation per rollup_interval. On by default: it is the highest-volume source, and per-event markers draw a picket fence. |
| config.grafana_annotations.categories.expiry.enabled | bool | `true` | A node key or auth key entering its expiry warning window. Needs collectors.keys or collectors.devices. |
| config.grafana_annotations.categories.expiry.rollup | bool | `true` | Roll the category up. On by default: a fresh deployment finds every currently expiring key at once. |
| config.grafana_annotations.dashboard_uid | string | `""` | Confine annotations to ONE dashboard. Empty (default) writes organization annotations, visible on every board and in Explore. |
| config.grafana_annotations.dedupe_retention | string | `"48h"` | How long a published annotation's dedupe key is remembered, so a restart cannot republish it. Must comfortably exceed the longest source overlap window. |
| config.grafana_annotations.extra_tags | list | `[]` | Extra tags added to every annotation, e.g. [env:prod]. Every annotation already carries tailscale2otel, category:<c> and rule:<id>. |
| config.grafana_annotations.max_per_minute | int | `60` | Token-bucket ceiling on annotations written per process. Overage is dropped and counted, never delayed. |
| config.grafana_annotations.queue_size | int | `512` | Hand-off buffer between the collector goroutines and the single publisher. A full queue drops and counts rather than blocking collection. |
| config.grafana_annotations.rollup_interval | string | `"5m"` | Bucket width for rolled-up categories: one region annotation per interval per category per tailnet, instead of one marker per event. |
| config.grafana_annotations.state_file | string | `""` | Where the dedupe set persists. Empty puts annotations.json beside checkpoint.file_path, so it lands on the same persistence volume. Without a persistent volume a restart may republish recent annotations once. |
| config.grafana_annotations.timeout | string | `"10s"` | Per-request timeout for POST /api/annotations. |
| config.grafana_annotations.token | string | `""` | Grafana service-account token. It needs exactly one action, `annotations:create` on scope `annotations:type:organization`, and nothing else. Prefer existingSecret or token_file over putting it here: a non-empty value moves the whole rendered config into a Secret. |
| config.grafana_annotations.token_file | string | `""` | Path to a file holding the token (a mounted Secret). Value XOR file. |
| config.grafana_annotations.url | string | `""` | Grafana base URL, e.g. https://mystack.grafana.net. Setting it IS the opt-in for the annotation writer — the only thing this process writes anywhere. Empty disables the feature entirely (no client, no goroutine, no log line). Once set, the pod FAILS TO START unless the token can write an annotation, because a writer discovered broken at the first real event produced nothing when an operator went looking. |
| config.headscale.api_key | string | `""` | Bearer API key. Prefer the TS2OTEL_HEADSCALE__API_KEY secret over an inline value. |
| config.headscale.api_key_file | string | `""` | Read headscale.api_key from this path instead of an inline value (mounted-Secret style). Set the value or the file, not both; the file's content is whitespace-trimmed. |
| config.headscale.http.rate_limit | int | `0` |  |
| config.headscale.http.retry.base_delay | string | `"0s"` |  |
| config.headscale.http.retry.max_attempts | int | `0` |  |
| config.headscale.http.retry.max_delay | string | `"0s"` |  |
| config.headscale.http.timeout | string | `"30s"` | Per-request timeout for Headscale API calls (the only http knob applied in v1). |
| config.headscale.max_response_bytes | int | `4194304` | Cap on ONE Headscale API response body before it is decoded, in bytes. Sized from a measured ~715 B/node, so 4 MiB covers roughly 5,800 nodes. These endpoints are not paginated, so a larger deployment needs a larger value — raise the container memory limit alongside it, since decoding costs several times the wire size. A value above 64 MiB triggers a startup warning. |
| config.headscale.url | string | `""` | Headscale origin only (scheme + host, optional port; no path, credentials, query, or fragment), e.g. https://headscale.example.org. |
| config.ingress_wal.corruption | string | `"fail"` | Corruption policy. Only fail is supported: startup/drain stops rather than discarding data. |
| config.ingress_wal.directory | string | `"/var/lib/tailscale2otel/ingress-wal"` | Absolute, clean, non-root WAL directory. Must live on the state volume for persistence. |
| config.ingress_wal.enabled | bool | `false` | Persist accepted receiver bodies before acknowledging them. Off by default. |
| config.ingress_wal.max_bytes | int | `268435456` | Encoded WAL byte ceiling. Full WALs fail receiver requests closed; no TTL or eviction. |
| config.ingress_wal.max_entries | int | `10000` | Encoded WAL entry ceiling. Full WALs fail receiver requests closed; no TTL or eviction. |
| config.log_format | string | `"text"` | Operational log encoding: `text` or `json`. JSON is one record per line, for a cluster log pipeline that parses rather than greps. |
| config.log_level | string | `"info"` | Log verbosity: debug | info | warn | error. |
| config.otlp.batch.logs.export_interval | string | `"0s"` | How often a partial batch is flushed. 0 = SDK default. |
| config.otlp.batch.logs.export_max_batch_size | int | `0` | Records per export; must be <= max_queue_size when both are set. 0 = SDK default. |
| config.otlp.batch.logs.export_timeout | string | `"0s"` | Bound on one export attempt. 0 = SDK default. |
| config.otlp.batch.logs.max_queue_size | int | `0` | Records buffered before new ones are dropped (non-blocking by design). 0 = SDK default. |
| config.otlp.batch.traces.export_interval | string | `"0s"` | How often a partial batch is flushed. 0 = SDK default. |
| config.otlp.batch.traces.export_max_batch_size | int | `0` | Spans per export; must be <= max_queue_size when both are set. 0 = SDK default. |
| config.otlp.batch.traces.export_timeout | string | `"0s"` | Bound on one export attempt. 0 = SDK default. |
| config.otlp.batch.traces.max_queue_size | int | `0` | Spans buffered before new ones are dropped. 0 = SDK default. |
| config.otlp.compression | string | `""` | Request compression: gzip | none. Empty defers to OTEL_EXPORTER_OTLP[_<SIGNAL>]_COMPRESSION, then the exporter default. |
| config.otlp.credential_reload.enabled | bool | `false` | Governs only the background poller; last-known-good validation is always retained for a configured file regardless of this flag. |
| config.otlp.credential_reload.interval | string | `"30s"` | Poll period; minimum 5s. Ignored when enabled is false. |
| config.otlp.endpoint | string | `"https://otlp-gateway-prod-us-central-0.grafana.net/otlp"` | OTLP endpoint base URL. For Grafana Cloud use the otlp-gateway URL for YOUR region (the /v1/metrics and /v1/logs paths are appended automatically on the http protocol). |
| config.otlp.grafana_cloud.instance_id | string | `""` | Grafana Cloud instance/stack ID. Convenience: expands to an "Authorization: Basic <base64(instance_id:token)>" header. Set via TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID (secret). |
| config.otlp.grafana_cloud.token | string | `""` | Grafana Cloud OTLP token paired with instance_id. Set via TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN (secret). |
| config.otlp.grafana_cloud.token_file | string | `""` | Read the Grafana Cloud token from this path instead of an inline value. Set the value or the file, not both; the file's content is whitespace-trimmed. |
| config.otlp.grpc_reconnection_period | string | `"0s"` | Force a fresh gRPC connection attempt after this long. gRPC only; 0 = the gRPC client default. A rotated client certificate takes effect on both transports immediately, but a rotated CA bundle on gRPC only takes effect on the NEXT new connection — this bounds that. |
| config.otlp.headers | object | `{}` | Extra raw headers (alternative to grafana_cloud, e.g. for a non-Grafana backend). |
| config.otlp.limits.log_attribute_value_bytes | int | `4096` | Cap each individual string-valued log attribute. Never applied to metric labels, which must stay byte-exact or the series splits. Minimum 64. |
| config.otlp.limits.log_body_bytes | int | `32768` | Cap one log record's body before export. A receiver's request-body limit bounds a whole inbound request, but a valid request can still carry one enormous record that dominates a batch or breaches the backend's per-record limit. Truncation is UTF-8 safe, runs AFTER redaction so a secret can never be half-redacted, and leaves an explicit marker. Minimum 64; there is deliberately no unlimited setting. |
| config.otlp.logs | object | `{"compression":"","enabled":null,"endpoint":"","grpc_reconnection_period":"0s","headers":{},"max_request_size":0,"protocol":"","retry":{"enabled":null,"initial_interval":"0s","max_elapsed_time":"0s","max_interval":"0s"},"timeout":"0s","tls":{"ca_file":"","cert_file":"","insecure":null,"insecure_skip_verify":null,"key_file":""}}` | Send ONE signal (logs) somewhere else. Same inheritance rules as otlp.metrics above. |
| config.otlp.logs.compression | string | `""` | Empty inherits otlp.compression. |
| config.otlp.logs.enabled | string | `nil` | null inherits (the signal is on); false stops exporting this signal without disturbing the others. |
| config.otlp.logs.endpoint | string | `""` | Empty inherits otlp.endpoint. |
| config.otlp.logs.grpc_reconnection_period | string | `"0s"` | 0 inherits otlp.grpc_reconnection_period. |
| config.otlp.logs.headers | object | `{}` | REPLACES otlp.headers for this signal rather than merging. |
| config.otlp.logs.max_request_size | int | `0` | 0 inherits otlp.max_request_size. |
| config.otlp.logs.protocol | string | `""` | Empty inherits otlp.protocol. |
| config.otlp.logs.retry.enabled | string | `nil` | An untouched block inherits otlp.retry; setting ANY field overrides the whole policy for this signal. |
| config.otlp.logs.timeout | string | `"0s"` | 0 inherits otlp.timeout. |
| config.otlp.logs.tls.ca_file | string | `""` | Empty inherits otlp.tls.ca_file. |
| config.otlp.logs.tls.cert_file | string | `""` | Empty inherits otlp.tls.cert_file. |
| config.otlp.logs.tls.insecure | string | `nil` | null inherits; explicit true/false overrides. |
| config.otlp.logs.tls.insecure_skip_verify | string | `nil` | null inherits; explicit true/false overrides. |
| config.otlp.logs.tls.key_file | string | `""` | Empty inherits otlp.tls.key_file. |
| config.otlp.max_request_size | int | `0` | Bytes; a client-side REJECTION guard, not a splitter — it fails an oversized request instead of shipping it into a backend 413. Use metric_export_batch_size to actually stay under an ingest limit. 0 = no cap. |
| config.otlp.metric_export_batch_size | int | `10000` | Maximum datapoints per OTLP metric request. Serialized bytes vary with labels; lower values trade more requests for smaller payloads. |
| config.otlp.metric_interval | string | `"60s"` | How often metrics are pushed (the metric export interval). |
| config.otlp.metrics | object | `{"compression":"","enabled":null,"endpoint":"","grpc_reconnection_period":"0s","headers":{},"max_request_size":0,"protocol":"","retry":{"enabled":null,"initial_interval":"0s","max_elapsed_time":"0s","max_interval":"0s"},"timeout":"0s","tls":{"ca_file":"","cert_file":"","insecure":null,"insecure_skip_verify":null,"key_file":""}}` | Send ONE signal (metrics) somewhere else — a different collector, tenant, credential or protocol. Every field here inherits the matching otlp.* value above when left empty/null, EXCEPT headers, which REPLACES otlp.headers rather than merging — a credential never crosses a signal boundary. |
| config.otlp.metrics.compression | string | `""` | Empty inherits otlp.compression. |
| config.otlp.metrics.enabled | string | `nil` | null inherits (the signal is on); false stops exporting this signal without disturbing the others. |
| config.otlp.metrics.endpoint | string | `""` | Empty inherits otlp.endpoint. |
| config.otlp.metrics.grpc_reconnection_period | string | `"0s"` | 0 inherits otlp.grpc_reconnection_period. |
| config.otlp.metrics.headers | object | `{}` | REPLACES otlp.headers for this signal rather than merging. |
| config.otlp.metrics.max_request_size | int | `0` | 0 inherits otlp.max_request_size. |
| config.otlp.metrics.protocol | string | `""` | Empty inherits otlp.protocol. |
| config.otlp.metrics.retry.enabled | string | `nil` | An untouched block inherits otlp.retry; setting ANY field overrides the whole policy for this signal. |
| config.otlp.metrics.timeout | string | `"0s"` | 0 inherits otlp.timeout. |
| config.otlp.metrics.tls.ca_file | string | `""` | Empty inherits otlp.tls.ca_file. |
| config.otlp.metrics.tls.cert_file | string | `""` | Empty inherits otlp.tls.cert_file. |
| config.otlp.metrics.tls.insecure | string | `nil` | null inherits; explicit true/false overrides. |
| config.otlp.metrics.tls.insecure_skip_verify | string | `nil` | null inherits; explicit true/false overrides. |
| config.otlp.metrics.tls.key_file | string | `""` | Empty inherits otlp.tls.key_file. |
| config.otlp.protocol | string | `"http"` | Export protocol: http | grpc | stdout (stdout = local debug). |
| config.otlp.retry.enabled | string | `nil` | An explicit false genuinely disables retry (distinct from omitting this whole block, which keeps the exporter's own default of retry-on). Unset (null) here means "leave the exporter default". |
| config.otlp.retry.initial_interval | string | `"5s"` | First backoff delay. |
| config.otlp.retry.max_elapsed_time | string | `"1m"` | Give up after this long. |
| config.otlp.retry.max_interval | string | `"30s"` | Backoff ceiling. |
| config.otlp.stdout.metric_interval | string | `"5s"` | Metric push cadence when otlp.protocol is stdout — a short cadence so a debug run doesn't wait 60s for a metric. Logs and spans are emitted synchronously regardless. 0 = the stdout exporter's own default. |
| config.otlp.stdout.pretty | bool | `false` | Indent the emitted JSON. |
| config.otlp.timeout | string | `"0s"` | Per-request export timeout. 0 defers to OTEL_EXPORTER_OTLP[_<SIGNAL>]_TIMEOUT, then the exporter's 10s default. |
| config.otlp.tls.ca_file | string | `""` | Path to a CA bundle to verify the server certificate. |
| config.otlp.tls.cert_file | string | `""` | Client certificate for mutual TLS. |
| config.otlp.tls.insecure | bool | `false` | Disable transport security ENTIRELY (plaintext) — this is NOT a certificate-verification skip, see insecure_skip_verify for that. The Authorization header built from grafana_cloud rides on whatever transport this selects, so `true` puts that credential on the wire unencrypted. Only ever for a trusted in-cluster Collector, never across an untrusted network. |
| config.otlp.tls.insecure_skip_verify | bool | `false` | Keep TLS on but skip server-certificate verification (self-signed / private-CA gateways, testing only). Distinct from `insecure` above; prefer ca_file in production. |
| config.otlp.tls.key_file | string | `""` | Client private key for mutual TLS. |
| config.otlp.traces | object | `{"compression":"","enabled":null,"endpoint":"","grpc_reconnection_period":"0s","headers":{},"max_request_size":0,"protocol":"","retry":{"enabled":null,"initial_interval":"0s","max_elapsed_time":"0s","max_interval":"0s"},"timeout":"0s","tls":{"ca_file":"","cert_file":"","insecure":null,"insecure_skip_verify":null,"key_file":""}}` | Send ONE signal (traces) somewhere else. Same inheritance rules as otlp.metrics above; credentials are never shared across a signal boundary. |
| config.otlp.traces.compression | string | `""` | Empty inherits otlp.compression. |
| config.otlp.traces.enabled | string | `nil` | null inherits (the signal is on); false stops exporting this signal without disturbing the others. |
| config.otlp.traces.endpoint | string | `""` | Empty inherits otlp.endpoint. |
| config.otlp.traces.grpc_reconnection_period | string | `"0s"` | 0 inherits otlp.grpc_reconnection_period. |
| config.otlp.traces.headers | object | `{}` | REPLACES otlp.headers for this signal rather than merging. |
| config.otlp.traces.max_request_size | int | `0` | 0 inherits otlp.max_request_size. |
| config.otlp.traces.protocol | string | `""` | Empty inherits otlp.protocol. |
| config.otlp.traces.retry.enabled | string | `nil` | An untouched block inherits otlp.retry; setting ANY field overrides the whole policy for this signal. |
| config.otlp.traces.timeout | string | `"0s"` | 0 inherits otlp.timeout. |
| config.otlp.traces.tls.ca_file | string | `""` | Empty inherits otlp.tls.ca_file. |
| config.otlp.traces.tls.cert_file | string | `""` | Empty inherits otlp.tls.cert_file. |
| config.otlp.traces.tls.insecure | string | `nil` | null inherits; explicit true/false overrides. |
| config.otlp.traces.tls.insecure_skip_verify | string | `nil` | null inherits; explicit true/false overrides. |
| config.otlp.traces.tls.key_file | string | `""` | Empty inherits otlp.tls.key_file. |
| config.pii_filter.command_text | bool | `true` | Emit the verbatim `kubectl exec` command line on Kubernetes-audit logs. This is the only attribute a human types at a shell, so it can carry a pasted secret; it has its own toggle rather than sharing free_text_details. Turning it off KEEPS the bounded tailscale.k8s.command_class classification that the exec metrics are built on. |
| config.pii_filter.emails | bool | `true` | Emit user/actor login names (often emails). |
| config.pii_filter.endpoint_paths | bool | `true` | Emit Tailscale API endpoint paths (self-obs). |
| config.pii_filter.external_ips | bool | `true` | Emit public/routable addresses. |
| config.pii_filter.free_text_details | bool | `true` | Emit audit old/new/details, target names, key descriptions, posture values. |
| config.pii_filter.hostnames | bool | `true` | Emit device + collector-host hostnames. |
| config.pii_filter.internal_ips | bool | `true` | Emit RFC1918 / ULA / link-local addresses. |
| config.pii_filter.network_topology | bool | `true` | Emit route CIDRs, split-DNS domains, and search paths. |
| config.pii_filter.node_ids | bool | `true` | Emit Tailscale node IDs. |
| config.pii_filter.service_addrs | bool | `true` | Emit VIP service names. |
| config.pii_filter.tailnet_name | bool | `true` | Emit the tailnet identifier. |
| config.pii_filter.tailscale_ips | bool | `true` | Emit Tailscale-range addresses (100.64.0.0/10, fd7a:115c:a1e0::/48). |
| config.pii_filter.user_display_names | bool | `true` | Emit actor display (human) names. |
| config.pii_filter.user_ids | bool | `true` | Emit numeric/opaque user IDs (user.id). |
| config.profiling.block_profile_rate | int | `100000` | runtime.SetBlockProfileRate in nanoseconds; records blocking events averaging at least this long (100us) for both push and pull. Same on-by-default/applied-only-when-enabled rule as mutex_profile_fraction. 0 drops the block profile. |
| config.profiling.mutex_profile_fraction | int | `5` | runtime.SetMutexProfileFraction; samples 1/N mutex-contention events for both push and pull. On by default, but APPLIED ONLY when pprof or pyroscope is enabled, so it costs a non-profiling pod nothing. 0 drops the mutex profile. |
| config.profiling.pprof.enabled | bool | `false` | Mount net/http/pprof on the admin server so Grafana Alloy's pyroscope.scrape can PULL profiles. Requires admin.enabled + admin.auth.token. |
| config.profiling.pyroscope.basic_auth_password | string | `""` | Basic-auth password (Grafana Cloud: an access policy token with profiles:write). Set via TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_PASSWORD (secret). |
| config.profiling.pyroscope.basic_auth_password_file | string | `""` | Read the Pyroscope basic-auth password from this path instead of an inline value (mounted-Secret style). Set the value or the file, not both; content is whitespace-trimmed. |
| config.profiling.pyroscope.basic_auth_user | string | `""` | Basic-auth user (Grafana Cloud: the profiles instance ID). Set via TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_USER (secret). |
| config.profiling.pyroscope.credential_reload.enabled | bool | `false` | Governs only the background poller; last-known-good validation is always retained for a configured file regardless of this flag. |
| config.profiling.pyroscope.credential_reload.interval | string | `"30s"` | Poll period; minimum 5s. Ignored when enabled is false. |
| config.profiling.pyroscope.enabled | bool | `false` | Run the Pyroscope continuous-profiling push agent (pyroscope-go SDK). |
| config.profiling.pyroscope.headers | object | `{}` | Extra HTTP headers sent on every profile upload, e.g. { X-Api-Key: abc }. Values are secrets and redact from the status page and logs. Reserved headers (Authorization when basic auth is set, and the tenant header) win over anything set here. |
| config.profiling.pyroscope.server_address | string | `""` | Pyroscope/Grafana Cloud Profiles server URL. REQUIRED when enabled. |
| config.profiling.pyroscope.span_profiles.enabled | bool | `false` | Correlate sampled CPU profiles with trace spans, so a Grafana trace links straight to the profile. REQUIRES both tracing.enabled and profiling.pyroscope.enabled. CPU profiles ONLY — Go's runtime attaches pprof labels to CPU samples, so heap/mutex/block/goroutine profiles cannot carry span identity. |
| config.profiling.pyroscope.tags | object | `{}` | Extra static labels merged onto every profile, e.g. { env: prod }. |
| config.profiling.pyroscope.tailnet_label | string | `"off"` | off | hashed | name — whether continuous profiles carry a tailnet dimension. A tailnet name is a CUSTOMER identifier and profiles go to a different destination from metrics/logs, so this is opt-in and NOT covered by pii_filter. hashed = a stable 12-hex SHA-256 prefix (pseudonymous, not anonymous). Emitted only for a single configured tailnet; multi-tailnet gets no tag, since there is one profiler per process. |
| config.profiling.pyroscope.tenant_id | string | `""` | X-Scope-OrgID for multi-tenant servers (leave empty for Grafana Cloud). |
| config.profiling.pyroscope.tls.ca_file | string | `""` | PEM bundle of the CA to trust for the profiles endpoint (private CA / self-signed gateway). Must contain at least one certificate. |
| config.profiling.pyroscope.tls.cert_file | string | `""` | Client certificate for mutual TLS to the profiles endpoint. Set together with key_file. |
| config.profiling.pyroscope.tls.insecure_skip_verify | bool | `false` | Keep TLS on but skip server-certificate verification on profile uploads. A footgun — prefer ca_file with the gateway's CA. |
| config.profiling.pyroscope.tls.key_file | string | `""` | Client key paired with profiling.pyroscope.tls.cert_file. |
| config.profiling.pyroscope.upload_rate | string | `"60s"` | How often profiles are flushed to the server. |
| config.prometheus.auth.allow_unauthenticated | bool | `false` | Acknowledge serving /metrics with NO token on a network-reachable bind. In a cluster this is the normal case — the scrape is controlled by a NetworkPolicy rather than a credential — but it must be stated, because /metrics carries device names, flow endpoints and audit identities. A loopback bind never needs this. Ignored when token is set. |
| config.prometheus.auth.token | string | `""` | Gate /metrics behind this token (HTTP Basic password or "Authorization: Bearer <token>"). Empty on a network-reachable bind is REFUSED with 403 unless allow_unauthenticated is set. Set via TS2OTEL_PROMETHEUS__AUTH__TOKEN (secret). |
| config.prometheus.auth.token_file | string | `""` | Read prometheus.auth.token from this path instead of an inline value (mounted-Secret style). Set the value or the file, not both; the file's content is whitespace-trimmed. |
| config.prometheus.coalesce_gather | bool | `true` | Serve overlapping scrapes from the same in-flight gather instead of duplicating collection work. This costs slight staleness. |
| config.prometheus.enabled | bool | `false` | Backwards-compatible pull opt-in alongside OTLP. delivery.mode=prometheus or dual also enables it. |
| config.prometheus.listen | string | `"127.0.0.1:2112"` | Address the Prometheus endpoint binds. Default loopback only. Keep distinct from admin.listen. |
| config.prometheus.max_requests_in_flight | int | `4` | Cap concurrent /metrics gathers; excess scrapes get 503. A Gather walks every series in the registry, so N simultaneous slow scrapes cost N times that walk. Must be positive while prometheus.enabled is true; 0 meant unlimited before v4.0.0 and is now refused. |
| config.prometheus.timeout | string | `"8s"` | Give up on a single /metrics gather after this long, answering 503. Keep it below the scraper's own timeout so this process, not the scraper, decides. |
| config.prometheus.tls.cert_file | string | `""` | HTTPS certificate for the Prometheus endpoint. Set together with key_file (both-or-neither); leaving both empty serves plain HTTP. |
| config.prometheus.tls.client_auth | string | `""` | How strictly the client certificate is checked: require_and_verify (the default once client_ca_file is set), verify_if_given, require, request, or none. Only require_and_verify and verify_if_given actually validate the chain; the weaker modes are for staged rollouts. |
| config.prometheus.tls.client_ca_file | string | `""` | Require scrapers to present a client certificate signed by this CA (mutual TLS). Needs cert_file/key_file — a client CA on a plaintext listener never runs. Composes with auth.token: when both are set a request must satisfy both. |
| config.prometheus.tls.key_file | string | `""` | HTTPS private key paired with cert_file. Both paths must exist and be readable at startup. |
| config.provider | string | `"tailscale"` | Control-plane backend: tailscale (default) | headscale. Under headscale only the devices/users/keys/acl/nodemetrics collectors run; the Tailscale-only collectors auto-disable. |
| config.resource.attributes | object | `{}` | Custom Resource attributes, e.g. { deploy.team: platform }. Max 32 entries, 256-byte keys/values; reserved keys (service.name, service.version, service.instance.id, tailscale.tailnet, tailscale2otel.provider) are refused. |
| config.resource.deployment_environment | string | `""` | deployment.environment.name — outside service.*, so it lands in target_info only and may vary per environment. |
| config.resource.from_env | bool | `false` | Also read OTEL_RESOURCE_ATTRIBUTES / OTEL_SERVICE_NAME, filtered by the same rules. Off by default: it hands the ambient environment a channel onto a per-series label surface. |
| config.resource.service_namespace | string | `""` | service.namespace — promoted to a job-adjacent LABEL on every series. Keep it low-cardinality and stable across deploys. |
| config.self_observability.enabled | bool | `true` | Emit the exporter's own health metrics (scrape/api/export/build_info/enrich/runtime). |
| config.self_observability.instance_id | string | `""` | service.instance.id resource attribute; empty falls back to the pod/host name. Override with TS2OTEL_SELF_OBSERVABILITY__INSTANCE_ID (e.g. set to the pod name via the Downward API). |
| config.streaming.auto_configure | bool | `false` | PUT this receiver as a Splunk-HEC log-streaming sink on startup (requires public_url). Registers BOTH log types (network/flow AND configuration/audit), OVERWRITING any existing sink for either. NEVER enable against a tailnet whose streaming you do not intend to overwrite. |
| config.streaming.decompress | string | `"auto"` | Body decompression: auto | gzip | zstd | none. |
| config.streaming.enabled | bool | `false` | Enable the HEC-style streaming receiver. |
| config.streaming.listen | string | `":8088"` | Address the receiver binds (host:port). |
| config.streaming.max_body_bytes | int | `0` | Cap on DECOMPRESSED body; 0 = 64MiB default, <0 = unlimited (413 on exceed). When ingress_wal.enabled, an enabled receiver must set this explicitly to >0 and <=64MiB. |
| config.streaming.max_concurrent_requests | int | `0` | How many requests may buffer a body AT ONCE (max_body_bytes caps one body, this caps their sum); 0 = 4 default, <0 = unlimited (503 + Retry-After on exceed). Worst-case buffering is roughly this x max_body_bytes, so raise it together with resources.limits.memory. |
| config.streaming.path | string | `"/services/collector/event"` | HTTP path the receiver serves (the Splunk-HEC event endpoint). |
| config.streaming.public_url | string | `""` | Externally reachable receiver URL; REQUIRED when auto_configure: true. |
| config.streaming.routes | list | `[]` | FILE-ONLY multi-tailnet routes. Each item has tailnet, exact path, token or token_file, optional public_url, and optional auto_configure. Non-empty replaces legacy path/token identity. |
| config.streaming.tls.cert_file | string | `""` | TLS certificate file; set with key_file to serve the receiver over HTTPS. |
| config.streaming.tls.key_file | string | `""` | TLS private key file paired with cert_file. |
| config.streaming.token | string | `""` | Expected as 'Authorization: Splunk <token>'. Set via TS2OTEL_STREAMING__TOKEN (secret). |
| config.streaming.token_file | string | `""` | Read streaming.token from this path instead of an inline value (mounted-Secret style). Set the value or the file, not both; the file's content is whitespace-trimmed. |
| config.tailnets | list | `[]` | Multi-tailnet / MSP list. Optional; mutually exclusive with an explicit tailscale.tailnet (leave it "-" when using this). Each entry observes one tailnet and is self-contained (its own name + auth + http); credentials are NOT inherited from the tailscale block. Tailnet identity is emitted as the tailscale.tailnet OTEL resource attribute (one target_info per tailnet). Set per-tailnet secrets via TS2OTEL_* env is NOT supported for the list (file-only) — provide creds inline here or mount a config file. Streaming/webhook receivers require single-tailnet mode. Default empty (single tailnet from the tailscale block below). An entry may also carry its own `objectstore.flow` destination (endpoint/region/bucket/prefix plus the same credential and limit fields as collectors.flowlogs.objectstore). With this list non-empty nothing is inherited from that global block, and when config.collectors.flowlogs.source is `objectstore` EVERY entry must have a complete destination of its own and no two entries may name the same bucket+prefix — otherwise startup fails, naming the tailnet. Use `*_file` credential keys pointing at a mounted Secret to keep per-tailnet S3 keys out of this file. When this list is non-empty the chart renders the whole config.yaml into a dedicated Secret (<fullname>-config) instead of a ConfigMap, so the inline per-tailnet credentials never land in a ConfigMap readable by namespace viewers. |
| config.tailscale.auth.apikey | string | `""` | API key, used only when method: apikey. Set via TS2OTEL_TAILSCALE__AUTH__APIKEY (secret). |
| config.tailscale.auth.apikey_file | string | `""` | Read tailscale.auth.apikey from this path instead of an inline value. Set the value or the file, not both; the file's content is whitespace-trimmed. |
| config.tailscale.auth.method | string | `"oauth"` | Auth method: oauth (recommended) | apikey | workload_identity. Prefer an OAuth client (short-lived scoped tokens, not user-tied); a personal API key expires in <=90 days and is user-tied, and the exporter logs a WARN when apikey is selected. workload_identity is the keyless OIDC exchange — no stored secret at all, ideal for a Kubernetes ServiceAccount token. |
| config.tailscale.auth.oauth.client_id | string | `""` | OAuth client ID. Set via TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID (secret). |
| config.tailscale.auth.oauth.client_secret | string | `""` | OAuth client secret. Set via TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET (secret). |
| config.tailscale.auth.oauth.client_secret_file | string | `""` | Read the OAuth client secret from this path instead of an inline value. Point it at a projected Secret volume to keep the credential out of the pod's environment entirely. Set the value or the file, not both; the file's content is whitespace-trimmed. |
| config.tailscale.auth.oauth.scopes | list | `["all:read"]` | OAuth scopes to request. "all:read" covers every read-only collector. |
| config.tailscale.auth.workload_identity.client_id | string | `""` | Federated OAuth client ID from the Tailscale admin console. Required for this method. |
| config.tailscale.auth.workload_identity.id_token_file | string | `""` | Path to the OIDC ID token exchanged for a Tailscale access token. In Kubernetes this is a projected ServiceAccount token, e.g. /var/run/secrets/tokens/tailscale. Re-read on every exchange, so kubelet rotation is picked up without a restart. |
| config.tailscale.http.rate_limit | int | `0` | Global requests/sec across all collectors (0 = unlimited). |
| config.tailscale.http.retry.base_delay | string | `"500ms"` | Initial backoff delay; doubles each retry up to max_delay. |
| config.tailscale.http.retry.max_attempts | int | `4` | Max attempts per request (incl. the first) before giving up. |
| config.tailscale.http.retry.max_delay | string | `"10s"` | Ceiling on the per-retry backoff delay. |
| config.tailscale.http.timeout | string | `"30s"` | Per-request HTTP timeout for Tailscale API calls. |
| config.tailscale.max_log_response_bytes | int | `33554432` | Same ceiling for the bulk log pulls (flow logs, audit logs), which are legitimately multi-MB, in bytes (32 MiB). Decoding costs several times the wire size, so keep both budgets well under resources.limits.memory — above 64 MiB the app raises a startup advisory. |
| config.tailscale.max_response_bytes | int | `4194304` | Cap on the response body read from a Tailscale snapshot endpoint (devices, keys, dns, services, …) before it is decoded, in bytes (4 MiB). Bounds peak decode memory. Fleet-wide: a tailnets[] entry does NOT override it. |
| config.tailscale.tailnet | string | `"-"` | Tailnet name, or "-" for the auth principal's default tailnet (the default, which works out of the box for single-tailnet OAuth). Override with the TS2OTEL_TAILSCALE__TAILNET env var (set via secret above). |
| config.tracing | object | `{"enabled":false,"remote_parent":"trust","sampler":"parentbased_always_on","sampler_arg":1,"samplers":{"background":{"arg":0,"sampler":""},"receiver":{"arg":0,"sampler":""},"scrape":{"arg":0,"sampler":""}}}` | OTEL traces pillar (spans for the exporter's own work). OFF by default; reuses otlp.* for the endpoint/protocol/headers/TLS. |
| config.tracing.enabled | bool | `false` | Emit spans. When true, also enables trace-based exemplars on tailscale2otel.api.duration. |
| config.tracing.remote_parent | string | `"trust"` | How an INBOUND traceparent's sampled bit is treated by the stream/webhook receivers: trust (today's behavior) | ignore (the local sampler alone decides, so a sender cannot force sampling) | link (start a new local root trace and link the remote one). |
| config.tracing.sampler | string | `"parentbased_always_on"` | Head sampler (always_on|always_off|traceidratio|parentbased_always_on|parentbased_traceidratio). |
| config.tracing.sampler_arg | float | `1` | Sample ratio in [0,1] for the *traceidratio samplers (ignored otherwise). |
| config.tracing.samplers | object | `{"background":{"arg":0,"sampler":""},"receiver":{"arg":0,"sampler":""},"scrape":{"arg":0,"sampler":""}}` | Per-workload-class head sampler override. An empty sampler inherits tracing.sampler above, so an untouched block behaves exactly as the single global sampler. |
| config.tracing.samplers.background.arg | float | `0` | Ratio in [0,1] for the *traceidratio samplers. |
| config.tracing.samplers.background.sampler | string | `""` | Periodic non-scrape work, e.g. the release/update check. Empty inherits tracing.sampler. |
| config.tracing.samplers.receiver.arg | float | `0` | Ratio in [0,1] for the *traceidratio samplers. |
| config.tracing.samplers.receiver.sampler | string | `""` | One root span per HEC-stream / webhook request — usually the highest-rate class, so this is the one you turn down. Empty inherits tracing.sampler. |
| config.tracing.samplers.scrape.arg | float | `0` | Ratio in [0,1] for the *traceidratio samplers. |
| config.tracing.samplers.scrape.sampler | string | `""` | One root span per collector scrape cycle. Same enum as tracing.sampler; empty inherits it. |
| config.version_checks.cache_ttl | string | `"1h"` | How long a fetched "latest version" is cached before re-fetching (minimum 5m). |
| config.version_checks.devices.enabled | bool | `true` | Emit per-device tailscale.device.version_skew + fleet roll-ups (device client version vs latest Tailscale stable). Makes an outbound HTTPS call; fail-open. Needs the devices collector. |
| config.version_checks.devices.outdated_minor_threshold | int | `3` | A device this many minor releases behind the latest Tailscale stable counts toward tailscale.devices.outdated. |
| config.version_checks.self.enabled | bool | `true` | Emit tailscale2otel.update_available (running build vs latest tailscale2otel GitHub release). Makes an outbound HTTPS call; fail-open. Disable for air-gapped deployments. |
| config.version_checks.timeout | string | `"10s"` | Per-request timeout for the external version fetch. |
| config.webhook.dedup_audit_events | bool | `false` | Best-effort: drop a webhook event already counted via the audit logs (off by default). |
| config.webhook.enabled | bool | `false` | Enable the webhook receiver. |
| config.webhook.listen | string | `":8089"` | Address the receiver binds (host:port). |
| config.webhook.max_body_bytes | int | `0` | Cap on the RAW body read before signature verification; 0 = 1 MiB default, <0 = unlimited (413 on exceed). Distinct from streaming.max_body_bytes, which caps a decompressed body. When ingress_wal.enabled, an enabled receiver must set this explicitly to >0 and <=64MiB. |
| config.webhook.max_concurrent_requests | int | `0` | How many requests may buffer a body AT ONCE, before the HMAC is verified. max_body_bytes caps one body; this caps their sum, so unauthenticated senders cannot multiply it. 0 = 4 default, <0 = unlimited (503 + Retry-After on exceed). Worst-case buffered memory is roughly this x max_body_bytes, so raise it together with resources.limits.memory. |
| config.webhook.path | string | `"/tailscale/webhook"` | HTTP path the receiver serves. |
| config.webhook.routes | list | `[]` | FILE-ONLY multi-tailnet routes. Each item has tailnet and secret or secret_file. Non-empty replaces legacy path/secret identity; all events must name one route tailnet. |
| config.webhook.secret | string | `""` | HMAC-SHA256 verification secret. Empty is accepted only on loopback; otherwise the receiver refuses every request with HTTP 403 before reading its body. Set via TS2OTEL_WEBHOOK__SECRET (secret). |
| config.webhook.secret_file | string | `""` | Read webhook.secret from this path instead of an inline value (mounted-Secret style). Set the value or the file, not both; the file's content is whitespace-trimmed. |
| config.webhook.tls.cert_file | string | `""` | TLS certificate file; set with key_file for native webhook HTTPS. Leave both empty when an HTTPS reverse proxy terminates TLS. |
| config.webhook.tls.key_file | string | `""` | TLS private key file paired with cert_file. |
| config.webhook.tolerance | string | `"5m"` | Allowed clock skew in BOTH directions on the signed timestamp: a request older than now-tolerance or newer than now+tolerance is rejected. The two-sided check matters because a correctly signed but future-dated request would otherwise stay replayable. 0 disables the timestamp check entirely. |
| configStorage.mode | string | `"auto"` | Storage backend for the rendered `config.yaml`: `auto` | `secret` | `configmap`. `auto` (default) renders it into a ConfigMap while it is credential-free, and into Secret `<fullname>-config` as soon as any credential-bearing key is set inline under `config:` (`tailscale.auth.oauth.client_secret`, `tailscale.auth.apikey`, `headscale.api_key`, `otlp.grafana_cloud.token`, `otlp.headers`, the three `collectors.flowlogs.objectstore.*` and three `collectors.auditlogs.objectstore.*` credential keys, `streaming.token`, `webhook.secret`, `prometheus.auth.token`, `admin.auth.token`, `profiling.pyroscope.basic_auth_password`, any `tailnets[]` entry, or a `collectors.node_metrics.targets[]` entry with `bearer_token`/`headers`). A ConfigMap is readable by anyone holding `get configmaps` in the namespace, which is routinely granted far more widely than `get secrets`. `secret` always uses the Secret. `configmap` forces the ConfigMap and makes `helm template` FAIL, naming the offending keys, if a credential is set inline. |
| existingConfigKey | string | `"config.yaml"` | Key inside existingConfigMap/existingConfigSecret holding the config YAML. |
| existingConfigMap | string | `""` | Mount config.yaml from a ConfigMap YOU manage instead of one the chart renders. For GitOps/ExternalSecrets/SOPS setups where the config is produced outside Helm. Mutually exclusive with existingConfigSecret; either one makes the chart render no config object of its own and ignore the whole `config:` tree. |
| existingConfigSecret | string | `""` | Mount config.yaml from a Secret YOU manage. Same as existingConfigMap but for a config carrying credentials — this is how multi-tailnet credentials reach the pod without ever passing through Helm values (and so without appearing in `helm get values`). |
| existingSecret | string | `""` | Name of a pre-created Secret exposing the TS2OTEL_* env keys. When set, no Secret is rendered. |
| extraEnv | list | `[]` |  |
| extraEnvFrom | list | `[]` | Extra `envFrom` sources appended to the container's envFrom, as-is (configMapRef / secretRef). The chart's own Secret is always FIRST, so a later source here can deliberately override it — which is the documented way to let an external secret operator own a TS2OTEL_* value — while the chart can never silently override yours. Example:   extraEnvFrom:     - configMapRef:         name: proxy-config     - secretRef:         name: external-tailscale-creds |
| extraVolumeMounts | list | `[]` | Extra volume mounts appended to the main container's volumeMounts, as-is. Paired with extraVolumes above by name. |
| extraVolumes | list | `[]` | Extra volumes appended to the pod spec as-is (e.g. a Secret volume holding TLS cert/key material for config.streaming.tls or config.webhook.tls, since readOnlyRootFilesystem leaves no other place to put arbitrary files). Paired with extraVolumeMounts below by volume name. See the chart README for a worked TLS-cert example. |
| fullnameOverride | string | `""` | Fully override the generated resource names. |
| gateway.streaming.annotations | object | `{}` | Extra annotations on the HTTPRoute object. |
| gateway.streaming.enabled | bool | `false` | Render an HTTPRoute for the Splunk-HEC receiver. Requires service.streaming.enabled and config.streaming.token (or token_file). |
| gateway.streaming.hostnames | list | `[]` | Hostnames this route matches. Empty matches every hostname the parent Gateway listener accepts. |
| gateway.streaming.parentRefs | list | `[]` | Required. At least one Gateway API parentRef, e.g.:   parentRefs:     - name: my-gateway       namespace: gateway-system       sectionName: https |
| gateway.streaming.path | string | `"/"` | Path routed to the streaming receiver (matched as PathPrefix). |
| gateway.webhook.annotations | object | `{}` | Extra annotations on the HTTPRoute object. |
| gateway.webhook.enabled | bool | `false` | Render an HTTPRoute for the webhook receiver. Requires service.webhook.enabled and config.webhook.secret (or secret_file). |
| gateway.webhook.hostnames | list | `[]` | Hostnames this route matches. Tailscale only delivers webhooks to a public URL on 80/443, so this is usually the one hostname Tailscale calls. |
| gateway.webhook.parentRefs | list | `[]` | Required. At least one Gateway API parentRef — see streaming above for the shape. |
| gateway.webhook.path | string | `"/"` | Path routed to the webhook receiver (matched as PathPrefix). |
| goRuntime | object | `{"gogc":"200","memLimit":""}` | Go runtime tuning, injected as container env vars. This is a near-idle poller with a tiny live heap, so the Go default GOGC=100 fires frequent (individually cheap) collections that dominate the CPU profile; raising GOGC cuts that GC share. |
| goRuntime.gogc | string | `"200"` | GOGC: heap-growth percentage between collections (Go default 100). Empty ("") leaves the Go default. |
| goRuntime.memLimit | string | `""` | GOMEMLIMIT soft memory cap, e.g. "230MiB". Empty ("") auto-computes ~90% of resources.limits.memory (mirrors the docker-compose backstop; e.g. 256Mi -> 230MiB), falling back to unset if that limit is absent or in a unit outside Mi/Gi. Set explicitly to override the computed value. |
| image.digest | string | `""` | Pin the image by immutable digest instead of a mutable tag, e.g. "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855". When set, the rendered reference is `repository@digest` and image.tag / .Chart.AppVersion are IGNORED ENTIRELY — digest wins deterministically, never `repo:tag@digest`. Because the reference is then content-addressed, bumping the deployed image becomes an EXPLICIT values change to this field: pullPolicy no longer matters (there is nothing left for Always to re-resolve — the same digest always names the same content) and there is no more "new tag lands, next rollout picks it up automatically" side channel. Rotate this value deliberately, e.g. from the digest the release pipeline publishes alongside each tag. |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy. |
| image.repository | string | `"ghcr.io/rknightion/tailscale2otel"` | Container image repository. |
| image.tag | string | `""` | Image tag. Defaults to .Chart.appVersion when empty. IGNORED entirely when image.digest is set — see digest below. |
| imagePullSecrets | list | `[]` | Image pull secrets for private registries. |
| ingress.streaming.annotations | object | `{}` | Extra annotations (e.g. cert-manager.io/cluster-issuer for automated TLS). |
| ingress.streaming.className | string | `""` | IngressClassName. Empty ("") uses the cluster's default IngressClass. |
| ingress.streaming.enabled | bool | `false` | Render an Ingress for the Splunk-HEC receiver. Requires service.streaming.enabled and config.streaming.token (or token_file). |
| ingress.streaming.host | string | `""` | Required. The host this Ingress rule routes; a host-less rule is a catch-all that would swallow traffic for unrelated apps on the same controller. |
| ingress.streaming.path | string | `"/"` | Path routed to the streaming receiver. |
| ingress.streaming.pathType | string | `"Prefix"` | Ingress pathType. |
| ingress.streaming.tls.enabled | bool | `true` | Terminate TLS at the Ingress (the default). Tailscale streaming/webhook delivery requires HTTPS, so disabling this is only correct when a mesh or sidecar upstream of the controller terminates TLS for you instead. |
| ingress.streaming.tls.secretName | string | `""` | Secret holding the TLS cert/key. Leave empty when relying solely on a cert-manager (or similar) annotation to provision and name it for you — but at least one of secretName or an annotation must be set. |
| ingress.webhook.annotations | object | `{}` | Extra annotations (e.g. cert-manager.io/cluster-issuer for automated TLS). |
| ingress.webhook.className | string | `""` | IngressClassName. Empty ("") uses the cluster's default IngressClass. |
| ingress.webhook.enabled | bool | `false` | Render an Ingress for the webhook receiver. Requires service.webhook.enabled and config.webhook.secret (or secret_file). |
| ingress.webhook.host | string | `""` | Required. Tailscale only delivers webhooks to a public URL on 80/443, so this MUST be the hostname Tailscale is configured to call. |
| ingress.webhook.path | string | `"/"` | Path routed to the webhook receiver. |
| ingress.webhook.pathType | string | `"Prefix"` | Ingress pathType. |
| ingress.webhook.tls.enabled | bool | `true` | Terminate TLS at the Ingress (the default). Tailscale webhooks are HTTPS-only, so disabling this only makes sense when a mesh upstream of the controller terminates TLS instead — Tailscale itself will not deliver to a plaintext endpoint. |
| ingress.webhook.tls.secretName | string | `""` | Secret holding the TLS cert/key. Leave empty when relying solely on a cert-manager (or similar) annotation — but at least one of secretName or an annotation must be set. |
| metrics.externalPrometheusToken | string | `"auto"` | Whether an opaque source (`existingSecret`, `extraEnvFrom`, existingConfigMap, or existingConfigSecret) supplies a Prometheus auth token: `auto` when no opaque source owns it, otherwise declare `present` or `absent`. Helm cannot inspect those resources. A remote Service/monitor fails to render while this remains `auto`, avoiding a silent 401 or an unauthenticated Service. |
| metrics.podMonitor.bearerTokenSecret | object | `{}` | Bearer token for prometheus.auth.token, read from a Secret you manage. REQUIRED when that listener has a token: a scrape without it gets 401 and the target silently reports no data. Only a reference is rendered — never the value. |
| metrics.podMonitor.enabled | bool | `false` | Render a PodMonitor. Requires config.prometheus.enabled. A PodMonitor scrapes pods DIRECTLY, so it needs no Service — prefer it over serviceMonitor unless you specifically want the Service in the path. |
| metrics.podMonitor.interval | string | `""` | Scrape interval (e.g. 60s). Empty ("") inherits the Prometheus default. |
| metrics.podMonitor.labels | object | `{}` | Extra labels on the PodMonitor object (your Prometheus `podMonitorSelector` usually matches one). |
| metrics.podMonitor.metricRelabelings | list | `[]` | metric_relabel_configs applied to scraped samples. |
| metrics.podMonitor.path | string | `"/metrics"` | Metrics path on the Prometheus listener. |
| metrics.podMonitor.relabelings | list | `[]` | relabel_configs applied before the scrape. |
| metrics.podMonitor.sampleLimit | int | `0` | Cap on samples accepted per scrape; 0 leaves it unset. |
| metrics.podMonitor.scrapeTimeout | string | `""` | Per-scrape timeout. Empty ("") inherits the Prometheus default. |
| metrics.podMonitor.tlsConfig | object | `{}` | TLS settings passed to the scrape config (ca/cert/keySecret, serverName, insecureSkipVerify). Only Secret references, never inline material. |
| metrics.serviceMonitor.bearerTokenSecret | object | `{}` | Bearer token for prometheus.auth.token, read from a Secret you manage. REQUIRED when that listener has a token. Only a reference is rendered — never the value. |
| metrics.serviceMonitor.enabled | bool | `false` | Render a ServiceMonitor. Requires config.prometheus.enabled AND service.prometheus.enabled — a ServiceMonitor selects a Service, so without one it matches nothing and silently reports no data. Prefer podMonitor unless you specifically want the Service in the scrape path. |
| metrics.serviceMonitor.interval | string | `""` | Scrape interval. Empty ("") inherits the Prometheus default. |
| metrics.serviceMonitor.labels | object | `{}` | Extra labels on the ServiceMonitor object (your Prometheus `serviceMonitorSelector` usually matches one). |
| metrics.serviceMonitor.metricRelabelings | list | `[]` | metric_relabel_configs applied to scraped samples. |
| metrics.serviceMonitor.path | string | `"/metrics"` | Metrics path on the Prometheus listener. |
| metrics.serviceMonitor.relabelings | list | `[]` | relabel_configs applied before the scrape. |
| metrics.serviceMonitor.sampleLimit | int | `0` | Cap on samples accepted per scrape; 0 leaves it unset. |
| metrics.serviceMonitor.scrapeTimeout | string | `""` | Per-scrape timeout. Empty ("") inherits the Prometheus default. |
| metrics.serviceMonitor.tlsConfig | object | `{}` | TLS settings for the scrape (Secret references only, never inline material). |
| nameOverride | string | `""` | Override the chart name portion of resource names. |
| networkPolicy.egress.allowAll | bool | `true` | Allow ALL egress (the default). READ THIS before setting it false. Kubernetes NetworkPolicy has no hostname/DNS-name selector — only podSelector, namespaceSelector and ipBlock — so there is no portable way to express "the Tailscale control-plane API" (Tailscale publishes no stable IP range Tailscale itself commits to) or "an operator-chosen OTLP endpoint" (arbitrary, different per install). CNIs that add hostname-based egress (Cilium's CiliumNetworkPolicy, Calico's GlobalNetworkPolicy) solve this, but are NOT the portable networking.k8s.io/v1 API this chart emits, so this chart does not attempt one and will not invent an IP allowlist that would silently go stale. This policy's real value is its INGRESS half below, which CAN be made portable and correct — egress defaults to allowAll for exactly that reason. Setting this false keeps only egress.dns (if true) plus egress.extra, and WILL BREAK the exporter — it can reach neither the Tailscale API nor your OTLP endpoint — unless egress.extra covers both. If you need a real egress restriction, use your CNI's own policy CRD instead. |
| networkPolicy.egress.dns | bool | `true` | Allow DNS (UDP+TCP port 53) egress. Almost always required: the pod resolves the Tailscale API host, the OTLP endpoint host, and (if configured) an S3-compatible object-store endpoint by name. Only consulted while allowAll is false. |
| networkPolicy.egress.extra | list | `[]` | Raw NetworkPolicyEgressRule entries appended as-is (e.g. an ipBlock scoped to your OTLP gateway or object-store endpoint). Only consulted while allowAll is false. |
| networkPolicy.enabled | bool | `false` | Render a NetworkPolicy for this pod. |
| networkPolicy.ingress.extra | list | `[]` | Raw NetworkPolicyIngressRule entries appended as-is, alongside the rules this chart generates automatically for each enabled listener/consumer (see templates/networkpolicy.yaml). |
| nodeSelector | object | `{}` | Node selector for pod scheduling. |
| persistence.accessMode | string | `"ReadWriteOnce"` | PVC access mode. |
| persistence.enabled | bool | `false` | Persist process state across pod replacement/rescheduling. When false, an emptyDir is used: it survives container restarts within the same pod, but is lost with pod replacement, rescheduling, or node loss. When true, a PVC is created or existingClaim is mounted. |
| persistence.existingClaim | string | `""` | Use an existing PVC instead of creating one (empty = create one). Only used when enabled; persistence.enabled=true is required, and existingClaim may supply the durable volume instead of this chart creating it. |
| persistence.size | string | `"64Mi"` | PVC size. The existing 64Mi default suits checkpoints only. With the default 256Mi encoded WAL limit, request at least 512Mi for WAL entries plus staging files and metadata. If config.flows.store.directory also points into this volume (e.g. /var/lib/tailscale2otel/flows), size for that store separately — see the disk-sizing estimate in docs/flow-view.md — and add its estimate on top of the WAL/checkpoint figure above. |
| persistence.storageClass | string | `""` | StorageClass for the PVC (empty = cluster default). Only used when enabled. |
| podAnnotations | object | `{}` | Extra annotations for the pod. |
| podLabels | object | `{}` | Extra labels for the pod. |
| podSecurityContext | object | `{"fsGroup":65532,"fsGroupChangePolicy":"OnRootMismatch","runAsNonRoot":true,"seccompProfile":{"type":"RuntimeDefault"}}` | Pod-level security context. Runs as non-root with the RuntimeDefault seccomp profile; the app needs no special privileges. fsGroup makes the opt-in PVC persistence path (persistence.enabled=true) reliably writable by the uid-65532 container regardless of the CSI driver's default ownership behavior — a freshly provisioned block PVC is typically root:root on many drivers. The default emptyDir checkpoint volume already works without this (kubelet chmods emptyDir roots 0777, and the image pre-seeds /var/lib/tailscale2otel owned by 65532:65532). |
| probes.liveness.enabled | bool | `true` | Enable the liveness probe (GET /healthz). |
| probes.liveness.failureThreshold | int | `3` | Consecutive failures before the kubelet restarts the container. |
| probes.liveness.initialDelaySeconds | int | `5` | Seconds after container start before the first liveness probe. |
| probes.liveness.periodSeconds | int | `15` | Seconds between liveness probes. |
| probes.liveness.successThreshold | int | `1` | Consecutive successes required to consider the probe passing again. Kubernetes REJECTS any value other than 1 for a liveness probe — enforced here, not just documented. |
| probes.liveness.timeoutSeconds | int | `1` | Seconds before a liveness probe attempt times out. |
| probes.readiness.enabled | bool | `true` | Enable the readiness probe (GET /readyz). |
| probes.readiness.failureThreshold | int | `3` | Consecutive failures before the pod is marked NotReady (removed from Service endpoints). |
| probes.readiness.initialDelaySeconds | int | `5` | Seconds after container start before the first readiness probe. |
| probes.readiness.periodSeconds | int | `15` | Seconds between readiness probes. |
| probes.readiness.successThreshold | int | `1` | Consecutive successes required to mark the pod Ready again after a failure. Unlike liveness/startup, Kubernetes allows any value >=1 here. |
| probes.readiness.timeoutSeconds | int | `1` | Seconds before a readiness probe attempt times out. |
| probes.startup.enabled | bool | `false` | Enable a startup probe (GET /readyz). Off by default: opt in to shield a slow first initialization (e.g. the first poll cycle against a large multi-tailnet fleet) with its own budget, separate from the steady-state liveness timing above — while a startup probe is configured and has not yet succeeded, the kubelet does not run liveness/readiness at all, so a slow startup can never trip liveness before it has had a chance to become healthy. |
| probes.startup.failureThreshold | int | `30` | Consecutive failures before the container is considered to have failed startup and is restarted. Default budget: 30 x periodSeconds(10s) = 300s to become ready. |
| probes.startup.initialDelaySeconds | int | `0` | Seconds after container start before the first startup probe. |
| probes.startup.periodSeconds | int | `10` | Seconds between startup probes. |
| probes.startup.successThreshold | int | `1` | Consecutive successes before the startup probe hands off to liveness/readiness. Kubernetes REJECTS any value other than 1 for a startup probe — enforced here. |
| probes.startup.timeoutSeconds | int | `1` | Seconds before a startup probe attempt times out. |
| replicaCount | int | `1` | Replica count. MUST stay 1 — this is a singleton poller (no leader election or shared checkpoint coordination); scaling up would double-emit every metric and log against the same tailnet. Enforced by the generated values.schema.json AND a template `fail` guard (see templates/deployment.yaml) — any other value is rejected. |
| resources | object | `{"limits":{"cpu":"500m","memory":"256Mi"},"requests":{"cpu":"50m","memory":"64Mi"}}` | Resource requests and limits. The defaults suit a few-hundred-device tailnet; raise limits if you enable high-volume flow-log streaming or many node-metrics targets. |
| rolloutTrigger | string | `""` | Opaque value surfaced as the pod annotation `tailscale2otel.m7kni.io/rollout-trigger`. Changing it changes the pod template and forces a Recreate rollout. This is the supported way to pick up ANY secret-bearing change: a rotated `existingSecret`, a changed inline `secret:` value, or an edit to a secret-backed `config.yaml` (an inline credential under `config:`, a `tailnets[]` entry, or a node_metrics target bearer_token/headers). Kubernetes NEVER refreshes environment variables or Secret-mounted files in a running container, so credentials injected via `envFrom`/`secret.yaml`/`secret-config.yaml` stay stale until the pod is replaced. After rotating run e.g. `helm upgrade ... --set rolloutTrigger=$(date +%s)`. Pick anything you like (a timestamp, a git SHA, a counter) — it must NOT be a secret value or any digest of one, since annotations are readable by anyone who can read the Deployment (workload-read), a materially weaker grant than Secret-read (GHSA-825f-hph6-x65w: a published checksum of secret material lets that reader verify offline guesses against the real value). For an automated path instead, run Stakater Reloader in the cluster and set `podAnnotations.reloader.stakater.com/auto: "true"` (Reloader watches the Secret object's contents server-side and issues the rollout restart itself, so it never needs to publish a digest). The chart's own `checksum/config` annotation only ever hashes the rendered config when it is credential-free (ConfigMap-backed); a secret-backed config (Secret-backed) is never hashed anywhere — use rolloutTrigger or Reloader for that case too. |
| secret | object | `{"TS2OTEL_ADMIN__AUTH__TOKEN":"","TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__ACCESS_KEY_ID":"","TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__SECRET_ACCESS_KEY":"","TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__SESSION_TOKEN":"","TS2OTEL_HEADSCALE__API_KEY":"","TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID":"","TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN":"","TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_PASSWORD":"","TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_USER":"","TS2OTEL_PROMETHEUS__AUTH__TOKEN":"","TS2OTEL_STREAMING__TOKEN":"","TS2OTEL_TAILSCALE__AUTH__APIKEY":"","TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID":"","TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET":"","TS2OTEL_TAILSCALE__TAILNET":"","TS2OTEL_WEBHOOK__SECRET":""}` | Inline secret values rendered into a Secret and injected via envFrom. These TS2OTEL_* keys override the corresponding fields in the ConfigMap config.yaml at runtime — secrets never appear in the ConfigMap. Keys left empty ("") are NOT rendered into the Secret: an empty env var would override — and blank — the same key set under `config:` (env beats file), silently disabling e.g. receiver auth. |
| secret.TS2OTEL_ADMIN__AUTH__TOKEN | string | `""` | Shared token gating the admin status page (/ and /api/status.json) and pprof. With no token the page is served only on a loopback config.admin.listen and every other bind is refused with HTTP 403 — so a chart install that publishes the admin Service needs one. REQUIRED when you enable config.profiling.pprof. /healthz and /readyz are never gated. |
| secret.TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__ACCESS_KEY_ID | string | `""` | S3 access key for the flow-log export bucket. Set ONLY when config.collectors.flowlogs.source=objectstore AND the bucket has no role to assume (MinIO, Ceph). On EKS leave these empty and annotate the service account for IRSA instead. |
| secret.TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__SECRET_ACCESS_KEY | string | `""` | S3 secret key paired with the access key above. |
| secret.TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__SESSION_TOKEN | string | `""` | S3 session token. Temporary credentials only; usually unnecessary, since the ambient chain refreshes its own. |
| secret.TS2OTEL_HEADSCALE__API_KEY | string | `""` | Headscale Bearer API key. Used ONLY when config.provider=headscale. |
| secret.TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID | string | `""` | Grafana Cloud instance/stack ID (the numeric user for OTLP basic auth). |
| secret.TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN | string | `""` | Grafana Cloud OTLP token (the password for OTLP basic auth). |
| secret.TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_PASSWORD | string | `""` | Pyroscope basic-auth password (Grafana Cloud: an access policy token with profiles:write). |
| secret.TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_USER | string | `""` | Pyroscope basic-auth user (Grafana Cloud: the profiles instance ID). Set ONLY when you enable config.profiling.pyroscope. |
| secret.TS2OTEL_PROMETHEUS__AUTH__TOKEN | string | `""` | Token gating the Prometheus /metrics endpoint (HTTP Basic password or "Authorization: Bearer <token>"). With no token /metrics is refused with HTTP 403 on any network-reachable bind; loopback stays open, and config.prometheus.auth.allow_unauthenticated re-opens a network bind explicitly for an in-cluster scrape behind a NetworkPolicy. Set ONLY when prometheus.enabled. |
| secret.TS2OTEL_STREAMING__TOKEN | string | `""` | HEC token the streaming receiver expects ("Authorization: Splunk <token>"). Set ONLY when you enable config.streaming. With no token the receiver fails CLOSED: it serves only a loopback config.streaming.listen and refuses every other bind. |
| secret.TS2OTEL_TAILSCALE__AUTH__APIKEY | string | `""` | Tailscale API key. Used ONLY when config.tailscale.auth.method=apikey. Prefer OAuth: a personal API key expires in <=90 days and is tied to the user that created it (stops working when that user is removed). The exporter logs a WARN when method=apikey. |
| secret.TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID | string | `""` | OAuth client ID (recommended auth; needs the "all:read" scope). Used when config.tailscale.auth.method=oauth. |
| secret.TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET | string | `""` | OAuth client secret paired with TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID. |
| secret.TS2OTEL_TAILSCALE__TAILNET | string | `""` | Tailnet name (e.g. "example.com"), or "-" for the auth principal's default tailnet. |
| secret.TS2OTEL_WEBHOOK__SECRET | string | `""` | Webhook HMAC-SHA256 secret. Set ONLY when you enable config.webhook. Empty is accepted only on a loopback config.webhook.listen; network-reachable binds refuse every request with HTTP 403 before reading its body. |
| securityContext | object | `{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"readOnlyRootFilesystem":true,"runAsGroup":65532,"runAsUser":65532}` | Container-level security context. Drops all capabilities and runs with a read-only root filesystem (the app writes only to the optional checkpoint volume). Runs as the distroless `nonroot` uid/gid 65532 (a high, non-system id > 10000) to satisfy hardened-cluster policy. |
| service.admin.annotations | object | `{}` | Extra annotations on this Service (e.g. cloud LB or ServiceMonitor selectors). |
| service.admin.enabled | bool | `false` | Render a Service for the admin listener. Requires config.admin.auth.token. Probes do NOT need this — the kubelet reaches the pod directly. |
| service.admin.port | string | `""` | Service port. Empty ("") uses the listener's own port from config.admin.listen. |
| service.admin.type | string | `"ClusterIP"` | Service type. ClusterIP unless you have a specific reason; a LoadBalancer puts the listener on your cloud provider's public ingress path. |
| service.prometheus.annotations | object | `{}` | Extra annotations on this Service. |
| service.prometheus.enabled | bool | `false` | Render a Service for the Prometheus pull endpoint. Requires config.prometheus.auth.token. |
| service.prometheus.port | string | `""` | Service port. Empty ("") uses config.prometheus.listen's port. |
| service.prometheus.type | string | `"ClusterIP"` | Service type. |
| service.streaming.annotations | object | `{}` | Extra annotations on this Service. |
| service.streaming.enabled | bool | `false` | Render a Service for the Splunk-HEC receiver. Requires config.streaming.token. |
| service.streaming.port | string | `""` | Service port. Empty ("") uses config.streaming.listen's port. |
| service.streaming.type | string | `"ClusterIP"` | Service type. |
| service.webhook.annotations | object | `{}` | Extra annotations on this Service. |
| service.webhook.enabled | bool | `false` | Render a Service for the webhook receiver. Requires config.webhook.secret. |
| service.webhook.port | string | `""` | Service port. Empty ("") uses config.webhook.listen's port. |
| service.webhook.type | string | `"ClusterIP"` | Service type. |
| serviceAccount.annotations | object | `{}` | Annotations to add to the ServiceAccount. |
| serviceAccount.automountServiceAccountToken | bool | `false` | Automount the ServiceAccount API token into the pod. The exporter makes no Kubernetes API calls, so this defaults to false to drop an unused, attacker-useful credential from the network-facing pod. |
| serviceAccount.create | bool | `true` | Create a ServiceAccount. |
| serviceAccount.name | string | `""` | ServiceAccount name. Generated when empty. |
| terminationGracePeriodSeconds | int | `45` | Seconds the kubelet allows for a graceful stop before SIGKILL. Shutdown is STAGED and each stage is separately bounded in the binary: the receivers drain already-ACKed requests (10s), the ingress WAL performs one final drain (10s), then the OTLP pipeline flushes and shuts down (10s) — 30s worst case, plus headroom. Kubernetes' own default is 30s, which is exactly the drain with no margin, so a rollout could SIGKILL the pod mid-flush and lose the final flow rollup, the WAL backlog and the last export. Values below the enforced minimum make `helm template` fail rather than silently truncating a drain. Raise it if you raise a stage timeout; `internal/app` fails the build if the two stop agreeing (#332). |
| tolerations | list | `[]` | Tolerations for pod scheduling. |
| workloadIdentity.audience | string | `""` | Token audience. Empty ("") derives Tailscale's required value, `api.tailscale.com/<client id>`. Override only if Tailscale documents a different one. |
| workloadIdentity.enabled | bool | `false` | Mount a projected, audience-scoped ServiceAccount token and point the exporter at it. Requires config.tailscale.auth.method: workload_identity and a client_id. |
| workloadIdentity.expirationSeconds | int | `3600` | Requested token lifetime in seconds. The kubelet refreshes the file in place at ~80% of this, and the exporter re-reads it on every token exchange, so rotation needs no restart. Kubernetes clamps values below 600. |
| workloadIdentity.fileName | string | `"token"` | Filename within mountPath. |
| workloadIdentity.mountPath | string | `"/var/run/secrets/tailscale"` | Directory the token is projected into. |

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.14.2](https://github.com/norwoodj/helm-docs/releases/v1.14.2)
