# tailscale2otel

![Version: 0.6.0](https://img.shields.io/badge/Version-0.6.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.1.0](https://img.shields.io/badge/AppVersion-0.1.0-informational?style=flat-square)

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
Secrets are injected exclusively via `TS2OTEL_*` environment variables from the
Secret — they never appear in the ConfigMap.

Helm deep-merges maps, so single-key overrides work without restating the rest:

```sh
helm install t deploy/helm/tailscale2otel \
  --set "secret.TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID=..." \
  --set "secret.TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET=..." \
  --set "secret.TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID=..." \
  --set "secret.TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN=..." \
  --set config.log_level=debug
```

See [CHANGELOG.md](./CHANGELOG.md) for the breaking 0.2.0 migration (config moved
under `config:`) and the 0.5.0 migration (secret keys renamed to `TS2OTEL_*`,
`${VAR}` placeholders removed from config).

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` | Affinity rules for pod scheduling. |
| config.admin.auth.token | string | `""` | Shared secret gating the status page and pprof (HTTP Basic password or "Authorization: Bearer <token>"); /healthz and /readyz stay open. Set via TS2OTEL_ADMIN__AUTH__TOKEN (secret). Required when profiling.pprof.enabled. |
| config.admin.enabled | bool | `false` | Enable the admin probe server. |
| config.admin.landing_page | bool | `true` | Serve the human status page at / and machine-readable JSON at /api/status.json. |
| config.admin.listen | string | `":9090"` | Address the admin server binds; serves /healthz and /readyz. Bind to loopback/tailnet for defense-in-depth. |
| config.cardinality.derp_region_rollup | bool | `true` | Emit per-DERP-region rollup gauges (tailscale.derp.region.*) on the devices collector. |
| config.cardinality.flow.collapse_external | bool | `true` | Bucket unresolved IPs as external/unknown to cap cardinality. Affects flow LOGS and, when node_dims is true, the src/dst node labels on flow METRICS. |
| config.cardinality.flow.destination_port | bool | `false` | Add destination.port to flow METRICS (independent of source_port). |
| config.cardinality.flow.destination_service | bool | `false` | Add tailscale.dst.service (IANA service name, e.g. tcp/443->https) to flow METRICS — a bounded stand-in for destination.port; always on flow LOGS. |
| config.cardinality.flow.exit_node_attribution | bool | `true` | Emit the bounded tailscale.exit_node.io/packets counters attributing exit traffic to the relaying node (bounded by exit-node count); independent of metrics_mode. |
| config.cardinality.flow.metrics_mode | string | `"rollup"` | Which flow metric families to emit: rollup (default) | all | both. rollup = bounded top-N *.rollup families (busiest src/dst node pairs by bytes; remainder folds into an __other__ series so totals are preserved; no L4 ports; adds tailscale.network.unique.* gauges). all = raw per-connection tailscale.network.io/packets shaped by the toggles below. both = emit both (~2x series; summing double-counts). |
| config.cardinality.flow.node_dims | bool | `true` | Include src/dst device names as dimensions on flow metrics. |
| config.cardinality.flow.rollup_top_n | int | `500` | Rollup only: busiest src/dst node pairs kept per flush; the rest fold into __other__. 0 = default (500). |
| config.cardinality.flow.source_port | bool | `false` | Add source.port to flow METRICS (independent of destination_port); ports are always on flow LOGS. |
| config.cardinality.metric_limit | int | `10000` | Hard per-metric series cap (0/negative = unlimited). |
| config.cardinality.per_entity.device | bool | `true` | Emit per-device gauges (online/last_seen/key.expiry/derp/routes); false emits only the aggregate tailscale.devices.count rollup. |
| config.cardinality.per_entity.key | bool | `true` | Emit the per-key expiry gauge; false emits only tailscale.keys.count (the key-expiry warning log still fires). |
| config.cardinality.per_entity.service | bool | `true` | Emit the per-service ports/hosts gauges; false emits only tailscale.services.count. |
| config.cardinality.per_entity.user | bool | `true` | Emit per-user gauges (devices/connected/last_seen); false emits only tailscale.users.count. |
| config.cardinality.per_entity.webhook | bool | `true` | Emit the per-endpoint webhook subscriptions gauge; false emits only tailscale.webhook_endpoints.count. |
| config.cardinality.subnet_route_rollup | bool | `true` | Emit the per-CIDR tailscale.subnet_routes.routers redundancy gauge (one series per subnet CIDR); the fleet exit/subnet count aggregates emit regardless. |
| config.checkpoint.file_path | string | `"/var/lib/tailscale2otel/checkpoints.json"` | Checkpoint file path when store: file (mount a writable volume here). |
| config.checkpoint.store | string | `"file"` | Checkpoint store: memory | file. "memory" loses window cursors on restart (re-does initial_lookback); "file" persists them atomically (needs a writable volume at file_path). |
| config.collectors.acl.enabled | bool | `true` | Enable the ACL/policy collector (acl.last_changed, acl.size, acl.rules by section). |
| config.collectors.acl.interval | string | `"600s"` | Poll interval. |
| config.collectors.auditlogs.enabled | bool | `true` | Enable the configuration-audit-logs collector. |
| config.collectors.auditlogs.initial_lookback | string | `"5m"` | Cold-start lookback on first run. |
| config.collectors.auditlogs.interval | string | `"60s"` | Poll interval. |
| config.collectors.auditlogs.lag | string | `"60s"` | Tail-hazard lag (see flowlogs.lag). |
| config.collectors.auditlogs.max_window | string | `"6h"` | Maximum width of a single poll window. |
| config.collectors.auditlogs.source | string | `"poll"` | Ingestion source: poll | stream | both. Pick ONE method per log type (see flowlogs.source): `both` risks double-counting and de-dup is only a best-effort failsafe (WARNed at startup). Set `stream` when config.streaming.enabled is true. |
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
| config.collectors.flowlogs.source | string | `"poll"` | Ingestion source: poll | stream | both. PICK ONE method per log type: `both` runs poll AND the `streaming` receiver and risks double-counting — cross-source de-duplication is a best-effort FAILSAFE, not a guarantee. The exporter logs a WARN at startup when streaming is enabled while this collector still polls. Set `stream` (not poll/both) when config.streaming.enabled is true. |
| config.collectors.keys.enabled | bool | `true` | Enable the auth/API keys collector (key.expiry, keys.count). |
| config.collectors.keys.expiry_warn | string | `"168h"` | Emit a tailscale.key.expiring WARN log when a key expires within this window. |
| config.collectors.keys.interval | string | `"300s"` | Poll interval. |
| config.collectors.log_stream.enabled | bool | `true` | Enable the log-streaming delivery-health collector. Self-gates to configured=0 (no error) when no SIEM sink is configured for a log type. |
| config.collectors.log_stream.interval | string | `"600s"` | Poll interval. |
| config.collectors.node_metrics.drop_labels | list | `[]` | Label keys stripped from every forwarded series (the instance label is never dropped). |
| config.collectors.node_metrics.enabled | bool | `false` | Enable the node-metrics scraper. Requires at least one entry in `targets`. |
| config.collectors.node_metrics.interval | string | `"60s"` | Scrape interval for every target. |
| config.collectors.node_metrics.metric_allow | list | `[]` | Passthrough allow-list: anchored regex on the forwarded metric NAME; if non-empty, only matching names are forwarded. |
| config.collectors.node_metrics.metric_deny | list | `[]` | Passthrough deny-list: anchored regex; names matching any are dropped (after metric_allow). |
| config.collectors.node_metrics.targets | list | `[]` | List of scrape targets. Each: {url, instance, labels{}, bearer_token, bearer_token_file, headers{}, tls{insecure,ca_file,cert_file,key_file}}. The "instance" label is the node identity. |
| config.collectors.node_metrics.timeout | string | `"10s"` | Per-scrape HTTP timeout. |
| config.collectors.posture_integrations.enabled | bool | `true` | Enable the device-posture-integration collector (MDM/EDR matched counts + last_sync staleness). |
| config.collectors.posture_integrations.interval | string | `"600s"` | Poll interval. |
| config.collectors.services.collect_hosts | bool | `false` | Also collect per-service backing-host detail (approval/configured state) — one extra API call per service (N+1). Off by default. |
| config.collectors.services.enabled | bool | `true` | Enable the Tailscale Services (VIP) collector (services.count + per-service ports/hosts). |
| config.collectors.services.interval | string | `"600s"` | Poll interval. |
| config.collectors.settings.enabled | bool | `true` | Enable the tailnet-settings collector (setting.enabled flags, key-duration). |
| config.collectors.settings.interval | string | `"600s"` | Poll interval (settings change rarely). |
| config.collectors.users.enabled | bool | `true` | Enable the users collector (users.count, per-user devices/connected/last_seen). |
| config.collectors.users.interval | string | `"300s"` | Poll interval (user data changes slowly). |
| config.collectors.webhooks.enabled | bool | `true` | Enable the webhook-endpoint inventory collector (count + per-endpoint subscriptions; no url/secret). |
| config.collectors.webhooks.interval | string | `"600s"` | Poll interval. |
| config.enrichment.cache_ttl | string | `"5m"` | Staleness alarm threshold for the device-enrichment cache (drives the tailscale2otel.enrich.cache_age self-obs gauge); does not evict entries. |
| config.enrichment.reverse_dns.cache_ttl | string | `"24h"` | How long a resolved name is cached (PTRs rarely change, so a long TTL keeps resolver load low). |
| config.enrichment.reverse_dns.enabled | bool | `false` | Opt-in reverse-DNS (PTR) enrichment of EXTERNAL flow addresses; resolved names replace the "external" bucket in tailscale.src/dst.node (flow logs always; flow metrics when cardinality.flow.node_dims is on). On flow METRICS this can add ~one series per external IP. |
| config.enrichment.reverse_dns.max_entries | int | `50000` | Cache size bound; new external IPs beyond this are not resolved (~150 bytes/entry). |
| config.enrichment.reverse_dns.negative_ttl | string | `"5m"` | How long a failed lookup is remembered (suppresses retries). |
| config.enrichment.reverse_dns.server | string | `""` | Resolver to query as "ip" or "ip:port" (default port 53); empty = system/container resolver. |
| config.enrichment.reverse_dns.timeout | string | `"2s"` | Per-lookup timeout. |
| config.headscale.api_key | string | `""` | Bearer API key. Prefer the TS2OTEL_HEADSCALE__API_KEY secret over an inline value. |
| config.headscale.http.rate_limit | int | `0` |  |
| config.headscale.http.retry.base_delay | string | `"0s"` |  |
| config.headscale.http.retry.max_attempts | int | `0` |  |
| config.headscale.http.retry.max_delay | string | `"0s"` |  |
| config.headscale.http.timeout | string | `"30s"` | Per-request timeout for Headscale API calls (the only http knob applied in v1). |
| config.headscale.url | string | `""` | Headscale control-plane base URL, e.g. https://headscale.example.org. |
| config.log_level | string | `"info"` | Log verbosity: debug | info | warn | error. |
| config.otlp.endpoint | string | `"https://otlp-gateway-prod-us-central-0.grafana.net/otlp"` | OTLP endpoint base URL. For Grafana Cloud use the otlp-gateway URL for YOUR region (the /v1/metrics and /v1/logs paths are appended automatically on the http protocol). |
| config.otlp.grafana_cloud.instance_id | string | `""` | Grafana Cloud instance/stack ID. Convenience: expands to an "Authorization: Basic <base64(instance_id:token)>" header. Set via TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID (secret). |
| config.otlp.grafana_cloud.token | string | `""` | Grafana Cloud OTLP token paired with instance_id. Set via TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN (secret). |
| config.otlp.headers | object | `{}` | Extra raw headers (alternative to grafana_cloud, e.g. for a non-Grafana backend). |
| config.otlp.metric_interval | string | `"60s"` | How often metrics are pushed (the metric export interval). |
| config.otlp.protocol | string | `"http"` | Export protocol: http | grpc | stdout (stdout = local debug). |
| config.otlp.tls.ca_file | string | `""` | Path to a CA bundle to verify the server certificate. |
| config.otlp.tls.cert_file | string | `""` | Client certificate for mutual TLS. |
| config.otlp.tls.insecure | bool | `false` | Skip TLS certificate verification (insecure; for local/testing only). |
| config.otlp.tls.key_file | string | `""` | Client private key for mutual TLS. |
| config.profiling.block_profile_rate | int | `0` | runtime.SetBlockProfileRate (nanoseconds); >0 enables block profiling for both push and pull. |
| config.profiling.mutex_profile_fraction | int | `0` | runtime.SetMutexProfileFraction; >0 enables mutex profiling for both push and pull. |
| config.profiling.pprof.enabled | bool | `false` | Mount net/http/pprof on the admin server so Grafana Alloy's pyroscope.scrape can PULL profiles. Requires admin.enabled + admin.auth.token. |
| config.profiling.pyroscope.basic_auth_password | string | `""` | Basic-auth password (Grafana Cloud: an access policy token with profiles:write). Set via TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_PASSWORD (secret). |
| config.profiling.pyroscope.basic_auth_user | string | `""` | Basic-auth user (Grafana Cloud: the profiles instance ID). Set via TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_USER (secret). |
| config.profiling.pyroscope.enabled | bool | `false` | Run the Pyroscope continuous-profiling push agent (pyroscope-go SDK). |
| config.profiling.pyroscope.server_address | string | `""` | Pyroscope/Grafana Cloud Profiles server URL. REQUIRED when enabled. |
| config.profiling.pyroscope.tags | object | `{}` | Extra static labels merged onto every profile, e.g. { env: prod }. |
| config.profiling.pyroscope.tenant_id | string | `""` | X-Scope-OrgID for multi-tenant servers (leave empty for Grafana Cloud). |
| config.profiling.pyroscope.upload_rate | string | `"15s"` | How often profiles are flushed to the server. |
| config.provider | string | `"tailscale"` | Control-plane backend: tailscale (default) | headscale. Under headscale only the devices/users/keys/acl/nodemetrics collectors run; the Tailscale-only collectors auto-disable. |
| config.self_observability.enabled | bool | `true` | Emit the exporter's own health metrics (scrape/api/export/build_info/enrich/runtime). |
| config.self_observability.instance_id | string | `""` | service.instance.id resource attribute; empty falls back to the pod/host name. Override with TS2OTEL_SELF_OBSERVABILITY__INSTANCE_ID (e.g. set to the pod name via the Downward API). |
| config.streaming.auto_configure | bool | `false` | PUT this receiver as a Splunk-HEC log-streaming sink on startup (requires public_url). NEVER enable against a tailnet whose streaming you do not intend to overwrite. |
| config.streaming.decompress | string | `"auto"` | Body decompression: auto | gzip | zstd | none. |
| config.streaming.enabled | bool | `false` | Enable the HEC-style streaming receiver. |
| config.streaming.listen | string | `":8088"` | Address the receiver binds (host:port). |
| config.streaming.max_body_bytes | int | `0` | Cap on DECOMPRESSED body; 0 = 64MiB default, <0 = unlimited (413 on exceed). |
| config.streaming.path | string | `"/services/collector/event"` | HTTP path the receiver serves (the Splunk-HEC event endpoint). |
| config.streaming.public_url | string | `""` | Externally reachable receiver URL; REQUIRED when auto_configure: true. |
| config.streaming.tls.cert_file | string | `""` | TLS certificate file; set with key_file to serve the receiver over HTTPS. |
| config.streaming.tls.key_file | string | `""` | TLS private key file paired with cert_file. |
| config.streaming.token | string | `""` | Expected as 'Authorization: Splunk <token>'. Set via TS2OTEL_STREAMING__TOKEN (secret). |
| config.tailscale.auth.apikey | string | `""` | API key, used only when method: apikey. Set via TS2OTEL_TAILSCALE__AUTH__APIKEY (secret). |
| config.tailscale.auth.method | string | `"oauth"` | Auth method: oauth (recommended) | apikey. Prefer an OAuth client (short-lived scoped tokens, not user-tied); a personal API key expires in <=90 days and is user-tied, and the exporter logs a WARN when apikey is selected. |
| config.tailscale.auth.oauth.client_id | string | `""` | OAuth client ID. Set via TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID (secret). |
| config.tailscale.auth.oauth.client_secret | string | `""` | OAuth client secret. Set via TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET (secret). |
| config.tailscale.auth.oauth.scopes | list | `["all:read"]` | OAuth scopes to request. "all:read" covers every read-only collector. |
| config.tailscale.http.rate_limit | int | `0` | Global requests/sec across all collectors (0 = unlimited). |
| config.tailscale.http.retry.base_delay | string | `"500ms"` | Initial backoff delay; doubles each retry up to max_delay. |
| config.tailscale.http.retry.max_attempts | int | `4` | Max attempts per request (incl. the first) before giving up. |
| config.tailscale.http.retry.max_delay | string | `"10s"` | Ceiling on the per-retry backoff delay. |
| config.tailscale.http.timeout | string | `"30s"` | Per-request HTTP timeout for Tailscale API calls. |
| config.tailscale.tailnet | string | `""` | Tailnet name, or "-" for the auth principal's default tailnet. Override with TS2OTEL_TAILSCALE__TAILNET env var (set via secret above). |
| config.tracing | object | `{"enabled":false,"sampler":"parentbased_always_on","sampler_arg":1}` | OTEL traces pillar (spans for the exporter's own work). OFF by default; reuses otlp.* for the endpoint/protocol/headers/TLS. |
| config.tracing.enabled | bool | `false` | Emit spans. When true, also enables trace-based exemplars on tailscale2otel.api.duration. |
| config.tracing.sampler | string | `"parentbased_always_on"` | Head sampler (always_on|always_off|traceidratio|parentbased_always_on|parentbased_traceidratio). |
| config.tracing.sampler_arg | float | `1` | Sample ratio in [0,1] for the *traceidratio samplers (ignored otherwise). |
| config.version_checks.cache_ttl | string | `"1h"` | How long a fetched "latest version" is cached before re-fetching (minimum 5m). |
| config.version_checks.devices.enabled | bool | `true` | Emit per-device tailscale.device.version_skew + fleet roll-ups (device client version vs latest Tailscale stable). Makes an outbound HTTPS call; fail-open. Needs the devices collector. |
| config.version_checks.devices.outdated_minor_threshold | int | `3` | A device this many minor releases behind the latest Tailscale stable counts toward tailscale.devices.outdated. |
| config.version_checks.self.enabled | bool | `true` | Emit tailscale2otel.update_available (running build vs latest tailscale2otel GitHub release). Makes an outbound HTTPS call; fail-open. Disable for air-gapped deployments. |
| config.version_checks.timeout | string | `"10s"` | Per-request timeout for the external version fetch. |
| config.webhook.dedup_audit_events | bool | `false` | Best-effort: drop a webhook event already counted via the audit logs (off by default). |
| config.webhook.enabled | bool | `false` | Enable the webhook receiver. |
| config.webhook.listen | string | `":8089"` | Address the receiver binds (host:port). |
| config.webhook.path | string | `"/tailscale/webhook"` | HTTP path the receiver serves. |
| config.webhook.secret | string | `""` | HMAC-SHA256 verification secret. Empty SKIPS verification (accepts unsigned POSTs). Set via TS2OTEL_WEBHOOK__SECRET (secret). |
| existingSecret | string | `""` | Name of a pre-created Secret exposing the TS2OTEL_* env keys. When set, no Secret is rendered. |
| fullnameOverride | string | `""` | Fully override the generated resource names. |
| goRuntime | object | `{"gogc":"200","memLimit":""}` | Go runtime tuning, injected as container env vars. This is a near-idle poller with a tiny live heap, so the Go default GOGC=100 fires frequent (individually cheap) collections that dominate the CPU profile; raising GOGC cuts that GC share. |
| goRuntime.gogc | string | `"200"` | GOGC: heap-growth percentage between collections (Go default 100). Empty ("") leaves the Go default. |
| goRuntime.memLimit | string | `""` | GOMEMLIMIT soft memory cap, e.g. "230MiB" (keep ~10% under resources.limits.memory). Empty ("") leaves it unset. |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy. |
| image.repository | string | `"ghcr.io/rknightion/tailscale2otel"` | Container image repository. |
| image.tag | string | `""` | Image tag. Defaults to .Chart.appVersion when empty. |
| imagePullSecrets | list | `[]` | Image pull secrets for private registries. |
| nameOverride | string | `""` | Override the chart name portion of resource names. |
| nodeSelector | object | `{}` | Node selector for pod scheduling. |
| persistence.accessMode | string | `"ReadWriteOnce"` | PVC access mode. |
| persistence.enabled | bool | `false` | Persist checkpoints (window cursors) across restarts. When false, an emptyDir is used (survives container restarts within a pod, but not rescheduling); when true, a PVC is created. |
| persistence.existingClaim | string | `""` | Use an existing PVC instead of creating one (empty = create one). Only used when enabled. |
| persistence.size | string | `"64Mi"` | PVC size (checkpoints are tiny). |
| persistence.storageClass | string | `""` | StorageClass for the PVC (empty = cluster default). Only used when enabled. |
| podAnnotations | object | `{}` | Extra annotations for the pod. |
| podLabels | object | `{}` | Extra labels for the pod. |
| podSecurityContext | object | `{"runAsNonRoot":true,"seccompProfile":{"type":"RuntimeDefault"}}` | Pod-level security context. Runs as non-root with the RuntimeDefault seccomp profile; the app needs no special privileges. |
| replicaCount | int | `1` | Replica count. Keep at 1 — this is a singleton poller (no leader election); scaling up would double-emit every metric and log. |
| resources | object | `{"limits":{"cpu":"500m","memory":"256Mi"},"requests":{"cpu":"50m","memory":"64Mi"}}` | Resource requests and limits. The defaults suit a few-hundred-device tailnet; raise limits if you enable high-volume flow-log streaming or many node-metrics targets. |
| secret | object | `{"TS2OTEL_ADMIN__AUTH__TOKEN":"","TS2OTEL_HEADSCALE__API_KEY":"","TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID":"","TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN":"","TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_PASSWORD":"","TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_USER":"","TS2OTEL_STREAMING__TOKEN":"","TS2OTEL_TAILSCALE__AUTH__APIKEY":"","TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID":"","TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET":"","TS2OTEL_TAILSCALE__TAILNET":"","TS2OTEL_WEBHOOK__SECRET":""}` | Inline secret values rendered into a Secret and injected via envFrom. These TS2OTEL_* keys override the corresponding fields in the ConfigMap config.yaml at runtime — secrets never appear in the ConfigMap. |
| secret.TS2OTEL_ADMIN__AUTH__TOKEN | string | `""` | Shared token gating the admin status page (/ and /api/status.json) and pprof. Empty leaves the status page open (a WARN fires if it's exposed on a wildcard bind); REQUIRED when you enable config.profiling.pprof. /healthz and /readyz are never gated. |
| secret.TS2OTEL_HEADSCALE__API_KEY | string | `""` | Headscale Bearer API key. Used ONLY when config.provider=headscale. |
| secret.TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID | string | `""` | Grafana Cloud instance/stack ID (the numeric user for OTLP basic auth). |
| secret.TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN | string | `""` | Grafana Cloud OTLP token (the password for OTLP basic auth). |
| secret.TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_PASSWORD | string | `""` | Pyroscope basic-auth password (Grafana Cloud: an access policy token with profiles:write). |
| secret.TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_USER | string | `""` | Pyroscope basic-auth user (Grafana Cloud: the profiles instance ID). Set ONLY when you enable config.profiling.pyroscope. |
| secret.TS2OTEL_STREAMING__TOKEN | string | `""` | HEC token the streaming receiver expects ("Authorization: Splunk <token>"). Set ONLY when you enable config.streaming. Empty makes streaming token auth a no-op. |
| secret.TS2OTEL_TAILSCALE__AUTH__APIKEY | string | `""` | Tailscale API key. Used ONLY when config.tailscale.auth.method=apikey. Prefer OAuth: a personal API key expires in <=90 days and is tied to the user that created it (stops working when that user is removed). The exporter logs a WARN when method=apikey. |
| secret.TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID | string | `""` | OAuth client ID (recommended auth; needs the "all:read" scope). Used when config.tailscale.auth.method=oauth. |
| secret.TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET | string | `""` | OAuth client secret paired with TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID. |
| secret.TS2OTEL_TAILSCALE__TAILNET | string | `""` | Tailnet name (e.g. "example.com"), or "-" for the auth principal's default tailnet. |
| secret.TS2OTEL_WEBHOOK__SECRET | string | `""` | Webhook HMAC-SHA256 secret. Set ONLY when you enable config.webhook. CRITICAL: leaving this empty makes config.webhook.secret empty, which SKIPS HMAC verification entirely, so unauthenticated webhook POSTs are accepted. Always set a secret when exposing the webhook. |
| securityContext | object | `{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"readOnlyRootFilesystem":true,"runAsGroup":65532,"runAsUser":65532}` | Container-level security context. Drops all capabilities and runs with a read-only root filesystem (the app writes only to the optional checkpoint volume). Runs as the distroless `nonroot` uid/gid 65532 (a high, non-system id > 10000) to satisfy hardened-cluster policy. |
| serviceAccount.annotations | object | `{}` | Annotations to add to the ServiceAccount. |
| serviceAccount.automountServiceAccountToken | bool | `false` | Automount the ServiceAccount API token into the pod. The exporter makes no Kubernetes API calls, so this defaults to false to drop an unused, attacker-useful credential from the network-facing pod. |
| serviceAccount.create | bool | `true` | Create a ServiceAccount. |
| serviceAccount.name | string | `""` | ServiceAccount name. Generated when empty. |
| tolerations | list | `[]` | Tolerations for pod scheduling. |

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.14.2](https://github.com/norwoodj/helm-docs/releases/v1.14.2)
