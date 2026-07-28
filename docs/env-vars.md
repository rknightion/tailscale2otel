---
title: Environment Variables
description: Every TS2OTEL_* environment variable, its default, and what it controls
---

# Environment-variable reference

Every configuration field is settable from an environment variable, so a container
deployment needs no mounted config file at all (and the env layer overrides any
file that *is* present — keep secrets here, never in YAML). See
[`configuration.md`](configuration.md) for the layering model and the prose
reference, and [`../config.example.yaml`](https://github.com/rknightion/tailscale2otel/blob/main/config.example.yaml) for the same
fields as a commented file.

**Naming.** Take the dotted config key, prefix it with `TS2OTEL_`, uppercase it,
and replace each `.` with `__` (a single `_` inside a name is preserved):

```text
tailscale.auth.oauth.client_secret  ->  TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET
collectors.flowlogs.interval        ->  TS2OTEL_COLLECTORS__FLOWLOGS__INTERVAL
```

**Lists** are comma-separated (e.g. `TS2OTEL_TAILSCALE__AUTH__OAUTH__SCOPES=all:read,log_streaming`).
A `TS2OTEL_*` variable that matches no known key is logged as a startup `WARN`.

> This table is **generated** from [`../config.example.yaml`](https://github.com/rknightion/tailscale2otel/blob/main/config.example.yaml).
> Do not edit between the markers; run `scripts/regen-generated.sh envref` (or
> `go test ./internal/config -run TestEnvReferenceDocInSync -update`) to refresh it.

<!-- BEGIN GENERATED: env-vars -->

| Environment variable | Default | Description |
| --- | --- | --- |
| `TS2OTEL_LOG_LEVEL` | `info` | exporter's own log verbosity: debug \| info \| warn \| error |
| `TS2OTEL_LOG_FORMAT` | `text` | operational log encoding: text \| json (json = one record per line) |
| `TS2OTEL_PROVIDER` | `tailscale` | control-plane backend: tailscale (default) \| headscale |
| `TS2OTEL_HEADSCALE__URL` | `""` | Headscale control-plane base URL, e.g. https://headscale.example.org (TS2OTEL_HEADSCALE__URL) |
| `TS2OTEL_HEADSCALE__API_KEY` | `""` | Bearer API key — keep in env (TS2OTEL_HEADSCALE__API_KEY) |
| `TS2OTEL_HEADSCALE__API_KEY_FILE` | `""` | read the value from this file instead (Docker secrets); set the value or the file, not both; content is whitespace-trimmed |
| `TS2OTEL_HEADSCALE__HTTP__TIMEOUT` | `30s` | per-request timeout (the ONLY http knob applied in v1) |
| `TS2OTEL_HEADSCALE__HTTP__RETRY__MAX_ATTEMPTS` | `0` | accepted for parity with tailscale.http but NOT applied by the minimal v1 Headscale client |
| `TS2OTEL_HEADSCALE__HTTP__RETRY__BASE_DELAY` | `0s` | accepted for parity with tailscale.http but NOT applied by the minimal v1 Headscale client |
| `TS2OTEL_HEADSCALE__HTTP__RETRY__MAX_DELAY` | `0s` | accepted for parity with tailscale.http but NOT applied by the minimal v1 Headscale client |
| `TS2OTEL_HEADSCALE__HTTP__RATE_LIMIT` | `0` | HTTP client used for all Headscale API calls |
| `TS2OTEL_HEADSCALE__MAX_RESPONSE_BYTES` | `4194304` | cap (4 MiB) on ONE Headscale API response body before decoding; ~5800 nodes at ~715 B each — raise it (and the container memory limit) on a bigger deployment, these endpoints are not paginated |
| `TS2OTEL_TAILSCALE__TAILNET` | `-` | "-" = the authenticated principal's default tailnet (works out of the box); or set your tailnet's name explicitly, e.g. "example.com" |
| `TS2OTEL_TAILSCALE__AUTH__METHOD` | `oauth` | oauth (recommended) \| apikey \| workload_identity (keyless OIDC exchange) |
| `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID` | `""` | OAuth client ID (set via TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID) |
| `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET` | `""` | OAuth client secret — keep in env, not here (..._CLIENT_SECRET) |
| `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET_FILE` | `""` | read the value from this file instead (Docker secrets); set the value or the file, not both; content is whitespace-trimmed |
| `TS2OTEL_TAILSCALE__AUTH__OAUTH__SCOPES` | `[all:read]` | least-privilege read scopes requested for the token _(comma-separated list)_ |
| `TS2OTEL_TAILSCALE__AUTH__APIKEY` | `""` | personal API key (set via TS2OTEL_TAILSCALE__AUTH__APIKEY); used only when method: apikey — expires <=90d and is tied to its creator |
| `TS2OTEL_TAILSCALE__AUTH__APIKEY_FILE` | `""` | read the value from this file instead (Docker secrets); set the value or the file, not both; content is whitespace-trimmed |
| `TS2OTEL_TAILSCALE__AUTH__WORKLOAD_IDENTITY__CLIENT_ID` | `""` | federated OAuth client ID from the Tailscale admin console (TS2OTEL_TAILSCALE__AUTH__WORKLOAD_IDENTITY__CLIENT_ID) |
| `TS2OTEL_TAILSCALE__AUTH__WORKLOAD_IDENTITY__ID_TOKEN_FILE` | `""` | path to the OIDC ID token, e.g. a Kubernetes projected service-account token; re-read on every exchange (rotation-safe) |
| `TS2OTEL_TAILSCALE__HTTP__TIMEOUT` | `30s` | per-attempt timeout (each retry attempt; a retried request may take longer, and long Retry-After waits are honored) |
| `TS2OTEL_TAILSCALE__HTTP__RETRY__MAX_ATTEMPTS` | `4` | total attempts per request (1 = no retry); exponential backoff between tries |
| `TS2OTEL_TAILSCALE__HTTP__RETRY__BASE_DELAY` | `500ms` | initial backoff delay |
| `TS2OTEL_TAILSCALE__HTTP__RETRY__MAX_DELAY` | `10s` | backoff ceiling |
| `TS2OTEL_TAILSCALE__HTTP__RATE_LIMIT` | `0` | global requests/sec across ALL collectors (0 = unlimited) |
| `TS2OTEL_TAILSCALE__MAX_RESPONSE_BYTES` | `4194304` | cap (4 MiB) on ONE snapshot-endpoint response body before decoding; ~2400 devices at ~1.8 KiB each — raise it (and the container memory limit) on a bigger tailnet, these endpoints are not paginated |
| `TS2OTEL_TAILSCALE__MAX_LOG_RESPONSE_BYTES` | `33554432` | cap (32 MiB) on ONE flow-log/audit-log response body; ~12000 flow records per poll — shrink the collector's window instead of raising this if you hit it |
| `TS2OTEL_OTLP__PROTOCOL` | `http` | http \| grpc \| stdout (stdout = print signals to the console for local debug, no backend) |
| `TS2OTEL_OTLP__ENDPOINT` | `https://otlp-gateway-prod-us-central-0.grafana.net/otlp` | OTLP base URL (the exporter appends /v1/metrics and /v1/logs itself) |
| `TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID` | `""` | Grafana Cloud stack/instance ID (set via TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID) |
| `TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN` | `""` | Grafana Cloud OTLP token — keep in env (..._GRAFANA_CLOUD__TOKEN) |
| `TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN_FILE` | `""` | read the value from this file instead (Docker secrets); set the value or the file, not both; content is whitespace-trimmed |
| `TS2OTEL_OTLP__TLS__INSECURE` | `false` | disable TLS entirely (plaintext h2c/http) — NOT a cert-verify skip; do not use in production |
| `TS2OTEL_OTLP__TLS__INSECURE_SKIP_VERIFY` | `false` | keep TLS but skip server-cert verification (self-signed/private-CA gateways, testing only — prefer ca_file) |
| `TS2OTEL_OTLP__TLS__CA_FILE` | `""` | path to a custom CA bundle to trust |
| `TS2OTEL_OTLP__TLS__CERT_FILE` | `""` | client certificate path (for mutual TLS) |
| `TS2OTEL_OTLP__TLS__KEY_FILE` | `""` | client private key path (for mutual TLS) |
| `TS2OTEL_OTLP__METRIC_INTERVAL` | `60s` | how often metrics are pushed (60s aligns with a 1-data-point-per-minute scrape) |
| `TS2OTEL_OTLP__METRIC_EXPORT_BATCH_SIZE` | `10000` | maximum datapoints per OTLP metric request; lower this when a backend has a small request-size limit (serialized bytes vary with labels) |
| `TS2OTEL_ENRICHMENT__CACHE_TTL` | `5m` | staleness alarm threshold for the IP/nodeID -> name device cache |
| `TS2OTEL_ENRICHMENT__REVERSE_DNS__ENABLED` | `false` | off by default (can add ~one flow-metric series per external IP when on) |
| `TS2OTEL_ENRICHMENT__REVERSE_DNS__SERVER` | `""` | resolver "ip" or "ip:port" (default :53); empty = system/container resolver |
| `TS2OTEL_ENRICHMENT__REVERSE_DNS__TIMEOUT` | `2s` | per-lookup timeout |
| `TS2OTEL_ENRICHMENT__REVERSE_DNS__CACHE_TTL` | `24h` | how long a resolved name is cached (PTRs rarely change, so a long TTL keeps resolver load low) |
| `TS2OTEL_ENRICHMENT__REVERSE_DNS__NEGATIVE_TTL` | `5m` | how long a failed lookup is remembered (suppresses retries) |
| `TS2OTEL_ENRICHMENT__REVERSE_DNS__MAX_ENTRIES` | `50000` | PTR cache size bound (new external IPs beyond this are not resolved; ~150 bytes/entry) |
| `TS2OTEL_ENRICHMENT__REVERSE_DNS__ACKNOWLEDGE_CARDINALITY` | `false` | set true (once metric_limit is sized) to silence the enabled+node_dims cardinality advisory |
| `TS2OTEL_CARDINALITY__METRIC_LIMIT` | `10000` | hard per-metric series cap; beyond it the SDK collapses extras into otel_metric_overflow (0/negative = unlimited) |
| `TS2OTEL_CARDINALITY__DERP_REGION_ROLLUP` | `true` | emit tailnet-wide per-DERP-region rollup gauges (tailscale.derp.region.*) |
| `TS2OTEL_CARDINALITY__SUBNET_ROUTE_ROLLUP` | `true` | emit per-CIDR tailscale.subnet_routes.routers redundancy gauge (one series per subnet CIDR); fleet exit/subnet counts emit regardless |
| `TS2OTEL_CARDINALITY__WARNING_THRESHOLD` | `2000` | status-page cardinality view flags a metric at/above this active-series count (self-obs only; 0 disables) |
| `TS2OTEL_CARDINALITY__CRITICAL_THRESHOLD` | `8000` | status-page cardinality view flags a metric critically at/above this count (must be >= warning_threshold; <= metric_limit when set; 0 disables) |
| `TS2OTEL_CARDINALITY__LABEL_VALUE_SAMPLE_CAP` | `100` | distinct values retained per (metric,label) for the label-cardinality views; beyond it the label is capped and examples truncated (0 disables label capture) |
| `TS2OTEL_CARDINALITY__FLOW__METRICS_MODE` | `rollup` | rollup (bounded top-N, lowest cardinality) \| all (raw per-connection) \| both (≈2x series; summing double-counts) |
| `TS2OTEL_CARDINALITY__FLOW__ROLLUP_TOP_N` | `500` | rollup mode: busiest src/dst node pairs kept per flush; the rest fold into an __other__ series (0 = default 500) |
| `TS2OTEL_CARDINALITY__FLOW__SOURCE_PORT` | `false` | add source.port to flow metrics. INERT under metrics_mode: rollup (raw modes only) — and the most expensive knob here, ephemeral ports are unbounded |
| `TS2OTEL_CARDINALITY__FLOW__DESTINATION_PORT` | `false` | add destination.port to flow metrics. INERT under metrics_mode: rollup (raw modes only); dst.service below is the bounded stand-in and is always on |
| `TS2OTEL_CARDINALITY__FLOW__NODE_DIMS` | `true` | include src/dst device names on flow metrics (who talked to whom); off keeps totals but drops the per-peer breakdown and suppresses the unique.* gauges |
| `TS2OTEL_CARDINALITY__FLOW__IDENTITY_DIMS` | `false` | add per-flow tailscale.{src,dst}.{user,tags,os} to flow metrics, on BOTH families. REQUIRES node_dims (identity is node-derived, so without it identity becomes the only splitting dimension); flow LOGS always carry them |
| `TS2OTEL_CARDINALITY__FLOW__COLLAPSE_EXTERNAL` | `true` | bucket unresolved IPs as external/unknown (keeps cardinality bounded) |
| `TS2OTEL_CARDINALITY__FLOW__EXIT_NODE_ATTRIBUTION` | `true` | emit bounded tailscale.exit_node.io/packets attributing exit traffic to the relaying node (bounded by exit-node count) |
| `TS2OTEL_CARDINALITY__PER_ENTITY__DEVICE` | `true` | per-device gauges (online/last_seen/key_expiry/derp/routes) |
| `TS2OTEL_CARDINALITY__PER_ENTITY__USER` | `true` | per-user gauges (devices/connected/last_seen) |
| `TS2OTEL_CARDINALITY__PER_ENTITY__KEY` | `true` | per-key expiry gauge (the expiry WARN log fires regardless) |
| `TS2OTEL_CARDINALITY__PER_ENTITY__WEBHOOK` | `true` | per-endpoint webhook-subscriptions gauge |
| `TS2OTEL_CARDINALITY__PER_ENTITY__SERVICE` | `true` | per-service ports/hosts gauges |
| `TS2OTEL_COLLECTORS__DEVICES__ENABLED` | `true` | device inventory — REQUIRED for flow/audit IP->name enrichment (disabling it degrades names to unknown/external) |
| `TS2OTEL_COLLECTORS__DEVICES__INTERVAL` | `60s` | how often the device snapshot is polled |
| `TS2OTEL_COLLECTORS__DEVICES__COLLECT_ROUTES` | `false` | also fetch advertised/primary subnet routes per device |
| `TS2OTEL_COLLECTORS__DEVICES__COLLECT_CONNECTIVITY` | `true` | emit per-device NAT/connectivity health (hard_nat/endpoints/direct_capable/udp/ipv6) + fleet rollups from the device payload (no extra API calls) |
| `TS2OTEL_COLLECTORS__DEVICES__COLLECT_POSTURE` | `false` | also fetch device posture (MDM/EDR) — enables the posture metrics + log |
| `TS2OTEL_COLLECTORS__DEVICES__COLLECT_DEVICE_INVITES` | `true` | also fetch outstanding device share invites per device (one extra API call per device, N+1); emits tailscale.device_invites.count |
| `TS2OTEL_COLLECTORS__DEVICES__POSTURE_LOG_MODE` | `changes` | needs collect_posture: changes (log only on change) \| always (every scrape) \| off (no log); the posture METRIC is always emitted |
| `TS2OTEL_COLLECTORS__DEVICES__ATTRIBUTE_NAMESPACES` | `[intune, jamf, kandji, crowdstrike, sentinelone, kolide, ip]` | needs collect_posture: posture-key namespaces promoted to attribute metrics; ["*"] = all, [] = disable _(comma-separated list)_ |
| `TS2OTEL_COLLECTORS__DEVICES__COLLECT_TAG_ROLLUP` | `true` | emit tailscale.devices.by_tag (one series per ACL tag); false keeps the other fleet-hygiene aggregates |
| `TS2OTEL_COLLECTORS__DEVICES__TAG_ROLLUP_LIMIT` | `50` | cap distinct tag series on by_tag; busiest N kept, rest fold into tag="__other__" (0/negative = unlimited) |
| `TS2OTEL_COLLECTORS__FLOWLOGS__ENABLED` | `true` | network flow logs -> traffic counters + per-connection logs |
| `TS2OTEL_COLLECTORS__FLOWLOGS__SOURCE` | `poll` | poll (this exporter PULLS) \| stream (Tailscale PUSHES to the streaming receiver) \| objectstore (read Tailscale's S3 export) \| both (discouraged: double-counts) |
| `TS2OTEL_COLLECTORS__FLOWLOGS__INTERVAL` | `60s` | poll only — how often a window is polled |
| `TS2OTEL_COLLECTORS__FLOWLOGS__LAG` | `120s` | poll only — query only up to now-lag so late-arriving records aren't missed |
| `TS2OTEL_COLLECTORS__FLOWLOGS__INITIAL_LOOKBACK` | `5m` | poll only — cold-start reach-back when there is no checkpoint yet |
| `TS2OTEL_COLLECTORS__FLOWLOGS__MAX_WINDOW` | `1h` | poll only — cap one tick's window so a long outage catches up over several ticks |
| `TS2OTEL_COLLECTORS__FLOWLOGS__REPLAY_OVERLAP` | `5m` | poll only — reread this completed-window overlap for records that became available late (0 disables) |
| `TS2OTEL_COLLECTORS__FLOWLOGS__REPLAY_SEEN_CAPACITY` | `131072` | poll only — durable hashed connection identities retained for overlap dedup (1..1048576 when enabled) |
| `TS2OTEL_COLLECTORS__FLOWLOGS__TRUSTED_REPORTER_NODE_IDS` | `[]` | verified FlowLog.NodeID values classified as configured; empty with trusted_reporter_tags means trust policy is unconfigured _(comma-separated list)_ |
| `TS2OTEL_COLLECTORS__FLOWLOGS__TRUSTED_REPORTER_TAGS` | `[]` | authoritative device tags (e.g. ["tag:router"]) classified as tagged; embedded flow tags never grant trust _(comma-separated list)_ |
| `TS2OTEL_COLLECTORS__FLOWLOGS__LOG_MODE` | `per_connection` | per_connection \| per_record \| off — log detail level (applies to poll AND stream) |
| `TS2OTEL_COLLECTORS__FLOWLOGS__MAX_LOG_RECORDS_PER_WINDOW` | `0` | cap flow LOG records per window (0 = unlimited); excess -> tailscale.network.flow.logs_dropped (metrics are never capped) |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__ENDPOINT` | `""` | required — service URL, e.g. https://s3.eu-west-2.amazonaws.com, or a MinIO/Ceph address (never derived from the region) |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__REGION` | `""` | required — part of the request signature; a wrong value fails every request with HTTP 403 |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__BUCKET` | `""` | required — the bucket Tailscale exports into |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__PREFIX` | `""` | the export's root within the bucket, above the YYYY/MM/DD partitions; NO leading slash (it becomes part of this feed's durable checkpoint identity, so removing it later re-emits history) |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__LAYOUT` | `partitioned` | partitioned (Tailscale's own export: objects under prefix/YYYY/MM/DD/) \| flat (a COPIED export whose self-contained YYYY-MM-DD-HH-MM-SS basenames sit directly under prefix; finds partitioned objects too, but costs more LIST requests since nothing bounds the re-walk) |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__PATH_STYLE` | `false` | address as <endpoint>/<bucket>/<key>; required by most non-AWS implementations |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__ALLOW_INSECURE_HTTP` | `false` | remote plaintext endpoints are rejected by default; loopback HTTP remains available for local MinIO development |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__ACCESS_KEY_ID` | `""` | SET VIA ENV ONLY. Leave empty to use the ambient chain: environment, then IRSA/web identity, then the ECS/EKS container credential endpoint, then EC2 instance profile |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__ACCESS_KEY_ID_FILE` | `""` | read the access key ID from this path instead; value XOR file |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__SECRET_ACCESS_KEY` | `""` | SET VIA ENV ONLY |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__SECRET_ACCESS_KEY_FILE` | `""` | read the secret access key from this path instead; value XOR file |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__SESSION_TOKEN` | `""` | SET VIA ENV ONLY — temporary credentials only |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__SESSION_TOKEN_FILE` | `""` | read the temporary session token from this path instead; value XOR file |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__INTERVAL` | `60s` | how often the bucket is listed |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__LOOKBACK` | `1h` | how far back past the cursor each listing reaches, so a late-arriving object is still found |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__INITIAL_LOOKBACK` | `6h` | cold-start reach-back, so a first run against a long history doesn't ingest all of it; CAPPED IN EFFECT AT 14 DAYS under layout: partitioned (a larger value silently ingests only the newest 14 day partitions — use layout: flat to reach further back) |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__MAX_OBJECTS` | `200` | objects ingested per cycle; the remainder is counted, logged and picked up next cycle |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__MAX_OBJECT_WIRE_BYTES` | `67108864` | reject and quarantine one object requiring more than 64 MiB of GET response bytes |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__MAX_OBJECT_DECOMPRESSED_BYTES` | `33554432` | reject and quarantine one object that expands beyond 32 MiB |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__MAX_OBJECT_RECORDS` | `100000` | reject and quarantine one object containing more than this many records |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__MAX_CYCLE_WIRE_BYTES` | `536870912` | defer untouched objects after 512 MiB of GET response data in one cycle |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__MAX_CYCLE_DECOMPRESSED_BYTES` | `268435456` | defer untouched objects after 256 MiB of decoded data in one cycle |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__MAX_CYCLE_RECORDS` | `500000` | defer untouched objects after this many decoded records in one cycle |
| `TS2OTEL_COLLECTORS__AUDITLOGS__ENABLED` | `true` | configuration/audit events -> event logs + a counter |
| `TS2OTEL_COLLECTORS__AUDITLOGS__SOURCE` | `poll` | poll \| stream \| both \| objectstore (see flowlogs) |
| `TS2OTEL_COLLECTORS__AUDITLOGS__INTERVAL` | `60s` | poll only |
| `TS2OTEL_COLLECTORS__AUDITLOGS__LAG` | `60s` | poll only |
| `TS2OTEL_COLLECTORS__AUDITLOGS__INITIAL_LOOKBACK` | `5m` | poll only |
| `TS2OTEL_COLLECTORS__AUDITLOGS__MAX_WINDOW` | `6h` | poll only |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__ENDPOINT` | `""` | required — service URL, e.g. https://s3.eu-west-2.amazonaws.com, or a MinIO/Ceph address (never derived from the region) |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__REGION` | `""` | required — part of the request signature; a wrong value fails every request with HTTP 403 |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__BUCKET` | `""` | required — the bucket Tailscale exports configuration logs into |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__PREFIX` | `""` | the export's root within the bucket, above the YYYY/MM/DD partitions; use a distinct prefix when flow and configuration logs share one bucket. NO leading slash (see flowlogs) |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__LAYOUT` | `partitioned` | partitioned (Tailscale's own export: objects under prefix/YYYY/MM/DD/) \| flat (a COPIED export whose self-contained YYYY-MM-DD-HH-MM-SS basenames sit directly under prefix; finds partitioned objects too, but costs more LIST requests since nothing bounds the re-walk) |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__PATH_STYLE` | `false` | address as <endpoint>/<bucket>/<key>; required by most non-AWS implementations |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__ALLOW_INSECURE_HTTP` | `false` | remote plaintext endpoints are rejected by default; loopback HTTP remains available for local MinIO development |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__ACCESS_KEY_ID` | `""` | SET VIA ENV ONLY. Leave empty to use the ambient chain: environment, then IRSA/web identity, then the ECS/EKS container credential endpoint, then EC2 instance profile |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__ACCESS_KEY_ID_FILE` | `""` | read the access key ID from this path instead; value XOR file |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__SECRET_ACCESS_KEY` | `""` | SET VIA ENV ONLY |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__SECRET_ACCESS_KEY_FILE` | `""` | read the secret access key from this path instead; value XOR file |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__SESSION_TOKEN` | `""` | SET VIA ENV ONLY — temporary credentials only |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__SESSION_TOKEN_FILE` | `""` | read the temporary session token from this path instead; value XOR file |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__INTERVAL` | `60s` | how often the bucket is listed |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__LOOKBACK` | `1h` | how far back past the cursor each listing reaches, so a late-arriving object is still found |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__INITIAL_LOOKBACK` | `6h` | cold-start reach-back, so a first run against a long history doesn't ingest all of it; CAPPED IN EFFECT AT 14 DAYS under layout: partitioned (a larger value silently ingests only the newest 14 day partitions — use layout: flat to reach further back) |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__MAX_OBJECTS` | `200` | objects ingested per cycle; the remainder is counted, logged and picked up next cycle |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__MAX_OBJECT_WIRE_BYTES` | `67108864` | reject and quarantine one object requiring more than 64 MiB of GET response bytes |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__MAX_OBJECT_DECOMPRESSED_BYTES` | `33554432` | reject and quarantine one object that expands beyond 32 MiB |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__MAX_OBJECT_RECORDS` | `100000` | reject and quarantine one object containing more than this many records |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__MAX_CYCLE_WIRE_BYTES` | `536870912` | defer untouched objects after 512 MiB of GET response data in one cycle |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__MAX_CYCLE_DECOMPRESSED_BYTES` | `268435456` | defer untouched objects after 256 MiB of decoded data in one cycle |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__MAX_CYCLE_RECORDS` | `500000` | defer untouched objects after this many decoded records in one cycle |
| `TS2OTEL_COLLECTORS__USERS__ENABLED` | `true` | user inventory (devices/connected/last_seen per user) |
| `TS2OTEL_COLLECTORS__USERS__INTERVAL` | `300s` | user inventory (devices/connected/last_seen per user) |
| `TS2OTEL_COLLECTORS__KEYS__ENABLED` | `true` | auth-key inventory + expiry warnings |
| `TS2OTEL_COLLECTORS__KEYS__INTERVAL` | `300s` | auth-key inventory + expiry warnings |
| `TS2OTEL_COLLECTORS__KEYS__EXPIRY_WARN` | `168h` | log a WARN when a key expires within this window (default 7d) |
| `TS2OTEL_COLLECTORS__SETTINGS__ENABLED` | `true` | tailnet settings snapshot |
| `TS2OTEL_COLLECTORS__SETTINGS__INTERVAL` | `600s` | tailnet settings snapshot |
| `TS2OTEL_COLLECTORS__ACL__ENABLED` | `true` | ACL policy snapshot |
| `TS2OTEL_COLLECTORS__ACL__INTERVAL` | `600s` | ACL policy snapshot |
| `TS2OTEL_COLLECTORS__ACL__VALIDATE` | `true` | run the non-mutating POST /acl/validate (policy_file:read scope) each tick; set false to keep the client strictly GET-only |
| `TS2OTEL_COLLECTORS__DNS__ENABLED` | `true` | DNS/MagicDNS settings snapshot |
| `TS2OTEL_COLLECTORS__DNS__INTERVAL` | `600s` | DNS/MagicDNS settings snapshot |
| `TS2OTEL_COLLECTORS__CONTACTS__ENABLED` | `true` | account/support/security contact verification status (no emails emitted) |
| `TS2OTEL_COLLECTORS__CONTACTS__INTERVAL` | `600s` | account/support/security contact verification status (no emails emitted) |
| `TS2OTEL_COLLECTORS__WEBHOOKS__ENABLED` | `true` | webhook-endpoint inventory: count + per-endpoint subscription count (no url/secret) |
| `TS2OTEL_COLLECTORS__WEBHOOKS__INTERVAL` | `600s` | webhook-endpoint inventory: count + per-endpoint subscription count (no url/secret) |
| `TS2OTEL_COLLECTORS__WEBHOOKS__DESIRED_EVENTS` | `[]` | optional expected event categories (e.g. ["nodeCreated","userSuspended"]); empty means no expectation _(comma-separated list)_ |
| `TS2OTEL_COLLECTORS__POSTURE_INTEGRATIONS__ENABLED` | `true` | MDM/EDR posture-integration sync health: matched counts + last_sync staleness |
| `TS2OTEL_COLLECTORS__POSTURE_INTEGRATIONS__INTERVAL` | `600s` | MDM/EDR posture-integration sync health: matched counts + last_sync staleness |
| `TS2OTEL_COLLECTORS__LOG_STREAM__ENABLED` | `true` | log-streaming delivery health to a SIEM sink (self-gates to configured=0 when no sink) |
| `TS2OTEL_COLLECTORS__LOG_STREAM__INTERVAL` | `600s` | log-streaming delivery health to a SIEM sink (self-gates to configured=0 when no sink) |
| `TS2OTEL_COLLECTORS__OAUTH_APPS__ENABLED` | `true` | OAuth-application inventory (alpha API; idles silently — no error — on tailnets without it) |
| `TS2OTEL_COLLECTORS__OAUTH_APPS__INTERVAL` | `300s` | OAuth-application inventory (alpha API; idles silently — no error — on tailnets without it) |
| `TS2OTEL_COLLECTORS__SERVICES__ENABLED` | `true` | Tailscale Services (VIP) inventory |
| `TS2OTEL_COLLECTORS__SERVICES__INTERVAL` | `600s` | Tailscale Services (VIP) inventory |
| `TS2OTEL_COLLECTORS__SERVICES__COLLECT_HOSTS` | `false` | also fetch per-service backing-host detail — one extra API call per service (N+1) |
| `TS2OTEL_COLLECTORS__NODE_METRICS__ENABLED` | `false` | OPTIONAL: scrape tailscaled per-node Prometheus /metrics and forward them centrally. Off by default; see docs/node-metrics.md |
| `TS2OTEL_COLLECTORS__NODE_METRICS__INTERVAL` | `60s` | how often each target is scraped |
| `TS2OTEL_COLLECTORS__NODE_METRICS__TIMEOUT` | `10s` | per-scrape HTTP timeout |
| `TS2OTEL_COLLECTORS__NODE_METRICS__MAX_RESPONSE_BYTES` | `4194304` | per-target response cap (4 MiB) — bounds memory |
| `TS2OTEL_COLLECTORS__NODE_METRICS__MAX_SAMPLES` | `50000` | per-target sample cap per scrape — bounds cardinality |
| `TS2OTEL_COLLECTORS__NODE_METRICS__MAX_DISTINCT_METRICS` | `2000` | cap on DISTINCT forwarded metric NAMES over the process lifetime (targets choose their own names and each new one creates a permanent instrument); 0 = 2000 default, negative = unlimited (over-budget names are dropped and counted) |
| `TS2OTEL_COLLECTORS__NODE_METRICS__METRIC_ALLOW` | `[]` | if non-empty, only forwarded metric names matching one of these anchored regexes are kept _(comma-separated list)_ |
| `TS2OTEL_COLLECTORS__NODE_METRICS__METRIC_DENY` | `[]` | forwarded metric names matching any of these anchored regexes are dropped (after allow) _(comma-separated list)_ |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DROP_LABELS` | `[]` | label keys stripped from every forwarded series (the tailscale.node identity label is never dropped) _(comma-separated list)_ |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__ENABLED` | `false` | OPTIONAL: discover scrape targets from the Tailscale devices API (unioned with static targets) |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__INTERVAL` | `5m` | how often the device inventory is re-scanned for targets |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__MAX_TARGETS` | `1000` | cap discovered targets per refresh |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__SCHEME` | `http` | http \| https — metrics endpoint scheme on each device |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__PORT` | `5252` | tailscaled client-metrics port |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__PATH` | `/metrics` | metrics endpoint path |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__ONLINE_ONLY` | `true` | only devices currently connected to the control plane |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__EXCLUDE_EXTERNAL` | `true` | skip shared/external devices |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__INCLUDE_TAGS` | `[]` | only devices with one of these tags (empty = all), e.g. ["tag:server"] _(comma-separated list)_ |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__EXCLUDE_TAGS` | `[]` | devices with any of these tags are skipped (wins over include_tags) _(comma-separated list)_ |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__ADDRESS_ORDER` | `ipv4` | ipv4 \| ipv6 — preferred address family (falls back to the other) |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__INSTANCE_SOURCE` | `name` | identity label per target: name (MagicDNS short name, unique+friendly — default) \| address (Tailscale host:port, always unique) \| hostname (OS hostname, NOT unique — collisions like "localhost" are auto-suffixed) |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__INCLUDE_HOST_LABELS` | `true` | attach host.name/host.id for joins with tailscale.device.* |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__INCLUDE_TAGS_LABEL` | `true` | attach tailscale.tags to each target's series |
| `TS2OTEL_CHECKPOINT__STORE` | `file` | file (persists window cursors across restarts; falls back to memory + WARN if the path isn't writable) \| memory (RAM only; cold-starts from initial_lookback after a restart) |
| `TS2OTEL_CHECKPOINT__FILE_PATH` | `/var/lib/tailscale2otel/checkpoints.json` | used when store: file — mount a writable, persistent path here (the dir is auto-created). This default suits a CONTAINER (the image pre-seeds it for uid 65532); a native run that cannot write it falls back to the platform state dir (~/.local/state, ~/Library/Application Support, %LocalAppData%) and logs where it went. Set this explicitly and it is used as-is — an explicit path is never relocated. |
| `TS2OTEL_INGRESS_WAL__ENABLED` | `false` | opt in to durable local acceptance and oldest-first replay for receiver payloads |
| `TS2OTEL_INGRESS_WAL__DIRECTORY` | `/var/lib/tailscale2otel/ingress-wal` | absolute, filepath-clean, non-root directory; mount durable state here when reschedule survival matters |
| `TS2OTEL_INGRESS_WAL__MAX_BYTES` | `268435456` | encoded WAL byte ceiling (256 MiB); full WAL fails new requests closed; no TTL/eviction |
| `TS2OTEL_INGRESS_WAL__MAX_ENTRIES` | `10000` | encoded entry ceiling; full WAL fails new requests closed; no TTL/eviction |
| `TS2OTEL_INGRESS_WAL__CORRUPTION` | `fail` | only supported mode: fail closed rather than discard corrupt state |
| `TS2OTEL_STREAMING__ENABLED` | `false` | run the Splunk-HEC receiver to INGEST pushed logs (set the relevant collectors' source: stream) |
| `TS2OTEL_STREAMING__LISTEN` | `:8088` | bind address for the Splunk-HEC-compatible receiver |
| `TS2OTEL_STREAMING__PATH` | `/services/collector/event` | HEC endpoint path Tailscale POSTs to |
| `TS2OTEL_STREAMING__TOKEN` | `""` | shared secret; Tailscale sends HTTP Basic auth (base64 user:token), "Authorization: Splunk <token>" also accepted as a fallback (set via TS2OTEL_STREAMING__TOKEN); empty on a NON-loopback listen = every request REFUSED with 403 (empty is allowed only on a loopback bind) |
| `TS2OTEL_STREAMING__TOKEN_FILE` | `""` | read the value from this file instead (Docker secrets); set the value or the file, not both; content is whitespace-trimmed |
| `TS2OTEL_STREAMING__PUBLIC_URL` | `""` | externally reachable receiver URL; REQUIRED only when auto_configure: true |
| `TS2OTEL_STREAMING__TLS__CERT_FILE` | `""` | HTTPS cert (Tailscale requires HTTPS; a `tailscale cert` works for private endpoints) |
| `TS2OTEL_STREAMING__TLS__KEY_FILE` | `""` | HTTPS key |
| `TS2OTEL_STREAMING__DECOMPRESS` | `auto` | auto \| gzip \| zstd \| none — request body decompression |
| `TS2OTEL_STREAMING__AUTO_CONFIGURE` | `false` | on startup, register THIS receiver as the tailnet's log-streaming sink for BOTH log types (network/flow AND configuration/audit), OVERWRITING any existing sink for either; needs enabled + public_url + the log_streaming OAuth scope |
| `TS2OTEL_STREAMING__MAX_BODY_BYTES` | `0` | cap on the DECOMPRESSED body; 0 = 64 MiB default, negative = unlimited (over-cap = 413); when ingress_wal.enabled this receiver must set a positive value <= 64 MiB |
| `TS2OTEL_STREAMING__MAX_CONCURRENT_REQUESTS` | `0` | how many requests may buffer a body AT ONCE (max_body_bytes caps one body, this caps their sum); 0 = 4 default, negative = unlimited (over-limit = 503 + Retry-After) |
| `TS2OTEL_WEBHOOK__ENABLED` | `false` | run the receiver for real-time Tailscale webhook events |
| `TS2OTEL_WEBHOOK__LISTEN` | `:8089` | bind address for the webhook receiver |
| `TS2OTEL_WEBHOOK__PATH` | `/tailscale/webhook` | endpoint path Tailscale POSTs events to |
| `TS2OTEL_WEBHOOK__SECRET` | `""` | HMAC-SHA256 verification secret (set via TS2OTEL_WEBHOOK__SECRET); empty is accepted only on loopback, otherwise every request is refused with 403 before body read |
| `TS2OTEL_WEBHOOK__SECRET_FILE` | `""` | read the value from this file instead (Docker secrets); set the value or the file, not both; content is whitespace-trimmed |
| `TS2OTEL_WEBHOOK__TLS__CERT_FILE` | `""` | serve native HTTPS when paired with key_file; leave both empty behind an HTTPS reverse proxy |
| `TS2OTEL_WEBHOOK__TLS__KEY_FILE` | `""` | private key paired with cert_file; both paths are validated as readable at startup |
| `TS2OTEL_WEBHOOK__TOLERANCE` | `5m` | reject signed timestamps older than this (replay window); "0" disables the check |
| `TS2OTEL_WEBHOOK__DEDUP_AUDIT_EVENTS` | `false` | best-effort: drop a webhook event already counted via the audit logs |
| `TS2OTEL_WEBHOOK__MAX_BODY_BYTES` | `0` | cap on the raw body read before signature verification; 0 = 1 MiB default, negative = unlimited (over-cap = 413); when ingress_wal.enabled this receiver must set a positive value <= 64 MiB |
| `TS2OTEL_WEBHOOK__MAX_CONCURRENT_REQUESTS` | `0` | how many requests may buffer a body AT ONCE, BEFORE the HMAC is checked (max_body_bytes caps one body, this caps their sum); 0 = 4 default, negative = unlimited (over-limit = 503 + Retry-After) |
| `TS2OTEL_PII_FILTER__EMAILS` | `true` | user/actor login names (often email addresses) |
| `TS2OTEL_PII_FILTER__USER_DISPLAY_NAMES` | `true` | actor display (human) names |
| `TS2OTEL_PII_FILTER__USER_IDS` | `true` | numeric/opaque user IDs (user.id) |
| `TS2OTEL_PII_FILTER__HOSTNAMES` | `true` | device + collector-host hostnames |
| `TS2OTEL_PII_FILTER__NODE_IDS` | `true` | Tailscale node IDs |
| `TS2OTEL_PII_FILTER__TAILSCALE_IPS` | `true` | 100.64.0.0/10 + fd7a:115c:a1e0::/48 addresses |
| `TS2OTEL_PII_FILTER__INTERNAL_IPS` | `true` | RFC1918 / ULA / link-local addresses |
| `TS2OTEL_PII_FILTER__EXTERNAL_IPS` | `true` | public/routable addresses |
| `TS2OTEL_PII_FILTER__SERVICE_ADDRS` | `true` | VIP service names |
| `TS2OTEL_PII_FILTER__ENDPOINT_PATHS` | `true` | Tailscale API endpoint paths (self-obs) |
| `TS2OTEL_PII_FILTER__NETWORK_TOPOLOGY` | `true` | route CIDRs + split-DNS domains + search paths |
| `TS2OTEL_PII_FILTER__TAILNET_NAME` | `true` | tailnet identifier |
| `TS2OTEL_PII_FILTER__FREE_TEXT_DETAILS` | `true` | audit old/new/details, target names, key descriptions, posture values |
| `TS2OTEL_SELF_OBSERVABILITY__ENABLED` | `true` | emit tailscale2otel.up, api.requests, runtime metrics, etc. |
| `TS2OTEL_SELF_OBSERVABILITY__INSTANCE_ID` | `""` | service.instance.id resource attr; empty => host name. Set via env, e.g. TS2OTEL_SELF_OBSERVABILITY__INSTANCE_ID=$POD_NAME |
| `TS2OTEL_ADMIN__ENABLED` | `true` | run the admin HTTP server (probes + status page + optional pprof mount) |
| `TS2OTEL_ADMIN__LISTEN` | `127.0.0.1:9091` | serves /healthz, /readyz, and the status page. Loopback by default: the status page is REFUSED (403) on a network-reachable bind without admin.auth.token, so widen this only together with a token |
| `TS2OTEL_ADMIN__LANDING_PAGE` | `true` | serve the human status page at / and machine-readable /api/status.json |
| `TS2OTEL_ADMIN__STATUS_REFRESH_INTERVAL` | `5s` | how often the status page re-polls /api/status.json (1s freshness ticker is independent) |
| `TS2OTEL_ADMIN__AUTH__TOKEN` | `""` | gate the status page + pprof behind this token (set via TS2OTEL_ADMIN__AUTH__TOKEN); empty is allowed only on a loopback listen — on any other bind the status page + JSON APIs are REFUSED with 403 (/healthz and /readyz stay open) |
| `TS2OTEL_ADMIN__AUTH__TOKEN_FILE` | `""` | read the value from this file instead (Docker secrets); set the value or the file, not both; content is whitespace-trimmed |
| `TS2OTEL_ADMIN__TLS__CERT_FILE` | `""` | serve the admin listener over HTTPS instead of plain HTTP; set together with key_file (both-or-neither) |
| `TS2OTEL_ADMIN__TLS__KEY_FILE` | `""` | HTTPS key for admin.tls.cert_file |
| `TS2OTEL_FLOWS__ENABLED` | `true` | keep a bounded, in-memory picture of recent traffic and serve /flows; needs admin.enabled + admin.landing_page (no effect otherwise) |
| `TS2OTEL_FLOWS__RETENTION` | `6h` | how far back /flows can see, as one-minute buckets (1m–24h). Memory scales with this, and with the number of tailnets in multi-tailnet mode. Lost on restart — OTLP stays the system of record |
| `TS2OTEL_FLOWS__MAX_FUTURE_SKEW` | `5m` | local-view admission only: reject records further ahead of this process clock (0–1h); OTLP emission is unchanged |
| `TS2OTEL_PROMETHEUS__ENABLED` | `false` | run the Prometheus pull endpoint (GET /metrics) on its own listener, alongside OTLP push |
| `TS2OTEL_PROMETHEUS__LISTEN` | `:2112` | bind for /metrics (default :2112); keep distinct from admin.listen |
| `TS2OTEL_PROMETHEUS__AUTH__TOKEN` | `""` | gate /metrics behind this token (Bearer or Basic password); empty + a network bind = REFUSED 403 unless allow_unauthenticated. Set via TS2OTEL_PROMETHEUS__AUTH__TOKEN |
| `TS2OTEL_PROMETHEUS__AUTH__TOKEN_FILE` | `""` | read the value from this file instead (Docker secrets); set the value or the file, not both; content is whitespace-trimmed |
| `TS2OTEL_PROMETHEUS__AUTH__ALLOW_UNAUTHENTICATED` | `false` | acknowledge serving /metrics with NO token on a network-reachable bind (e.g. in-cluster scraping behind a NetworkPolicy); a loopback bind never needs this |
| `TS2OTEL_PROMETHEUS__TLS__CERT_FILE` | `""` | serve the Prometheus /metrics listener over HTTPS instead of plain HTTP; set together with key_file (both-or-neither) |
| `TS2OTEL_PROMETHEUS__TLS__KEY_FILE` | `""` | HTTPS key for prometheus.tls.cert_file |
| `TS2OTEL_PROFILING__PPROF__ENABLED` | `false` | mount net/http/pprof on the admin server (REQUIRES admin.enabled + admin.auth.token — heap dumps can expose in-memory secrets) |
| `TS2OTEL_PROFILING__PYROSCOPE__ENABLED` | `false` | run the Pyroscope continuous-profiling push agent |
| `TS2OTEL_PROFILING__PYROSCOPE__SERVER_ADDRESS` | `""` | REQUIRED when enabled, e.g. http://pyroscope:4040 or https://profiles-prod-NNN.grafana.net |
| `TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_USER` | `""` | Grafana Cloud: the profiles instance ID (set via TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_USER) |
| `TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_PASSWORD` | `""` | Grafana Cloud: a profiles:write access-policy token (..._BASIC_AUTH_PASSWORD) |
| `TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_PASSWORD_FILE` | `""` | read the value from this file instead (Docker secrets); set the value or the file, not both; content is whitespace-trimmed |
| `TS2OTEL_PROFILING__PYROSCOPE__TENANT_ID` | `""` | X-Scope-OrgID for multi-tenant servers (leave empty for Grafana Cloud) |
| `TS2OTEL_PROFILING__PYROSCOPE__UPLOAD_RATE` | `60s` | how often profiles are flushed |
| `TS2OTEL_PROFILING__MUTEX_PROFILE_FRACTION` | `5` | runtime.SetMutexProfileFraction; on by default (applied only when pprof or pyroscope is enabled), 0 = disabled |
| `TS2OTEL_PROFILING__BLOCK_PROFILE_RATE` | `100000` | runtime.SetBlockProfileRate (ns); on by default (100µs), 0 = disabled |
| `TS2OTEL_TRACING__ENABLED` | `false` | emit spans. TS2OTEL_TRACING__ENABLED |
| `TS2OTEL_TRACING__SAMPLER` | `parentbased_always_on` | head sampler: always_on\|always_off\|traceidratio\|parentbased_always_on\|parentbased_traceidratio. TS2OTEL_TRACING__SAMPLER |
| `TS2OTEL_TRACING__SAMPLER_ARG` | `1.0` | sample ratio in [0,1] for the *traceidratio samplers (ignored otherwise). TS2OTEL_TRACING__SAMPLER_ARG |
| `TS2OTEL_VERSION_CHECKS__SELF__ENABLED` | `true` | emit tailscale2otel.update_available (running build vs latest tailscale2otel GitHub release) |
| `TS2OTEL_VERSION_CHECKS__DEVICES__ENABLED` | `true` | emit per-device tailscale.device.version_skew + fleet rollups (device client version vs latest Tailscale stable). Needs the devices collector. |
| `TS2OTEL_VERSION_CHECKS__DEVICES__OUTDATED_MINOR_THRESHOLD` | `3` | a device this many minor releases behind counts toward tailscale.devices.outdated |
| `TS2OTEL_VERSION_CHECKS__CACHE_TTL` | `1h` | how long a fetched "latest version" is cached before re-fetching (minimum 5m) |
| `TS2OTEL_VERSION_CHECKS__TIMEOUT` | `10s` | per-request timeout for the external version fetch |

**File-only** — these take structured values (a map or a list of objects) and must be set in the YAML config, not via an environment variable: `tailnets`, `otlp.headers`, `collectors.node_metrics.targets`, `streaming.routes`, `webhook.routes`, `profiling.pyroscope.tags`.

<!-- END GENERATED: env-vars -->
