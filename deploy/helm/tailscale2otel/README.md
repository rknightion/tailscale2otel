# tailscale2otel

![Version: 0.2.0](https://img.shields.io/badge/Version-0.2.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.1.0](https://img.shields.io/badge/AppVersion-0.1.0-informational?style=flat-square)

Poll the Tailscale API and export OpenTelemetry metrics + logs (OTLP). Optimized for Grafana Cloud.

**Homepage:** <https://github.com/rknightion/tailscale2otel>

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| rknightion |  |  |

## Source Code

* <https://github.com/rknightion/tailscale2otel>

## Configuration

The entire application config lives under a single top-level `config:` key in
`values.yaml` (the single source of truth, kept in sync with
`config.example.yaml`). It is rendered verbatim into a ConfigMap as `config.yaml`.
Secrets are `${ENV}` placeholders expanded at runtime from the injected Secret.

Helm deep-merges maps, so single-key overrides work without restating the rest:

```sh
helm install t deploy/helm/tailscale2otel \
  --set secret.TS_OAUTH_CLIENT_ID=... \
  --set secret.TS_OAUTH_CLIENT_SECRET=... \
  --set secret.GC_INSTANCE_ID=... \
  --set secret.GC_OTLP_TOKEN=... \
  --set config.log_level=debug
```

See [CHANGELOG.md](./CHANGELOG.md) for the breaking 0.2.0 migration (config moved
under `config:`).

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` | Affinity rules for pod scheduling. |
| config.admin.enabled | bool | `false` |  |
| config.admin.listen | string | `":9090"` | Serves /healthz and /readyz. |
| config.cardinality.collapse_external | bool | `true` | Bucket unresolved IPs as external/unknown. |
| config.cardinality.flow_include_ports | bool | `false` | Keep ports OFF flow metrics (always on flow logs). |
| config.cardinality.flow_node_dims | bool | `true` | Include src/dst device names on flow metrics. |
| config.checkpoint.file_path | string | `"/var/lib/tailscale2otel/checkpoints.json"` |  |
| config.checkpoint.store | string | `"memory"` | Checkpoint store: memory | file. |
| config.collectors.acl.enabled | bool | `true` |  |
| config.collectors.acl.interval | string | `"600s"` |  |
| config.collectors.auditlogs.enabled | bool | `true` |  |
| config.collectors.auditlogs.initial_lookback | string | `"5m"` |  |
| config.collectors.auditlogs.interval | string | `"60s"` |  |
| config.collectors.auditlogs.lag | string | `"60s"` |  |
| config.collectors.auditlogs.max_window | string | `"6h"` |  |
| config.collectors.auditlogs.source | string | `"poll"` |  |
| config.collectors.devices.collect_posture | bool | `false` |  |
| config.collectors.devices.collect_routes | bool | `false` |  |
| config.collectors.devices.enabled | bool | `true` |  |
| config.collectors.devices.interval | string | `"60s"` |  |
| config.collectors.dns.enabled | bool | `true` |  |
| config.collectors.dns.interval | string | `"600s"` |  |
| config.collectors.flowlogs.enabled | bool | `true` |  |
| config.collectors.flowlogs.initial_lookback | string | `"5m"` |  |
| config.collectors.flowlogs.interval | string | `"60s"` |  |
| config.collectors.flowlogs.lag | string | `"120s"` |  |
| config.collectors.flowlogs.log_mode | string | `"per_connection"` |  |
| config.collectors.flowlogs.max_log_records_per_window | int | `0` |  |
| config.collectors.flowlogs.max_window | string | `"1h"` |  |
| config.collectors.flowlogs.source | string | `"poll"` |  |
| config.collectors.keys.enabled | bool | `true` |  |
| config.collectors.keys.expiry_warn | string | `"168h"` |  |
| config.collectors.keys.interval | string | `"300s"` |  |
| config.collectors.node_metrics.enabled | bool | `false` |  |
| config.collectors.node_metrics.interval | string | `"60s"` |  |
| config.collectors.node_metrics.targets | list | `[]` |  |
| config.collectors.node_metrics.timeout | string | `"10s"` |  |
| config.collectors.settings.enabled | bool | `true` |  |
| config.collectors.settings.interval | string | `"600s"` |  |
| config.collectors.users.enabled | bool | `true` |  |
| config.collectors.users.interval | string | `"300s"` |  |
| config.enrichment.cache_ttl | string | `"5m"` | Staleness alarm threshold for the device cache. |
| config.log_level | string | `"info"` | Log verbosity: debug | info | warn | error. |
| config.otlp.endpoint | string | `"https://otlp-gateway-prod-us-central-0.grafana.net/otlp"` |  |
| config.otlp.grafana_cloud.instance_id | string | `"${GC_INSTANCE_ID}"` |  |
| config.otlp.grafana_cloud.token | string | `"${GC_OTLP_TOKEN}"` |  |
| config.otlp.headers | object | `{}` | Extra raw headers (alternative to grafana_cloud). |
| config.otlp.metric_interval | string | `"60s"` | How often metrics are pushed. |
| config.otlp.protocol | string | `"http"` | Export protocol: http | grpc | stdout (stdout = local debug). |
| config.otlp.tls.ca_file | string | `""` |  |
| config.otlp.tls.cert_file | string | `""` |  |
| config.otlp.tls.insecure | bool | `false` |  |
| config.otlp.tls.key_file | string | `""` |  |
| config.self_observability.enabled | bool | `true` | Emit the exporter's own health metrics. |
| config.streaming.auto_configure | bool | `false` | PUT this receiver as a Splunk-HEC log-streaming sink on startup. |
| config.streaming.decompress | string | `"auto"` | Body decompression: auto | gzip | zstd | none. |
| config.streaming.enabled | bool | `false` |  |
| config.streaming.listen | string | `":8088"` |  |
| config.streaming.max_body_bytes | int | `0` | Cap on DECOMPRESSED body; 0 = 64MiB default, <0 = unlimited (413 on exceed). |
| config.streaming.path | string | `"/services/collector/event"` |  |
| config.streaming.public_url | string | `""` | Externally reachable receiver URL; REQUIRED when auto_configure: true. |
| config.streaming.tls.cert_file | string | `""` |  |
| config.streaming.tls.key_file | string | `""` |  |
| config.streaming.token | string | `"${TS_STREAM_HEC_TOKEN}"` | Expected as 'Authorization: Splunk <token>'. |
| config.tailscale.auth.apikey | string | `"${TS_API_KEY}"` | API key, used only when method: apikey. |
| config.tailscale.auth.method | string | `"oauth"` | Auth method: oauth (recommended) | apikey. |
| config.tailscale.auth.oauth.client_id | string | `"${TS_OAUTH_CLIENT_ID}"` |  |
| config.tailscale.auth.oauth.client_secret | string | `"${TS_OAUTH_CLIENT_SECRET}"` |  |
| config.tailscale.auth.oauth.scopes[0] | string | `"all:read"` |  |
| config.tailscale.auth.oauth.token_url | string | `"https://api.tailscale.com/api/v2/oauth/token"` |  |
| config.tailscale.http.rate_limit | int | `0` | Global requests/sec across all collectors (0 = unlimited). |
| config.tailscale.http.retry.base_delay | string | `"500ms"` |  |
| config.tailscale.http.retry.max_attempts | int | `4` |  |
| config.tailscale.http.retry.max_delay | string | `"10s"` |  |
| config.tailscale.http.timeout | string | `"30s"` |  |
| config.tailscale.tailnet | string | `"${TS_TAILNET}"` | Tailnet name, or "-" for the auth principal's default tailnet. |
| config.webhook.dedup_audit_events | bool | `false` | Best-effort: drop a webhook event already counted via the audit logs. |
| config.webhook.enabled | bool | `false` |  |
| config.webhook.listen | string | `":8089"` |  |
| config.webhook.path | string | `"/tailscale/webhook"` |  |
| config.webhook.secret | string | `"${TS_WEBHOOK_SECRET}"` | HMAC-SHA256 verification secret. |
| existingSecret | string | `""` | Name of a pre-created Secret exposing the env keys below. When set, no Secret is rendered. |
| fullnameOverride | string | `""` | Fully override the generated resource names. |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy. |
| image.repository | string | `"ghcr.io/rknightion/tailscale2otel"` | Container image repository. |
| image.tag | string | `""` | Image tag. Defaults to .Chart.appVersion when empty. |
| imagePullSecrets | list | `[]` | Image pull secrets for private registries. |
| nameOverride | string | `""` | Override the chart name portion of resource names. |
| nodeSelector | object | `{}` | Node selector for pod scheduling. |
| podAnnotations | object | `{}` | Extra annotations for the pod. |
| podLabels | object | `{}` | Extra labels for the pod. |
| podSecurityContext | object | `{"runAsNonRoot":true,"seccompProfile":{"type":"RuntimeDefault"}}` | Pod-level security context. |
| replicaCount | int | `1` | Replica count. Keep at 1 — this is a singleton poller (no leader election); scaling up would double-emit telemetry. |
| resources | object | `{"limits":{"cpu":"500m","memory":"256Mi"},"requests":{"cpu":"50m","memory":"64Mi"}}` | Resource requests and limits. |
| secret | object | `{"GC_INSTANCE_ID":"","GC_OTLP_TOKEN":"","TS_API_KEY":"","TS_OAUTH_CLIENT_ID":"","TS_OAUTH_CLIENT_SECRET":"","TS_STREAM_HEC_TOKEN":"","TS_TAILNET":"","TS_WEBHOOK_SECRET":""}` | Inline secret values rendered into a Secret and injected via envFrom. These keys back the ${ENV} placeholders in `config` below. |
| securityContext | object | `{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"readOnlyRootFilesystem":true}` | Container-level security context. |
| serviceAccount.annotations | object | `{}` | Annotations to add to the ServiceAccount. |
| serviceAccount.create | bool | `true` | Create a ServiceAccount. |
| serviceAccount.name | string | `""` | ServiceAccount name. Generated when empty. |
| tolerations | list | `[]` | Tolerations for pod scheduling. |

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.14.2](https://github.com/norwoodj/helm-docs/releases/v1.14.2)
