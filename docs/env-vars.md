# Environment-variable reference

Every configuration field is settable from an environment variable, so a container
deployment needs no mounted config file at all (and the env layer overrides any
file that *is* present — keep secrets here, never in YAML). See
[`configuration.md`](configuration.md) for the layering model and the prose
reference, and [`../config.example.yaml`](../config.example.yaml) for the same
fields as a commented file.

**Naming.** Take the dotted config key, prefix it with `TS2OTEL_`, uppercase it,
and replace each `.` with `__` (a single `_` inside a name is preserved):

```
tailscale.auth.oauth.client_secret  ->  TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET
collectors.flowlogs.interval        ->  TS2OTEL_COLLECTORS__FLOWLOGS__INTERVAL
```

**Lists** are comma-separated (e.g. `TS2OTEL_TAILSCALE__AUTH__OAUTH__SCOPES=all:read,log_streaming`).
A `TS2OTEL_*` variable that matches no known key is logged as a startup `WARN`.

> This table is **generated** from [`../config.example.yaml`](../config.example.yaml).
> Do not edit between the markers; run `scripts/regen-generated.sh envref` (or
> `go test ./internal/config -run TestEnvReferenceDocInSync -update`) to refresh it.

<!-- BEGIN GENERATED: env-vars -->

| Environment variable | Default | Description |
| --- | --- | --- |
| `TS2OTEL_LOG_LEVEL` | `info` | exporter's own log verbosity: debug \| info \| warn \| error |
| `TS2OTEL_TAILSCALE__TAILNET` | `-` | "-" = the authenticated principal's default tailnet (works out of the box); or set your tailnet's name explicitly, e.g. "example.com" |
| `TS2OTEL_TAILSCALE__AUTH__METHOD` | `oauth` | oauth (recommended) \| apikey |
| `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID` | `""` | OAuth client ID (set via TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID) |
| `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET` | `""` | OAuth client secret — keep in env, not here (..._CLIENT_SECRET) |
| `TS2OTEL_TAILSCALE__AUTH__OAUTH__SCOPES` | `[all:read]` | least-privilege read scopes requested for the token _(comma-separated list)_ |
| `TS2OTEL_TAILSCALE__AUTH__APIKEY` | `""` | personal API key (set via TS2OTEL_TAILSCALE__AUTH__APIKEY); used only when method: apikey — expires <=90d and is tied to its creator |
| `TS2OTEL_TAILSCALE__HTTP__TIMEOUT` | `30s` | per-attempt timeout (each retry attempt; a retried request may take longer, and long Retry-After waits are honored) |
| `TS2OTEL_TAILSCALE__HTTP__RETRY__MAX_ATTEMPTS` | `4` | total attempts per request (1 = no retry); exponential backoff between tries |
| `TS2OTEL_TAILSCALE__HTTP__RETRY__BASE_DELAY` | `500ms` | initial backoff delay |
| `TS2OTEL_TAILSCALE__HTTP__RETRY__MAX_DELAY` | `10s` | backoff ceiling |
| `TS2OTEL_TAILSCALE__HTTP__RATE_LIMIT` | `0` | global requests/sec across ALL collectors (0 = unlimited) |
| `TS2OTEL_OTLP__PROTOCOL` | `http` | http \| grpc \| stdout (stdout = print signals to the console for local debug, no backend) |
| `TS2OTEL_OTLP__ENDPOINT` | `https://otlp-gateway-prod-us-central-0.grafana.net/otlp` | OTLP base URL (the exporter appends /v1/metrics and /v1/logs itself) |
| `TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID` | `""` | Grafana Cloud stack/instance ID (set via TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID) |
| `TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN` | `""` | Grafana Cloud OTLP token — keep in env (..._GRAFANA_CLOUD__TOKEN) |
| `TS2OTEL_OTLP__TLS__INSECURE` | `false` | skip TLS certificate verification (debugging only — do not use in production) |
| `TS2OTEL_OTLP__TLS__CA_FILE` | `""` | path to a custom CA bundle to trust |
| `TS2OTEL_OTLP__TLS__CERT_FILE` | `""` | client certificate path (for mutual TLS) |
| `TS2OTEL_OTLP__TLS__KEY_FILE` | `""` | client private key path (for mutual TLS) |
| `TS2OTEL_OTLP__METRIC_INTERVAL` | `60s` | how often metrics are pushed (60s aligns with a 1-data-point-per-minute scrape) |
| `TS2OTEL_ENRICHMENT__CACHE_TTL` | `5m` | staleness alarm threshold for the IP/nodeID -> name device cache |
| `TS2OTEL_ENRICHMENT__REVERSE_DNS__ENABLED` | `false` | off by default (can add ~one flow-metric series per external IP when on) |
| `TS2OTEL_ENRICHMENT__REVERSE_DNS__SERVER` | `""` | resolver "ip" or "ip:port" (default :53); empty = system/container resolver |
| `TS2OTEL_ENRICHMENT__REVERSE_DNS__TIMEOUT` | `2s` | per-lookup timeout |
| `TS2OTEL_ENRICHMENT__REVERSE_DNS__CACHE_TTL` | `24h` | how long a resolved name is cached (PTRs rarely change, so a long TTL keeps resolver load low) |
| `TS2OTEL_ENRICHMENT__REVERSE_DNS__NEGATIVE_TTL` | `5m` | how long a failed lookup is remembered (suppresses retries) |
| `TS2OTEL_ENRICHMENT__REVERSE_DNS__MAX_ENTRIES` | `50000` | PTR cache size bound (new external IPs beyond this are not resolved; ~150 bytes/entry) |
| `TS2OTEL_CARDINALITY__METRIC_LIMIT` | `10000` | hard per-metric series cap; beyond it the SDK collapses extras into otel_metric_overflow (0/negative = unlimited) |
| `TS2OTEL_CARDINALITY__DERP_REGION_ROLLUP` | `true` | emit tailnet-wide per-DERP-region rollup gauges (tailscale.derp.region.*) |
| `TS2OTEL_CARDINALITY__FLOW__METRICS_MODE` | `rollup` | rollup (bounded top-N, lowest cardinality) \| all (raw per-connection) \| both (≈2x series; summing double-counts) |
| `TS2OTEL_CARDINALITY__FLOW__ROLLUP_TOP_N` | `500` | rollup mode: busiest src/dst node pairs kept per flush; the rest fold into an __other__ series (0 = default 500) |
| `TS2OTEL_CARDINALITY__FLOW__SOURCE_PORT` | `false` | add source.port to flow metrics (raw modes only) |
| `TS2OTEL_CARDINALITY__FLOW__DESTINATION_PORT` | `false` | add destination.port to flow metrics (raw modes only) |
| `TS2OTEL_CARDINALITY__FLOW__DESTINATION_SERVICE` | `false` | add tailscale.dst.service (IANA name, e.g. tcp/443->https) to flow metrics |
| `TS2OTEL_CARDINALITY__FLOW__NODE_DIMS` | `true` | include src/dst device names on flow metrics |
| `TS2OTEL_CARDINALITY__FLOW__COLLAPSE_EXTERNAL` | `true` | bucket unresolved IPs as external/unknown (keeps cardinality bounded) |
| `TS2OTEL_CARDINALITY__PER_ENTITY__DEVICE` | `true` | per-device gauges (online/last_seen/key_expiry/derp/routes) |
| `TS2OTEL_CARDINALITY__PER_ENTITY__USER` | `true` | per-user gauges (devices/connected/last_seen) |
| `TS2OTEL_CARDINALITY__PER_ENTITY__KEY` | `true` | per-key expiry gauge (the expiry WARN log fires regardless) |
| `TS2OTEL_CARDINALITY__PER_ENTITY__WEBHOOK` | `true` | per-endpoint webhook-subscriptions gauge |
| `TS2OTEL_CARDINALITY__PER_ENTITY__SERVICE` | `true` | per-service ports/hosts gauges |
| `TS2OTEL_COLLECTORS__DEVICES__ENABLED` | `true` | device inventory — REQUIRED for flow/audit IP->name enrichment (disabling it degrades names to unknown/external) |
| `TS2OTEL_COLLECTORS__DEVICES__INTERVAL` | `60s` | how often the device snapshot is polled |
| `TS2OTEL_COLLECTORS__DEVICES__COLLECT_ROUTES` | `false` | also fetch advertised/primary subnet routes per device |
| `TS2OTEL_COLLECTORS__DEVICES__COLLECT_POSTURE` | `false` | also fetch device posture (MDM/EDR) — enables the posture metrics + log |
| `TS2OTEL_COLLECTORS__DEVICES__POSTURE_LOG_MODE` | `changes` | needs collect_posture: changes (log only on change) \| always (every scrape) \| off (no log); the posture METRIC is always emitted |
| `TS2OTEL_COLLECTORS__DEVICES__ATTRIBUTE_NAMESPACES` | `[intune, jamf, kandji, crowdstrike, sentinelone, kolide, ip]` | needs collect_posture: posture-key namespaces promoted to attribute metrics; ["*"] = all, [] = disable _(comma-separated list)_ |
| `TS2OTEL_COLLECTORS__FLOWLOGS__ENABLED` | `true` | network flow logs -> traffic counters + per-connection logs |
| `TS2OTEL_COLLECTORS__FLOWLOGS__SOURCE` | `poll` | poll (this exporter PULLS) \| stream (Tailscale PUSHES to the streaming receiver) \| both (discouraged: double-counts) |
| `TS2OTEL_COLLECTORS__FLOWLOGS__INTERVAL` | `60s` | poll only — how often a window is polled |
| `TS2OTEL_COLLECTORS__FLOWLOGS__LAG` | `120s` | poll only — query only up to now-lag so late-arriving records aren't missed |
| `TS2OTEL_COLLECTORS__FLOWLOGS__INITIAL_LOOKBACK` | `5m` | poll only — cold-start reach-back when there is no checkpoint yet |
| `TS2OTEL_COLLECTORS__FLOWLOGS__MAX_WINDOW` | `1h` | poll only — cap one tick's window so a long outage catches up over several ticks |
| `TS2OTEL_COLLECTORS__FLOWLOGS__LOG_MODE` | `per_connection` | per_connection \| per_record \| off — log detail level (applies to poll AND stream) |
| `TS2OTEL_COLLECTORS__FLOWLOGS__MAX_LOG_RECORDS_PER_WINDOW` | `0` | cap flow LOG records per window (0 = unlimited); excess -> tailscale.network.flow.logs_dropped (metrics are never capped) |
| `TS2OTEL_COLLECTORS__AUDITLOGS__ENABLED` | `true` | configuration/audit events -> event logs + a counter |
| `TS2OTEL_COLLECTORS__AUDITLOGS__SOURCE` | `poll` | poll \| stream \| both (see flowlogs) |
| `TS2OTEL_COLLECTORS__AUDITLOGS__INTERVAL` | `60s` | poll only |
| `TS2OTEL_COLLECTORS__AUDITLOGS__LAG` | `60s` | poll only |
| `TS2OTEL_COLLECTORS__AUDITLOGS__INITIAL_LOOKBACK` | `5m` | poll only |
| `TS2OTEL_COLLECTORS__AUDITLOGS__MAX_WINDOW` | `6h` | poll only |
| `TS2OTEL_COLLECTORS__USERS__ENABLED` | `true` | user inventory (devices/connected/last_seen per user) |
| `TS2OTEL_COLLECTORS__USERS__INTERVAL` | `300s` | user inventory (devices/connected/last_seen per user) |
| `TS2OTEL_COLLECTORS__KEYS__ENABLED` | `true` | auth-key inventory + expiry warnings |
| `TS2OTEL_COLLECTORS__KEYS__INTERVAL` | `300s` | auth-key inventory + expiry warnings |
| `TS2OTEL_COLLECTORS__KEYS__EXPIRY_WARN` | `168h` | log a WARN when a key expires within this window (default 7d) |
| `TS2OTEL_COLLECTORS__SETTINGS__ENABLED` | `true` | tailnet settings snapshot |
| `TS2OTEL_COLLECTORS__SETTINGS__INTERVAL` | `600s` | tailnet settings snapshot |
| `TS2OTEL_COLLECTORS__ACL__ENABLED` | `true` | ACL policy snapshot |
| `TS2OTEL_COLLECTORS__ACL__INTERVAL` | `600s` | ACL policy snapshot |
| `TS2OTEL_COLLECTORS__DNS__ENABLED` | `true` | DNS/MagicDNS settings snapshot |
| `TS2OTEL_COLLECTORS__DNS__INTERVAL` | `600s` | DNS/MagicDNS settings snapshot |
| `TS2OTEL_COLLECTORS__CONTACTS__ENABLED` | `true` | account/support/security contact verification status (no emails emitted) |
| `TS2OTEL_COLLECTORS__CONTACTS__INTERVAL` | `600s` | account/support/security contact verification status (no emails emitted) |
| `TS2OTEL_COLLECTORS__WEBHOOKS__ENABLED` | `true` | webhook-endpoint inventory: count + per-endpoint subscription count (no url/secret) |
| `TS2OTEL_COLLECTORS__WEBHOOKS__INTERVAL` | `600s` | webhook-endpoint inventory: count + per-endpoint subscription count (no url/secret) |
| `TS2OTEL_COLLECTORS__POSTURE_INTEGRATIONS__ENABLED` | `true` | MDM/EDR posture-integration sync health: matched counts + last_sync staleness |
| `TS2OTEL_COLLECTORS__POSTURE_INTEGRATIONS__INTERVAL` | `600s` | MDM/EDR posture-integration sync health: matched counts + last_sync staleness |
| `TS2OTEL_COLLECTORS__LOG_STREAM__ENABLED` | `true` | log-streaming delivery health to a SIEM sink (self-gates to configured=0 when no sink) |
| `TS2OTEL_COLLECTORS__LOG_STREAM__INTERVAL` | `600s` | log-streaming delivery health to a SIEM sink (self-gates to configured=0 when no sink) |
| `TS2OTEL_COLLECTORS__SERVICES__ENABLED` | `true` | Tailscale Services (VIP) inventory |
| `TS2OTEL_COLLECTORS__SERVICES__INTERVAL` | `600s` | Tailscale Services (VIP) inventory |
| `TS2OTEL_COLLECTORS__SERVICES__COLLECT_HOSTS` | `false` | also fetch per-service backing-host detail — one extra API call per service (N+1) |
| `TS2OTEL_COLLECTORS__NODE_METRICS__ENABLED` | `false` | OPTIONAL: scrape tailscaled per-node Prometheus /metrics and forward them centrally. Off by default; see docs/node-metrics.md |
| `TS2OTEL_COLLECTORS__NODE_METRICS__INTERVAL` | `60s` | how often each target is scraped |
| `TS2OTEL_COLLECTORS__NODE_METRICS__TIMEOUT` | `10s` | per-scrape HTTP timeout |
| `TS2OTEL_COLLECTORS__NODE_METRICS__MAX_RESPONSE_BYTES` | `4194304` | per-target response cap (4 MiB) — bounds memory |
| `TS2OTEL_COLLECTORS__NODE_METRICS__MAX_SAMPLES` | `50000` | per-target sample cap per scrape — bounds cardinality |
| `TS2OTEL_COLLECTORS__NODE_METRICS__METRIC_ALLOW` | `[]` | if non-empty, only forwarded metric names matching one of these anchored regexes are kept _(comma-separated list)_ |
| `TS2OTEL_COLLECTORS__NODE_METRICS__METRIC_DENY` | `[]` | forwarded metric names matching any of these anchored regexes are dropped (after allow) _(comma-separated list)_ |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DROP_LABELS` | `[]` | label keys stripped from every forwarded series (the instance label is never dropped) _(comma-separated list)_ |
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
| `TS2OTEL_CHECKPOINT__FILE_PATH` | `/var/lib/tailscale2otel/checkpoints.json` | used when store: file — mount a writable, persistent path here (the dir is auto-created) |
| `TS2OTEL_STREAMING__ENABLED` | `false` | run the Splunk-HEC receiver to INGEST pushed logs (set the relevant collectors' source: stream) |
| `TS2OTEL_STREAMING__LISTEN` | `:8088` | bind address for the Splunk-HEC-compatible receiver |
| `TS2OTEL_STREAMING__PATH` | `/services/collector/event` | HEC endpoint path Tailscale POSTs to |
| `TS2OTEL_STREAMING__TOKEN` | `""` | expected as "Authorization: Splunk <token>" (set via TS2OTEL_STREAMING__TOKEN); empty = unauthenticated (WARN) |
| `TS2OTEL_STREAMING__PUBLIC_URL` | `""` | externally reachable receiver URL; REQUIRED only when auto_configure: true |
| `TS2OTEL_STREAMING__TLS__CERT_FILE` | `""` | HTTPS cert (Tailscale requires HTTPS; a `tailscale cert` works for private endpoints) |
| `TS2OTEL_STREAMING__TLS__KEY_FILE` | `""` | HTTPS key |
| `TS2OTEL_STREAMING__DECOMPRESS` | `auto` | auto \| gzip \| zstd \| none — request body decompression |
| `TS2OTEL_STREAMING__AUTO_CONFIGURE` | `false` | on startup, register THIS receiver as the tailnet's log-streaming sink (needs enabled + public_url + the log_streaming OAuth scope) |
| `TS2OTEL_STREAMING__MAX_BODY_BYTES` | `0` | cap on the DECOMPRESSED body; 0 = 64 MiB default, negative = unlimited (over-cap = 413) |
| `TS2OTEL_WEBHOOK__ENABLED` | `false` | run the receiver for real-time Tailscale webhook events |
| `TS2OTEL_WEBHOOK__LISTEN` | `:8089` | bind address for the webhook receiver |
| `TS2OTEL_WEBHOOK__PATH` | `/tailscale/webhook` | endpoint path Tailscale POSTs events to |
| `TS2OTEL_WEBHOOK__SECRET` | `""` | HMAC-SHA256 verification secret (set via TS2OTEL_WEBHOOK__SECRET); empty = verification SKIPPED (WARN) |
| `TS2OTEL_WEBHOOK__TOLERANCE` | `5m` | reject signed timestamps older than this (replay window); "0" disables the check |
| `TS2OTEL_WEBHOOK__DEDUP_AUDIT_EVENTS` | `false` | best-effort: drop a webhook event already counted via the audit logs |
| `TS2OTEL_SELF_OBSERVABILITY__ENABLED` | `true` | emit tailscale2otel.up, api.requests, runtime metrics, etc. |
| `TS2OTEL_SELF_OBSERVABILITY__INSTANCE_ID` | `""` | service.instance.id resource attr; empty => host name. Set via env, e.g. TS2OTEL_SELF_OBSERVABILITY__INSTANCE_ID=$POD_NAME |
| `TS2OTEL_ADMIN__ENABLED` | `false` | run the admin HTTP server (probes + status page + optional pprof mount) |
| `TS2OTEL_ADMIN__LISTEN` | `:9090` | serves /healthz, /readyz, and the status page |
| `TS2OTEL_ADMIN__LANDING_PAGE` | `true` | serve the human status page at / and machine-readable /api/status.json |
| `TS2OTEL_ADMIN__AUTH__TOKEN` | `""` | gate the status page + pprof behind this token (set via TS2OTEL_ADMIN__AUTH__TOKEN); empty = open status page (WARN on a wildcard bind) |
| `TS2OTEL_PROFILING__PPROF__ENABLED` | `false` | mount net/http/pprof on the admin server (REQUIRES admin.enabled + admin.auth.token — heap dumps can expose in-memory secrets) |
| `TS2OTEL_PROFILING__PYROSCOPE__ENABLED` | `false` | run the Pyroscope continuous-profiling push agent |
| `TS2OTEL_PROFILING__PYROSCOPE__SERVER_ADDRESS` | `""` | REQUIRED when enabled, e.g. http://pyroscope:4040 or https://profiles-prod-NNN.grafana.net |
| `TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_USER` | `""` | Grafana Cloud: the profiles instance ID (set via TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_USER) |
| `TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_PASSWORD` | `""` | Grafana Cloud: a profiles:write access-policy token (..._BASIC_AUTH_PASSWORD) |
| `TS2OTEL_PROFILING__PYROSCOPE__TENANT_ID` | `""` | X-Scope-OrgID for multi-tenant servers (leave empty for Grafana Cloud) |
| `TS2OTEL_PROFILING__PYROSCOPE__UPLOAD_RATE` | `60s` | how often profiles are flushed |
| `TS2OTEL_PROFILING__MUTEX_PROFILE_FRACTION` | `0` | runtime.SetMutexProfileFraction (0 = disabled) |
| `TS2OTEL_PROFILING__BLOCK_PROFILE_RATE` | `0` | runtime.SetBlockProfileRate (0 = disabled) |

**File-only** — these take structured values (a map or a list of objects) and must be set in the YAML config, not via an environment variable: `otlp.headers`, `collectors.node_metrics.targets`, `profiling.pyroscope.tags`.

<!-- END GENERATED: env-vars -->
