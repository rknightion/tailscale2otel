---
title: Environment Variables
description: Every TS2OTEL_* environment variable, its default, and what it controls
---

# Environment-variable reference

Scalar fields and simple lists are settable from environment variables, so common container
deployments need no mounted config file. Maps and lists of structured entries remain file-only. The
env layer overrides any file that *is* present, so secrets can stay outside YAML. See
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
> Do not edit between the markers; run `just gen envref` (or
> `go test ./internal/config -run TestEnvReferenceDocInSync -update`) to refresh it.

<!-- BEGIN GENERATED: env-vars -->

| Environment variable | Default | Reload | Description |
| --- | --- | --- | --- |
| `TS2OTEL_LOG_LEVEL` | `info` | `restart` | exporter's own log verbosity: debug \| info \| warn \| error |
| `TS2OTEL_LOG_FORMAT` | `text` | `restart` | operational log encoding: text \| json (json = one record per line) |
| `TS2OTEL_PROVIDER` | `tailscale` | `restart` | control-plane backend: tailscale (default) \| headscale |
| `TS2OTEL_HEADSCALE__URL` | `""` | `restart` | Headscale origin only (scheme + host, optional port; no path, credentials, query, or fragment), e.g. https://headscale.example.org (TS2OTEL_HEADSCALE__URL) |
| `TS2OTEL_HEADSCALE__API_KEY` | `""` | `restart` | Bearer API key — keep in env (TS2OTEL_HEADSCALE__API_KEY) |
| `TS2OTEL_HEADSCALE__API_KEY_FILE` | `""` | `restart` | read the value from this file instead (Docker secrets); set the value or the file, not both; content is whitespace-trimmed |
| `TS2OTEL_HEADSCALE__IP_PREFIXES` | `[]` | `restart` | tailnet address CIDRs allocated by this Headscale; empty = Tailscale defaults. Must be canonical and fully inside RFC1918, fc00::/7, or 100.64.0.0/10 _(comma-separated list)_ |
| `TS2OTEL_HEADSCALE__HTTP__TIMEOUT` | `30s` | `restart` | per-attempt timeout; retry queueing/rate-limit wait is outside this budget |
| `TS2OTEL_HEADSCALE__HTTP__RETRY__MAX_ATTEMPTS` | `0` | `restart` | retry policy for retryable transport errors, HTTP 429 and HTTP 5xx |
| `TS2OTEL_HEADSCALE__HTTP__RETRY__BASE_DELAY` | `0s` | `restart` | retry policy for retryable transport errors, HTTP 429 and HTTP 5xx |
| `TS2OTEL_HEADSCALE__HTTP__RETRY__MAX_DELAY` | `0s` | `restart` | retry policy for retryable transport errors, HTTP 429 and HTTP 5xx |
| `TS2OTEL_HEADSCALE__HTTP__RATE_LIMIT` | `0` | `restart` | HTTP client used for all Headscale API calls |
| `TS2OTEL_HEADSCALE__MAX_RESPONSE_BYTES` | `4194304` | `restart` | cap (4 MiB) on ONE Headscale API response body before decoding; ~5800 nodes at ~715 B each — raise it (and the container memory limit) on a bigger deployment, these endpoints are not paginated |
| `TS2OTEL_TAILSCALE__TAILNET` | `-` | `restart` | "-" = the authenticated principal's default tailnet (works out of the box); or set your tailnet's name explicitly, e.g. "example.com" |
| `TS2OTEL_TAILSCALE__AUTH__METHOD` | `oauth` | `restart` | oauth (recommended) \| apikey \| workload_identity (keyless OIDC exchange) |
| `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID` | `""` | `restart` | OAuth client ID (set via TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID) |
| `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET` | `""` | `restart` | OAuth client secret — keep in env, not here (..._CLIENT_SECRET) |
| `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET_FILE` | `""` | `restart` | read the value from this file instead (Docker secrets); set the value or the file, not both; content is whitespace-trimmed |
| `TS2OTEL_TAILSCALE__AUTH__OAUTH__SCOPES` | `[all:read]` | `restart` | least-privilege read scopes requested for the token _(comma-separated list)_ |
| `TS2OTEL_TAILSCALE__AUTH__APIKEY` | `""` | `restart` | personal API key (set via TS2OTEL_TAILSCALE__AUTH__APIKEY); used only when method: apikey — expires <=90d and is tied to its creator |
| `TS2OTEL_TAILSCALE__AUTH__APIKEY_FILE` | `""` | `restart` | read the value from this file instead (Docker secrets); set the value or the file, not both; content is whitespace-trimmed |
| `TS2OTEL_TAILSCALE__AUTH__WORKLOAD_IDENTITY__CLIENT_ID` | `""` | `restart` | federated OAuth client ID from the Tailscale admin console (TS2OTEL_TAILSCALE__AUTH__WORKLOAD_IDENTITY__CLIENT_ID) |
| `TS2OTEL_TAILSCALE__AUTH__WORKLOAD_IDENTITY__ID_TOKEN_FILE` | `""` | `restart` | path to the OIDC ID token, e.g. a Kubernetes projected service-account token; re-read on every exchange (rotation-safe) |
| `TS2OTEL_TAILSCALE__HTTP__TIMEOUT` | `30s` | `restart` | per-attempt timeout (each retry attempt; a retried request may take longer, and long Retry-After waits are honored) |
| `TS2OTEL_TAILSCALE__HTTP__RETRY__MAX_ATTEMPTS` | `4` | `restart` | total attempts per request (1 = no retry); exponential backoff between tries |
| `TS2OTEL_TAILSCALE__HTTP__RETRY__BASE_DELAY` | `500ms` | `restart` | initial backoff delay |
| `TS2OTEL_TAILSCALE__HTTP__RETRY__MAX_DELAY` | `10s` | `restart` | backoff ceiling |
| `TS2OTEL_TAILSCALE__HTTP__RATE_LIMIT` | `0` | `restart` | global requests/sec across ALL collectors (0 = unlimited) |
| `TS2OTEL_TAILSCALE__MAX_RESPONSE_BYTES` | `4194304` | `restart` | cap (4 MiB) on ONE snapshot-endpoint response body before decoding; ~2400 devices at ~1.8 KiB each — raise it (and the container memory limit) on a bigger tailnet, these endpoints are not paginated |
| `TS2OTEL_TAILSCALE__MAX_LOG_RESPONSE_BYTES` | `33554432` | `restart` | cap (32 MiB) on ONE flow-log/audit-log response body; ~12000 flow records per poll — shrink the collector's window instead of raising this if you hit it |
| `TS2OTEL_TAILSCALE__ORGANIZATION` | `""` | `restart` | alpha roster inventory via first runtime credential (needs tailnets:read); runtimes still require explicit credentials |
| `TS2OTEL_PAM__TOKEN` | `""` | `restart` | static read-only Border0 service-account bearer token; keep in TS2OTEL_PAM__TOKEN |
| `TS2OTEL_PAM__API_URL` | `https://api.border0.com/api/v1` | `restart` | override only for a compatible proxy or local test endpoint |
| `TS2OTEL_OTLP__PROTOCOL` | `http` | `restart` | http \| grpc \| stdout (stdout = print signals to the console for local debug, no backend) |
| `TS2OTEL_OTLP__ENDPOINT` | `https://otlp-gateway-prod-us-central-0.grafana.net/otlp` | `restart` | OTLP base URL (the exporter appends /v1/metrics, /v1/logs and /v1/traces itself) |
| `TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID` | `""` | `restart` | Grafana Cloud stack/instance ID (set via TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID) |
| `TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN` | `""` | `restart` | Grafana Cloud OTLP token — keep in env (..._GRAFANA_CLOUD__TOKEN) |
| `TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN_FILE` | `""` | `file_content` | read the value from this file instead (Docker secrets); set the value or the file, not both; content is whitespace-trimmed |
| `TS2OTEL_OTLP__TLS__INSECURE` | `false` | `restart` | disable TLS entirely (plaintext h2c/http) — NOT a cert-verify skip; do not use in production |
| `TS2OTEL_OTLP__TLS__INSECURE_SKIP_VERIFY` | `false` | `restart` | keep TLS but skip server-cert verification (self-signed/private-CA gateways, testing only — prefer ca_file) |
| `TS2OTEL_OTLP__TLS__CA_FILE` | `""` | `file_content` | path to a custom CA bundle to trust |
| `TS2OTEL_OTLP__TLS__CERT_FILE` | `""` | `file_content` | client certificate path (for mutual TLS) |
| `TS2OTEL_OTLP__TLS__KEY_FILE` | `""` | `file_content` | client private key path (for mutual TLS) |
| `TS2OTEL_OTLP__METRIC_INTERVAL` | `60s` | `restart` | how often metrics are pushed (60s aligns with a 1-data-point-per-minute scrape) |
| `TS2OTEL_OTLP__METRIC_EXPORT_BATCH_SIZE` | `10000` | `restart` | maximum datapoints per OTLP metric request; lower this when a backend has a small request-size limit (serialized bytes vary with labels) |
| `TS2OTEL_OTLP__METRIC_TEMPORALITY` | `cumulative` | `restart` | cumulative (Grafana Cloud default) \| delta |
| `TS2OTEL_OTLP__OUTAGE_SUMMARY_INTERVAL` | `5m` | `restart` | repeat a concise diagnostic while an OTLP outage continues |
| `TS2OTEL_OTLP__LIMITS__LOG_BODY_BYTES` | `32768` | `restart` | cap a log record's body; UTF-8 safe, applied AFTER redaction, leaves a truncation marker. Minimum 64 — there is no unlimited setting |
| `TS2OTEL_OTLP__LIMITS__LOG_ATTRIBUTE_VALUE_BYTES` | `4096` | `restart` | cap each string-valued log ATTRIBUTE; never applied to metric labels (those must stay byte-exact or series split) |
| `TS2OTEL_OTLP__COMPRESSION` | `""` | `restart` | gzip \| none. Empty defers to OTEL_EXPORTER_OTLP[_<SIGNAL>]_COMPRESSION, then the exporter default. TS2OTEL_OTLP__COMPRESSION |
| `TS2OTEL_OTLP__TIMEOUT` | `0s` | `restart` | per-request export timeout; 0 defers to OTEL_EXPORTER_OTLP[_<SIGNAL>]_TIMEOUT, then the exporter's 10s. TS2OTEL_OTLP__TIMEOUT |
| `TS2OTEL_OTLP__MAX_REQUEST_SIZE` | `0` | `restart` | bytes; a client-side REJECTION guard, not a splitter — it fails an oversized request instead of shipping it into a 413. Use metric_export_batch_size to actually stay under an ingest limit |
| `TS2OTEL_OTLP__GRPC_RECONNECTION_PERIOD` | `0s` | `restart` | force a fresh gRPC connection attempt after this long; gRPC only, 0 = the gRPC client default |
| `TS2OTEL_OTLP__RETRY__ENABLED` | `true` | `restart` | set false to disable retry |
| `TS2OTEL_OTLP__RETRY__INITIAL_INTERVAL` | `5s` | `restart` | first backoff delay |
| `TS2OTEL_OTLP__RETRY__MAX_INTERVAL` | `30s` | `restart` | backoff ceiling |
| `TS2OTEL_OTLP__RETRY__MAX_ELAPSED_TIME` | `1m` | `restart` | give up after this long |
| `TS2OTEL_OTLP__BATCH__LOGS__MAX_QUEUE_SIZE` | `0` | `restart` | records buffered before new ones are dropped (non-blocking by design) |
| `TS2OTEL_OTLP__BATCH__LOGS__EXPORT_MAX_BATCH_SIZE` | `0` | `restart` | records per export; must be <= max_queue_size when both are set |
| `TS2OTEL_OTLP__BATCH__LOGS__EXPORT_INTERVAL` | `0s` | `restart` | how often a partial batch is flushed |
| `TS2OTEL_OTLP__BATCH__LOGS__EXPORT_TIMEOUT` | `0s` | `restart` | bound on one export attempt |
| `TS2OTEL_OTLP__BATCH__TRACES__MAX_QUEUE_SIZE` | `0` | `restart` | spans buffered before new ones are dropped |
| `TS2OTEL_OTLP__BATCH__TRACES__EXPORT_MAX_BATCH_SIZE` | `0` | `restart` | spans per export; must be <= max_queue_size when both are set |
| `TS2OTEL_OTLP__BATCH__TRACES__EXPORT_INTERVAL` | `0s` | `restart` | how often a partial batch is flushed |
| `TS2OTEL_OTLP__BATCH__TRACES__EXPORT_TIMEOUT` | `0s` | `restart` | bound on one export attempt |
| `TS2OTEL_OTLP__STDOUT__METRIC_INTERVAL` | `5s` | `restart` | short cadence so a debug run doesn't wait 60s for a metric; logs and spans are emitted synchronously |
| `TS2OTEL_OTLP__STDOUT__PRETTY` | `false` | `restart` | indent the emitted JSON |
| `TS2OTEL_OTLP__CREDENTIAL_RELOAD__ENABLED` | `false` | `restart` | governs the background poller only; last-known-good is always retained for a configured file |
| `TS2OTEL_OTLP__CREDENTIAL_RELOAD__INTERVAL` | `30s` | `restart` | poll period; minimum 5s. Ignored when enabled is false |
| `TS2OTEL_OTLP__METRICS__ENABLED` | `""` | `restart` | null inherits (the signal is on); false stops exporting this signal WITHOUT disturbing the others |
| `TS2OTEL_OTLP__METRICS__PROTOCOL` | `""` | `restart` | empty inherits otlp.protocol |
| `TS2OTEL_OTLP__METRICS__ENDPOINT` | `""` | `restart` | empty inherits otlp.endpoint |
| `TS2OTEL_OTLP__METRICS__TLS__INSECURE` | `""` | `restart` | null inherits; explicit true/false overrides |
| `TS2OTEL_OTLP__METRICS__TLS__INSECURE_SKIP_VERIFY` | `""` | `restart` | null inherits; explicit true/false overrides |
| `TS2OTEL_OTLP__METRICS__TLS__CA_FILE` | `""` | `restart` | every empty/null field inherits the matching otlp.tls value |
| `TS2OTEL_OTLP__METRICS__TLS__CERT_FILE` | `""` | `restart` | every empty/null field inherits the matching otlp.tls value |
| `TS2OTEL_OTLP__METRICS__TLS__KEY_FILE` | `""` | `restart` | every empty/null field inherits the matching otlp.tls value |
| `TS2OTEL_OTLP__METRICS__COMPRESSION` | `""` | `restart` | empty inherits otlp.compression |
| `TS2OTEL_OTLP__METRICS__TIMEOUT` | `0s` | `restart` | 0 inherits otlp.timeout |
| `TS2OTEL_OTLP__METRICS__MAX_REQUEST_SIZE` | `0` | `restart` | 0 inherits otlp.max_request_size |
| `TS2OTEL_OTLP__METRICS__GRPC_RECONNECTION_PERIOD` | `0s` | `restart` | 0 inherits otlp.grpc_reconnection_period |
| `TS2OTEL_OTLP__METRICS__RETRY__ENABLED` | `""` | `restart` | an untouched block inherits otlp.retry; setting ANY field overrides the whole policy for this signal |
| `TS2OTEL_OTLP__METRICS__RETRY__INITIAL_INTERVAL` | `0s` | `restart` | an untouched block inherits otlp.retry; setting ANY field overrides the whole policy for this signal |
| `TS2OTEL_OTLP__METRICS__RETRY__MAX_INTERVAL` | `0s` | `restart` | an untouched block inherits otlp.retry; setting ANY field overrides the whole policy for this signal |
| `TS2OTEL_OTLP__METRICS__RETRY__MAX_ELAPSED_TIME` | `0s` | `restart` | an untouched block inherits otlp.retry; setting ANY field overrides the whole policy for this signal |
| `TS2OTEL_OTLP__LOGS__ENABLED` | `""` | `restart` | null inherits (the signal is on); false stops exporting this signal WITHOUT disturbing the others |
| `TS2OTEL_OTLP__LOGS__PROTOCOL` | `""` | `restart` | empty inherits otlp.protocol |
| `TS2OTEL_OTLP__LOGS__ENDPOINT` | `""` | `restart` | empty inherits otlp.endpoint |
| `TS2OTEL_OTLP__LOGS__TLS__INSECURE` | `""` | `restart` | null inherits; explicit true/false overrides |
| `TS2OTEL_OTLP__LOGS__TLS__INSECURE_SKIP_VERIFY` | `""` | `restart` | null inherits; explicit true/false overrides |
| `TS2OTEL_OTLP__LOGS__TLS__CA_FILE` | `""` | `restart` | every empty/null field inherits the matching otlp.tls value |
| `TS2OTEL_OTLP__LOGS__TLS__CERT_FILE` | `""` | `restart` | every empty/null field inherits the matching otlp.tls value |
| `TS2OTEL_OTLP__LOGS__TLS__KEY_FILE` | `""` | `restart` | every empty/null field inherits the matching otlp.tls value |
| `TS2OTEL_OTLP__LOGS__COMPRESSION` | `""` | `restart` | empty inherits otlp.compression |
| `TS2OTEL_OTLP__LOGS__TIMEOUT` | `0s` | `restart` | 0 inherits otlp.timeout |
| `TS2OTEL_OTLP__LOGS__MAX_REQUEST_SIZE` | `0` | `restart` | 0 inherits otlp.max_request_size |
| `TS2OTEL_OTLP__LOGS__GRPC_RECONNECTION_PERIOD` | `0s` | `restart` | 0 inherits otlp.grpc_reconnection_period |
| `TS2OTEL_OTLP__LOGS__RETRY__ENABLED` | `""` | `restart` | an untouched block inherits otlp.retry; setting ANY field overrides the whole policy for this signal |
| `TS2OTEL_OTLP__LOGS__RETRY__INITIAL_INTERVAL` | `0s` | `restart` | an untouched block inherits otlp.retry; setting ANY field overrides the whole policy for this signal |
| `TS2OTEL_OTLP__LOGS__RETRY__MAX_INTERVAL` | `0s` | `restart` | an untouched block inherits otlp.retry; setting ANY field overrides the whole policy for this signal |
| `TS2OTEL_OTLP__LOGS__RETRY__MAX_ELAPSED_TIME` | `0s` | `restart` | an untouched block inherits otlp.retry; setting ANY field overrides the whole policy for this signal |
| `TS2OTEL_OTLP__TRACES__ENABLED` | `""` | `restart` | null inherits (the signal is on); false stops exporting this signal WITHOUT disturbing the others |
| `TS2OTEL_OTLP__TRACES__PROTOCOL` | `""` | `restart` | empty inherits otlp.protocol |
| `TS2OTEL_OTLP__TRACES__ENDPOINT` | `""` | `restart` | empty inherits otlp.endpoint |
| `TS2OTEL_OTLP__TRACES__TLS__INSECURE` | `""` | `restart` | null inherits; explicit true/false overrides |
| `TS2OTEL_OTLP__TRACES__TLS__INSECURE_SKIP_VERIFY` | `""` | `restart` | null inherits; explicit true/false overrides |
| `TS2OTEL_OTLP__TRACES__TLS__CA_FILE` | `""` | `restart` | every empty/null field inherits the matching otlp.tls value |
| `TS2OTEL_OTLP__TRACES__TLS__CERT_FILE` | `""` | `restart` | every empty/null field inherits the matching otlp.tls value |
| `TS2OTEL_OTLP__TRACES__TLS__KEY_FILE` | `""` | `restart` | every empty/null field inherits the matching otlp.tls value |
| `TS2OTEL_OTLP__TRACES__COMPRESSION` | `""` | `restart` | empty inherits otlp.compression |
| `TS2OTEL_OTLP__TRACES__TIMEOUT` | `0s` | `restart` | 0 inherits otlp.timeout |
| `TS2OTEL_OTLP__TRACES__MAX_REQUEST_SIZE` | `0` | `restart` | 0 inherits otlp.max_request_size |
| `TS2OTEL_OTLP__TRACES__GRPC_RECONNECTION_PERIOD` | `0s` | `restart` | 0 inherits otlp.grpc_reconnection_period |
| `TS2OTEL_OTLP__TRACES__RETRY__ENABLED` | `""` | `restart` | an untouched block inherits otlp.retry; setting ANY field overrides the whole policy for this signal |
| `TS2OTEL_OTLP__TRACES__RETRY__INITIAL_INTERVAL` | `0s` | `restart` | an untouched block inherits otlp.retry; setting ANY field overrides the whole policy for this signal |
| `TS2OTEL_OTLP__TRACES__RETRY__MAX_INTERVAL` | `0s` | `restart` | an untouched block inherits otlp.retry; setting ANY field overrides the whole policy for this signal |
| `TS2OTEL_OTLP__TRACES__RETRY__MAX_ELAPSED_TIME` | `0s` | `restart` | an untouched block inherits otlp.retry; setting ANY field overrides the whole policy for this signal |
| `TS2OTEL_DELIVERY__MODE` | `otlp` | `restart` | otlp keeps historical push-only delivery; prometheus serves /metrics and suppresses inherited OTLP metrics/logs/traces; dual enables both. An otlp.<signal>.endpoint explicitly opts that signal back in under prometheus mode |
| `TS2OTEL_ENRICHMENT__CACHE_TTL` | `5m` | `restart` | staleness alarm threshold for the IP/nodeID -> name device cache |
| `TS2OTEL_ENRICHMENT__DEVICE_CACHE_STALE_AFTER` | `0s` | `restart` | mark cached identity stale after this age; 0 preserves fresh-until-replaced behaviour |
| `TS2OTEL_ENRICHMENT__REVERSE_DNS__ENABLED` | `false` | `restart` | off by default (can add ~one flow-metric series per external IP when on) |
| `TS2OTEL_ENRICHMENT__REVERSE_DNS__SERVER` | `""` | `restart` | resolver "ip" or "ip:port" (default :53); empty = system/container resolver |
| `TS2OTEL_ENRICHMENT__REVERSE_DNS__TIMEOUT` | `2s` | `restart` | per-lookup timeout |
| `TS2OTEL_ENRICHMENT__REVERSE_DNS__CACHE_TTL` | `24h` | `restart` | how long a resolved name is cached (PTRs rarely change, so a long TTL keeps resolver load low) |
| `TS2OTEL_ENRICHMENT__REVERSE_DNS__NEGATIVE_TTL` | `5m` | `restart` | how long a failed lookup is remembered (suppresses retries) |
| `TS2OTEL_ENRICHMENT__REVERSE_DNS__STALE_TTL` | `1h` | `restart` | keep serving a resolved name this long past cache_ttl while one refresh runs (0 disables; stops the flow label flapping to external at every expiry) |
| `TS2OTEL_ENRICHMENT__REVERSE_DNS__MAX_ENTRIES` | `50000` | `restart` | PTR cache size bound (new external IPs beyond this are not resolved; ~150 bytes/entry) |
| `TS2OTEL_ENRICHMENT__REVERSE_DNS__ACKNOWLEDGE_CARDINALITY` | `false` | `restart` | set true (once metric_limit is sized) to silence the enabled+node_dims cardinality advisory |
| `TS2OTEL_ENRICHMENT__GEOIP__ENABLED` | `false` | `restart` | off by default; tailnet addresses (CGNAT 100.64/10, ULA fd7a:115c:a1e0::/48) are NEVER geolocated |
| `TS2OTEL_ENRICHMENT__GEOIP__COUNTRY_DATABASE` | `""` | `file_content` | path to a GeoLite2/GeoIP2 Country .mmdb; a City database also works and additionally fills locality/region/coordinates on flow LOGS |
| `TS2OTEL_ENRICHMENT__GEOIP__ASN_DATABASE` | `""` | `file_content` | path to a GeoLite2/GeoIP2 ASN .mmdb; the AS number and organization ride flow LOGS only, never a metric |
| `TS2OTEL_ENRICHMENT__GEOIP__RELOAD_INTERVAL` | `6h` | `restart` | re-stat the files above and hot-swap a changed one (this is what makes an external geoipupdate cron work; 0 disables) |
| `TS2OTEL_ENRICHMENT__GEOIP__ACKNOWLEDGE_CARDINALITY` | `false` | `restart` | set true (once metric_limit is sized) to silence the geo_dims-on-raw-flow-metrics advisory |
| `TS2OTEL_ENRICHMENT__GEOIP__DOWNLOAD__ENABLED` | `false` | `restart` | off by default; leave off if something else supplies the .mmdb files |
| `TS2OTEL_ENRICHMENT__GEOIP__DOWNLOAD__ACCOUNT_ID` | `""` | `restart` | MaxMind account ID (a free GeoLite2 account is enough) |
| `TS2OTEL_ENRICHMENT__GEOIP__DOWNLOAD__LICENSE_KEY` | `""` | `restart` | MaxMind license key — set via TS2OTEL_ENRICHMENT__GEOIP__DOWNLOAD__LICENSE_KEY, never in YAML |
| `TS2OTEL_ENRICHMENT__GEOIP__DOWNLOAD__LICENSE_KEY_FILE` | `""` | `restart` | read the license key from a file instead (Docker/Kubernetes secret style) |
| `TS2OTEL_ENRICHMENT__GEOIP__DOWNLOAD__EDITIONS` | `[GeoLite2-Country, GeoLite2-ASN]` | `restart` | MaxMind edition IDs; each installs as <directory>/<edition>.mmdb (swap in GeoLite2-City for locality/coordinates on logs) _(comma-separated list)_ |
| `TS2OTEL_ENRICHMENT__GEOIP__DOWNLOAD__DIRECTORY` | `""` | `restart` | where databases are installed; empty = <state dir>/geoip (same place as the checkpoint file) |
| `TS2OTEL_ENRICHMENT__GEOIP__DOWNLOAD__INTERVAL` | `24h` | `restart` | how often to ask MaxMind for a newer build; each check is conditional, so an unchanged database costs a 304 and no download quota |
| `TS2OTEL_ENRICHMENT__GEOIP__DOWNLOAD__TIMEOUT` | `5m` | `restart` | per-edition download timeout |
| `TS2OTEL_ENRICHMENT__GEOIP__DOWNLOAD__ENDPOINT` | `https://download.maxmind.com/geoip/databases` | `restart` | download API base; override only for a local mirror |
| `TS2OTEL_CARDINALITY__METRIC_LIMIT` | `10000` | `restart` | hard per-metric series cap; beyond it the SDK collapses extras into otel_metric_overflow (0/negative = unlimited) |
| `TS2OTEL_CARDINALITY__DERP_REGION_ROLLUP` | `true` | `restart` | emit tailnet-wide per-DERP-region rollup gauges (tailscale.derp.region.*) |
| `TS2OTEL_CARDINALITY__SUBNET_ROUTE_ROLLUP` | `true` | `restart` | emit per-CIDR tailscale.subnet_routes.routers redundancy gauge (one series per subnet CIDR); fleet exit/subnet counts emit regardless |
| `TS2OTEL_CARDINALITY__WARNING_THRESHOLD` | `2000` | `restart` | status-page cardinality view flags a metric at/above this active-series count (self-obs only; 0 disables) |
| `TS2OTEL_CARDINALITY__CRITICAL_THRESHOLD` | `8000` | `restart` | status-page cardinality view flags a metric critically at/above this count (must be >= warning_threshold; <= metric_limit when set; 0 disables) |
| `TS2OTEL_CARDINALITY__LABEL_VALUE_SAMPLE_CAP` | `100` | `restart` | distinct values retained per (metric,label) for the label-cardinality views; beyond it the label is capped and examples truncated (0 disables label capture) |
| `TS2OTEL_CARDINALITY__FLOW__METRICS_MODE` | `rollup` | `restart` | rollup (bounded top-N, lowest cardinality) \| all (raw per-connection) \| both (≈2x series; summing double-counts) |
| `TS2OTEL_CARDINALITY__FLOW__ROLLUP_TOP_N` | `500` | `restart` | rollup mode: busiest src/dst node pairs kept per flush; the rest fold into an __other__ series (0 = default 500) |
| `TS2OTEL_CARDINALITY__FLOW__SOURCE_PORT` | `false` | `restart` | add source.port to flow metrics. INERT under metrics_mode: rollup (raw modes only) — and the most expensive knob here, ephemeral ports are unbounded |
| `TS2OTEL_CARDINALITY__FLOW__DESTINATION_PORT` | `false` | `restart` | add destination.port to flow metrics. INERT under metrics_mode: rollup (raw modes only); dst.service below is the bounded stand-in and is always on |
| `TS2OTEL_CARDINALITY__FLOW__NODE_DIMS` | `true` | `restart` | include src/dst device names on flow metrics (who talked to whom); off keeps totals but drops the per-peer breakdown and suppresses the unique.* gauges |
| `TS2OTEL_CARDINALITY__FLOW__IDENTITY_DIMS` | `false` | `restart` | add per-flow tailscale.{src,dst}.{user,tags,os} to flow metrics, on BOTH families. REQUIRES node_dims (identity is node-derived, so without it identity becomes the only splitting dimension); flow LOGS always carry them |
| `TS2OTEL_CARDINALITY__FLOW__COLLAPSE_EXTERNAL` | `true` | `restart` | bucket unresolved IPs as external/unknown (keeps cardinality bounded) |
| `TS2OTEL_CARDINALITY__FLOW__EXIT_NODE_ATTRIBUTION` | `true` | `restart` | emit bounded tailscale.exit_node.io/packets attributing exit traffic to the relaying node (bounded by exit-node count) |
| `TS2OTEL_CARDINALITY__FLOW__GEO_DIMS` | `false` | `restart` | add source/destination geo.country.iso_code + geo.continent.code to flow METRICS (needs enrichment.geoip). Nearly free under metrics_mode: rollup (top-N bounded); on the RAW families it splits the single "external" series ~250 ways. ASN/city NEVER reach a metric; flow LOGS carry everything regardless |
| `TS2OTEL_CARDINALITY__PER_ENTITY__DEVICE` | `true` | `restart` | per-device gauges (online/last_seen/key_expiry/derp/routes) |
| `TS2OTEL_CARDINALITY__PER_ENTITY__USER` | `true` | `restart` | per-user gauges (devices/connected/last_seen) |
| `TS2OTEL_CARDINALITY__PER_ENTITY__KEY` | `true` | `restart` | per-key expiry gauge (the expiry WARN log fires regardless) |
| `TS2OTEL_CARDINALITY__PER_ENTITY__WEBHOOK` | `true` | `restart` | per-endpoint webhook-subscriptions gauge |
| `TS2OTEL_CARDINALITY__PER_ENTITY__SERVICE` | `true` | `restart` | per-service ports/hosts gauges |
| `TS2OTEL_COLLECTORS__DEVICES__ENABLED` | `true` | `restart` | device inventory — REQUIRED for flow/audit IP->name enrichment (disabling it degrades names to unknown/external) |
| `TS2OTEL_COLLECTORS__DEVICES__INTERVAL` | `60s` | `restart` | how often the device snapshot is polled |
| `TS2OTEL_COLLECTORS__DEVICES__CHANGE_LOG_ENABLED` | `false` | `restart` | emit structured device add/remove and field-change records. PII-bearing fields still follow pii_filter. TS2OTEL_COLLECTORS__DEVICES__CHANGE_LOG_ENABLED |
| `TS2OTEL_COLLECTORS__DEVICES__COLLECT_ROUTES` | `false` | `restart` | also fetch advertised/primary subnet routes per device |
| `TS2OTEL_COLLECTORS__DEVICES__COLLECT_CONNECTIVITY` | `true` | `restart` | emit per-device NAT/connectivity health (hard_nat/endpoints/direct_capable/udp/ipv6) + fleet rollups from the device payload (no extra API calls) |
| `TS2OTEL_COLLECTORS__DEVICES__COLLECT_POSTURE` | `false` | `restart` | also fetch device posture (MDM/EDR) — enables the posture metrics + log |
| `TS2OTEL_COLLECTORS__DEVICES__COLLECT_DEVICE_INVITES` | `true` | `restart` | also fetch outstanding device share invites per device (one extra API call per device, N+1); emits tailscale.device_invites.count |
| `TS2OTEL_COLLECTORS__DEVICES__SUBREQUEST_CONCURRENCY` | `1` | `restart` | bounded posture/invite request pool; 1 preserves sequential requests |
| `TS2OTEL_COLLECTORS__DEVICES__POSTURE_LOG_MODE` | `changes` | `restart` | needs collect_posture: changes (log only on change) \| always (every scrape) \| off (no log); the posture METRIC is always emitted |
| `TS2OTEL_COLLECTORS__DEVICES__EXPIRY_LOG_MODE` | `daily` | `restart` | node-key AND posture-attribute expiry WARN cadence: daily (change + at most one reminder/24h, default) \| always (every scrape, legacy) \| off; metrics always emit |
| `TS2OTEL_COLLECTORS__DEVICES__ATTRIBUTE_NAMESPACES` | `[intune, jamf, kandji, crowdstrike, sentinelone, kolide, ip]` | `restart` | needs collect_posture: posture-key namespaces promoted to attribute metrics; ["*"] = all, [] = disable _(comma-separated list)_ |
| `TS2OTEL_COLLECTORS__DEVICES__ATTRIBUTE_KEY_LIMIT` | `200` | `restart` | busiest posture attribute keys promoted fleet-wide; overflow keys are dropped and counted (0/negative = unlimited); cardinality.metric_limit is still the final SDK guard |
| `TS2OTEL_COLLECTORS__DEVICES__ATTRIBUTE_VALUE_LIMIT` | `50` | `restart` | busiest values per attribute key on the .info gauge; overflow folds to value="__other__" (0/negative = unlimited) |
| `TS2OTEL_COLLECTORS__DEVICES__COLLECT_TAG_ROLLUP` | `true` | `restart` | emit tailscale.devices.by_tag (one series per ACL tag); false keeps the other fleet-hygiene aggregates |
| `TS2OTEL_COLLECTORS__DEVICES__TAG_ROLLUP_LIMIT` | `50` | `restart` | cap distinct tag series on by_tag; busiest N kept, rest fold into tag="__other__" (0/negative = unlimited) |
| `TS2OTEL_COLLECTORS__FLOWLOGS__ENABLED` | `true` | `restart` | network flow logs -> traffic counters + per-connection logs |
| `TS2OTEL_COLLECTORS__FLOWLOGS__SOURCE` | `poll` | `restart` | poll (this exporter PULLS) \| stream (Tailscale PUSHES to the streaming receiver) \| objectstore (read Tailscale's S3 export) \| both (discouraged: double-counts) |
| `TS2OTEL_COLLECTORS__FLOWLOGS__INTERVAL` | `60s` | `restart` | poll only — how often a window is polled |
| `TS2OTEL_COLLECTORS__FLOWLOGS__LAG` | `120s` | `restart` | poll only — query only up to now-lag so late-arriving records aren't missed |
| `TS2OTEL_COLLECTORS__FLOWLOGS__INITIAL_LOOKBACK` | `5m` | `restart` | poll only — cold-start reach-back when there is no checkpoint yet |
| `TS2OTEL_COLLECTORS__FLOWLOGS__MAX_WINDOW` | `1h` | `restart` | poll only — cap one tick's window so a long outage catches up over several ticks |
| `TS2OTEL_COLLECTORS__FLOWLOGS__DEDUP_CAPACITY` | `16384` | `restart` | identities retained for poll-window AND cross-source dedup; must be >0 (unlimited would leak memory) |
| `TS2OTEL_COLLECTORS__FLOWLOGS__REPLAY_OVERLAP` | `5m` | `restart` | poll only — reread this completed-window overlap for records that became available late (0 disables) |
| `TS2OTEL_COLLECTORS__FLOWLOGS__REPLAY_SEEN_CAPACITY` | `131072` | `restart` | poll only — durable hashed connection identities retained for overlap dedup (1..1048576 when enabled) |
| `TS2OTEL_COLLECTORS__FLOWLOGS__TRUSTED_REPORTER_NODE_IDS` | `[]` | `restart` | verified FlowLog.NodeID values classified as configured; empty with trusted_reporter_tags means trust policy is unconfigured _(comma-separated list)_ |
| `TS2OTEL_COLLECTORS__FLOWLOGS__TRUSTED_REPORTER_TAGS` | `[]` | `restart` | authoritative device tags (e.g. ["tag:router"]) classified as tagged; embedded flow tags never grant trust _(comma-separated list)_ |
| `TS2OTEL_COLLECTORS__FLOWLOGS__LOG_MODE` | `per_connection` | `restart` | per_connection \| per_record \| off — log detail level (applies to poll AND stream) |
| `TS2OTEL_COLLECTORS__FLOWLOGS__MAX_LOG_RECORDS_PER_WINDOW` | `0` | `restart` | cap flow LOG records per window (0 = unlimited); excess -> tailscale.network.flow.logs_dropped (metrics are never capped) |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__ENDPOINT` | `""` | `restart` | required — service URL, e.g. https://s3.eu-west-2.amazonaws.com, or a MinIO/Ceph address (never derived from the region) |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__REGION` | `""` | `restart` | required — part of the request signature; a wrong value fails every request with HTTP 403 |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__BUCKET` | `""` | `restart` | required — the bucket Tailscale exports into |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__PREFIX` | `""` | `restart` | the export's root within the bucket, above the YYYY/MM/DD partitions; NO leading slash (it becomes part of this feed's durable checkpoint identity, so removing it later re-emits history) |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__LAYOUT` | `partitioned` | `restart` | partitioned (Tailscale's own export: objects under prefix/YYYY/MM/DD/) \| flat (a COPIED export whose self-contained YYYY-MM-DD-HH-MM-SS basenames sit directly under prefix; finds partitioned objects too, but costs more LIST requests since nothing bounds the re-walk) |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__PATH_STYLE` | `false` | `restart` | address as <endpoint>/<bucket>/<key>; required by most non-AWS implementations |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__ALLOW_INSECURE_HTTP` | `false` | `restart` | remote plaintext endpoints are rejected by default; loopback HTTP remains available for local MinIO development |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__ACCESS_KEY_ID` | `""` | `restart` | SET VIA ENV ONLY. Leave empty to use the ambient chain: environment, then IRSA/web identity, then the ECS/EKS container credential endpoint, then EC2 instance profile |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__ACCESS_KEY_ID_FILE` | `""` | `restart` | read the access key ID from this path instead; value XOR file |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__SECRET_ACCESS_KEY` | `""` | `restart` | SET VIA ENV ONLY |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__SECRET_ACCESS_KEY_FILE` | `""` | `restart` | read the secret access key from this path instead; value XOR file |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__SESSION_TOKEN` | `""` | `restart` | SET VIA ENV ONLY — temporary credentials only |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__SESSION_TOKEN_FILE` | `""` | `restart` | read the temporary session token from this path instead; value XOR file |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__INTERVAL` | `60s` | `restart` | how often the bucket is listed |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__LOOKBACK` | `1h` | `restart` | how far back past the cursor each listing reaches, so a late-arriving object is still found |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__INITIAL_LOOKBACK` | `6h` | `restart` | cold-start reach-back, so a first run against a long history doesn't ingest all of it; CAPPED IN EFFECT AT 14 DAYS under layout: partitioned (a larger value silently ingests only the newest 14 day partitions — use layout: flat to reach further back) |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__MAX_OBJECTS` | `200` | `restart` | objects ingested per cycle; the remainder is counted, logged and picked up next cycle |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__MAX_SEEN_KEYS` | `5000` | `restart` | durable seen-object identities retained per destination; too low can re-ingest an evicted object inside the lookback |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__MAX_OBJECT_WIRE_BYTES` | `67108864` | `restart` | reject and quarantine one object requiring more than 64 MiB of GET response bytes |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__MAX_OBJECT_DECOMPRESSED_BYTES` | `33554432` | `restart` | reject and quarantine one object that expands beyond 32 MiB |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__MAX_OBJECT_RECORDS` | `100000` | `restart` | reject and quarantine one object containing more than this many records |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__MAX_CYCLE_WIRE_BYTES` | `536870912` | `restart` | defer untouched objects after 512 MiB of GET response data in one cycle |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__MAX_CYCLE_DECOMPRESSED_BYTES` | `268435456` | `restart` | defer untouched objects after 256 MiB of decoded data in one cycle |
| `TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__MAX_CYCLE_RECORDS` | `500000` | `restart` | defer untouched objects after this many decoded records in one cycle |
| `TS2OTEL_COLLECTORS__AUDITLOGS__ENABLED` | `true` | `restart` | configuration/audit events -> event logs + a counter |
| `TS2OTEL_COLLECTORS__AUDITLOGS__SOURCE` | `poll` | `restart` | poll \| stream \| both \| objectstore (see flowlogs) |
| `TS2OTEL_COLLECTORS__AUDITLOGS__INTERVAL` | `60s` | `restart` | poll only |
| `TS2OTEL_COLLECTORS__AUDITLOGS__LAG` | `60s` | `restart` | poll only |
| `TS2OTEL_COLLECTORS__AUDITLOGS__INITIAL_LOOKBACK` | `5m` | `restart` | poll only |
| `TS2OTEL_COLLECTORS__AUDITLOGS__MAX_WINDOW` | `6h` | `restart` | poll only |
| `TS2OTEL_COLLECTORS__AUDITLOGS__DEDUP_CAPACITY` | `4096` | `restart` | identities retained for poll-window AND audit/webhook cross-source dedup; must be >0 |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__ENDPOINT` | `""` | `restart` | required — service URL, e.g. https://s3.eu-west-2.amazonaws.com, or a MinIO/Ceph address (never derived from the region) |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__REGION` | `""` | `restart` | required — part of the request signature; a wrong value fails every request with HTTP 403 |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__BUCKET` | `""` | `restart` | required — the bucket Tailscale exports configuration logs into |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__PREFIX` | `""` | `restart` | the export's root within the bucket, above the YYYY/MM/DD partitions; use a distinct prefix when flow and configuration logs share one bucket. NO leading slash (see flowlogs) |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__LAYOUT` | `partitioned` | `restart` | partitioned (Tailscale's own export: objects under prefix/YYYY/MM/DD/) \| flat (a COPIED export whose self-contained YYYY-MM-DD-HH-MM-SS basenames sit directly under prefix; finds partitioned objects too, but costs more LIST requests since nothing bounds the re-walk) |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__PATH_STYLE` | `false` | `restart` | address as <endpoint>/<bucket>/<key>; required by most non-AWS implementations |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__ALLOW_INSECURE_HTTP` | `false` | `restart` | remote plaintext endpoints are rejected by default; loopback HTTP remains available for local MinIO development |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__ACCESS_KEY_ID` | `""` | `restart` | SET VIA ENV ONLY. Leave empty to use the ambient chain: environment, then IRSA/web identity, then the ECS/EKS container credential endpoint, then EC2 instance profile |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__ACCESS_KEY_ID_FILE` | `""` | `restart` | read the access key ID from this path instead; value XOR file |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__SECRET_ACCESS_KEY` | `""` | `restart` | SET VIA ENV ONLY |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__SECRET_ACCESS_KEY_FILE` | `""` | `restart` | read the secret access key from this path instead; value XOR file |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__SESSION_TOKEN` | `""` | `restart` | SET VIA ENV ONLY — temporary credentials only |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__SESSION_TOKEN_FILE` | `""` | `restart` | read the temporary session token from this path instead; value XOR file |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__INTERVAL` | `60s` | `restart` | how often the bucket is listed |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__LOOKBACK` | `1h` | `restart` | how far back past the cursor each listing reaches, so a late-arriving object is still found |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__INITIAL_LOOKBACK` | `6h` | `restart` | cold-start reach-back, so a first run against a long history doesn't ingest all of it; CAPPED IN EFFECT AT 14 DAYS under layout: partitioned (a larger value silently ingests only the newest 14 day partitions — use layout: flat to reach further back) |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__MAX_OBJECTS` | `200` | `restart` | objects ingested per cycle; the remainder is counted, logged and picked up next cycle |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__MAX_SEEN_KEYS` | `5000` | `restart` | durable seen-object identities retained per destination |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__MAX_OBJECT_WIRE_BYTES` | `67108864` | `restart` | reject and quarantine one object requiring more than 64 MiB of GET response bytes |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__MAX_OBJECT_DECOMPRESSED_BYTES` | `33554432` | `restart` | reject and quarantine one object that expands beyond 32 MiB |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__MAX_OBJECT_RECORDS` | `100000` | `restart` | reject and quarantine one object containing more than this many records |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__MAX_CYCLE_WIRE_BYTES` | `536870912` | `restart` | defer untouched objects after 512 MiB of GET response data in one cycle |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__MAX_CYCLE_DECOMPRESSED_BYTES` | `268435456` | `restart` | defer untouched objects after 256 MiB of decoded data in one cycle |
| `TS2OTEL_COLLECTORS__AUDITLOGS__OBJECTSTORE__MAX_CYCLE_RECORDS` | `500000` | `restart` | defer untouched objects after this many decoded records in one cycle |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__ENABLED` | `false` | `restart` | Kubernetes API-audit events from Tailscale's tsrecorder -> request logs + bounded counters. Requires enableEvents in the tailscale.com/cap/kubernetes ACL grant (BETA upstream). NOTE: the source carries no response status, latency or byte count, so allowed-vs-denied and error rates are NOT derivable |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__ENDPOINT` | `""` | `restart` | required — service URL, e.g. https://s3.eu-west-1.amazonaws.com, or a MinIO/Ceph address |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__REGION` | `""` | `restart` | required — part of the request signature; a wrong value fails every request with HTTP 403 |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__BUCKET` | `""` | `restart` | required — the bucket tsrecorder writes recordings into. Never inherited from the flowlogs/auditlogs destinations: this is a separate bucket with its own key layout |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__PREFIX` | `""` | `restart` | usually EMPTY: tsrecorder keys are <stableID>/events/<ts>.event and <stableID>/<ts>.cast, and <stableID> differs per recorder replica so it cannot be pinned in a prefix. NO leading slash |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__LAYOUT` | `recorder` | `restart` | recorder is the only accepted value here; partitioned and flat are REFUSED because tsrecorder writes no YYYY/MM/DD partitions and its RFC3339Nano basenames sort differently |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__PATH_STYLE` | `false` | `restart` | address as <endpoint>/<bucket>/<key>; required by most non-AWS implementations |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__ALLOW_INSECURE_HTTP` | `false` | `restart` | remote plaintext endpoints are rejected by default; loopback HTTP remains available for local MinIO development |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__ACCESS_KEY_ID` | `""` | `restart` | SET VIA ENV ONLY. Leave empty to use the ambient chain: environment, then IRSA/web identity, then the ECS/EKS container credential endpoint, then EC2 instance profile |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__ACCESS_KEY_ID_FILE` | `""` | `restart` | read the access key ID from this path instead; value XOR file |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__SECRET_ACCESS_KEY` | `""` | `restart` | SET VIA ENV ONLY |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__SECRET_ACCESS_KEY_FILE` | `""` | `restart` | read the secret access key from this path instead; value XOR file |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__SESSION_TOKEN` | `""` | `restart` | SET VIA ENV ONLY — temporary credentials only |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__SESSION_TOKEN_FILE` | `""` | `restart` | read the temporary session token from this path instead; value XOR file |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__INTERVAL` | `60s` | `restart` | how often the bucket is listed |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__LOOKBACK` | `1h` | `restart` | how far back past the cursor each listing reaches, so a late-arriving object is still found |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__INITIAL_LOOKBACK` | `6h` | `restart` | cold-start reach-back, so a first run against a long history doesn't ingest all of it |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__MAX_OBJECTS` | `200` | `restart` | objects ingested per cycle; the remainder is counted, logged and picked up next cycle. tsrecorder writes ONE event per object, so a busy cluster needs a higher value here than a flow/audit export does |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__MAX_SEEN_KEYS` | `5000` | `restart` | durable seen-object identities retained per destination |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__MAX_OBJECT_WIRE_BYTES` | `67108864` | `restart` | reject and quarantine one object requiring more than 64 MiB of GET response bytes |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__MAX_OBJECT_DECOMPRESSED_BYTES` | `33554432` | `restart` | reject and quarantine one object that expands beyond 32 MiB. RAISE THIS if you record long terminal sessions: only the .cast header line is read for meaning, but the whole object is still streamed, and an oversized one is quarantined rather than partially read |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__MAX_OBJECT_RECORDS` | `100000` | `restart` | reject and quarantine one object containing more than this many records |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__MAX_CYCLE_WIRE_BYTES` | `536870912` | `restart` | defer untouched objects after 512 MiB of GET response data in one cycle |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__MAX_CYCLE_DECOMPRESSED_BYTES` | `268435456` | `restart` | defer untouched objects after 256 MiB of decoded data in one cycle |
| `TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__MAX_CYCLE_RECORDS` | `500000` | `restart` | defer untouched objects after this many decoded records in one cycle |
| `TS2OTEL_COLLECTORS__USERS__ENABLED` | `true` | `restart` | user inventory (devices/connected/last_seen per user) |
| `TS2OTEL_COLLECTORS__USERS__INTERVAL` | `300s` | `restart` | user inventory (devices/connected/last_seen per user) |
| `TS2OTEL_COLLECTORS__KEYS__ENABLED` | `true` | `restart` | auth-key inventory + expiry warnings |
| `TS2OTEL_COLLECTORS__KEYS__INTERVAL` | `300s` | `restart` | auth-key inventory + expiry warnings |
| `TS2OTEL_COLLECTORS__KEYS__EXPIRY_WARN` | `168h` | `restart` | log a WARN when a key expires within this window (default 7d) |
| `TS2OTEL_COLLECTORS__KEYS__EXPIRY_LOG_MODE` | `daily` | `restart` | WARN cadence: daily (change + at most one reminder/24h, default) \| always (every scrape, legacy) \| off; metrics always emit |
| `TS2OTEL_COLLECTORS__SETTINGS__ENABLED` | `true` | `restart` | tailnet settings snapshot |
| `TS2OTEL_COLLECTORS__SETTINGS__INTERVAL` | `600s` | `restart` | tailnet settings snapshot |
| `TS2OTEL_COLLECTORS__SETTINGS__SNAPSHOT_ENABLED` | `false` | `restart` | emit the complete settings response to logs on change plus a heartbeat. TS2OTEL_COLLECTORS__SETTINGS__SNAPSHOT_ENABLED |
| `TS2OTEL_COLLECTORS__PAM__ENABLED` | `false` | `restart` | requires a read-only service-account token in TS2OTEL_PAM__TOKEN |
| `TS2OTEL_COLLECTORS__PAM__INTERVAL` | `600s` | `restart` | connector/service/policy/identity/org inventory and config-shape interval |
| `TS2OTEL_COLLECTORS__PAM__SESSIONS_INTERVAL` | `60s` | `restart` | incremental newest-first session polling interval |
| `TS2OTEL_COLLECTORS__PAM__SNAPSHOT_ENABLED` | `false` | `restart` | opt in to redacted PAM configuration snapshots; secrets are stripped before serialization |
| `TS2OTEL_COLLECTORS__PAM__SNAPSHOT_HEARTBEAT` | `24h` | `restart` | refresh unchanged snapshots at this cadence |
| `TS2OTEL_COLLECTORS__PAM__SNAPSHOT_BODY_BYTES` | `32768` | `restart` | maximum serialized snapshot event body |
| `TS2OTEL_COLLECTORS__ACL__ENABLED` | `true` | `restart` | ACL policy snapshot |
| `TS2OTEL_COLLECTORS__ACL__INTERVAL` | `600s` | `restart` | ACL policy snapshot |
| `TS2OTEL_COLLECTORS__ACL__SNAPSHOT_ENABLED` | `false` | `restart` | EXPLICIT PII CONSENT: ship the raw policy and diffs, including every user email and group member, to the logs backend. This overrides pii_filter for those bodies, so logs retention holds tailnet identity data. TS2OTEL_COLLECTORS__ACL__SNAPSHOT_ENABLED |
| `TS2OTEL_COLLECTORS__ACL__SNAPSHOT_HEARTBEAT` | `24h` | `restart` | refresh an unchanged raw policy snapshot at this cadence. TS2OTEL_COLLECTORS__ACL__SNAPSHOT_HEARTBEAT |
| `TS2OTEL_COLLECTORS__ACL__VALIDATE` | `true` | `restart` | run the non-mutating POST /acl/validate (policy_file:read scope) each tick; set false to keep the client strictly GET-only |
| `TS2OTEL_COLLECTORS__DNS__ENABLED` | `true` | `restart` | DNS/MagicDNS settings snapshot |
| `TS2OTEL_COLLECTORS__DNS__INTERVAL` | `600s` | `restart` | DNS/MagicDNS settings snapshot |
| `TS2OTEL_COLLECTORS__DNS__SNAPSHOT_ENABLED` | `false` | `restart` | emit the complete DNS response to logs on change plus a heartbeat. TS2OTEL_COLLECTORS__DNS__SNAPSHOT_ENABLED |
| `TS2OTEL_COLLECTORS__CONTACTS__ENABLED` | `true` | `restart` | account/support/security contact verification status (no emails emitted) |
| `TS2OTEL_COLLECTORS__CONTACTS__INTERVAL` | `600s` | `restart` | account/support/security contact verification status (no emails emitted) |
| `TS2OTEL_COLLECTORS__WEBHOOKS__ENABLED` | `true` | `restart` | webhook-endpoint inventory: count + per-endpoint subscription count (no url/secret) |
| `TS2OTEL_COLLECTORS__WEBHOOKS__INTERVAL` | `600s` | `restart` | webhook-endpoint inventory: count + per-endpoint subscription count (no url/secret) |
| `TS2OTEL_COLLECTORS__WEBHOOKS__SNAPSHOT_ENABLED` | `false` | `restart` | emit the complete webhook inventory response to logs on change plus a heartbeat. TS2OTEL_COLLECTORS__WEBHOOKS__SNAPSHOT_ENABLED |
| `TS2OTEL_COLLECTORS__WEBHOOKS__DESIRED_EVENTS` | `[]` | `restart` | optional expected event categories (e.g. ["nodeCreated","userSuspended"]); empty means no expectation _(comma-separated list)_ |
| `TS2OTEL_COLLECTORS__POSTURE_INTEGRATIONS__ENABLED` | `true` | `restart` | MDM/EDR posture-integration sync health: matched counts + last_sync staleness |
| `TS2OTEL_COLLECTORS__POSTURE_INTEGRATIONS__INTERVAL` | `600s` | `restart` | MDM/EDR posture-integration sync health: matched counts + last_sync staleness |
| `TS2OTEL_COLLECTORS__POSTURE_INTEGRATIONS__SNAPSHOT_ENABLED` | `false` | `restart` | emit the complete posture-integration response to logs on change plus a heartbeat. TS2OTEL_COLLECTORS__POSTURE_INTEGRATIONS__SNAPSHOT_ENABLED |
| `TS2OTEL_COLLECTORS__LOG_STREAM__ENABLED` | `true` | `restart` | log-streaming delivery health to a SIEM sink (self-gates to configured=0 when no sink) |
| `TS2OTEL_COLLECTORS__LOG_STREAM__INTERVAL` | `600s` | `restart` | log-streaming delivery health to a SIEM sink (self-gates to configured=0 when no sink) |
| `TS2OTEL_COLLECTORS__LOG_STREAM__CONFIGURATION_INTERVAL` | `0s` | `restart` | 0 inherits interval; otherwise probe configuration-log delivery independently |
| `TS2OTEL_COLLECTORS__LOG_STREAM__NETWORK_INTERVAL` | `0s` | `restart` | 0 inherits interval; otherwise probe network-log delivery independently |
| `TS2OTEL_COLLECTORS__OAUTH_APPS__ENABLED` | `true` | `restart` | OAuth-application inventory (alpha API; idles silently — no error — on tailnets without it) |
| `TS2OTEL_COLLECTORS__OAUTH_APPS__INTERVAL` | `300s` | `restart` | OAuth-application inventory (alpha API; idles silently — no error — on tailnets without it) |
| `TS2OTEL_COLLECTORS__SERVICES__ENABLED` | `true` | `restart` | Tailscale Services (VIP) inventory |
| `TS2OTEL_COLLECTORS__SERVICES__INTERVAL` | `600s` | `restart` | Tailscale Services (VIP) inventory |
| `TS2OTEL_COLLECTORS__SERVICES__COLLECT_HOSTS` | `false` | `restart` | also fetch per-service backing-host detail — one extra API call per service (N+1) |
| `TS2OTEL_COLLECTORS__SERVICES__SUBREQUEST_CONCURRENCY` | `1` | `restart` | bounded backing-host request pool; 1 preserves sequential requests |
| `TS2OTEL_COLLECTORS__SERVICES__COLLECT_TAG_ROLLUP` | `true` | `restart` | emit tailscale.services.by_tag (one series per ACL tag); false disables the rollup |
| `TS2OTEL_COLLECTORS__SERVICES__TAG_ROLLUP_LIMIT` | `50` | `restart` | cap distinct service-tag series; busiest N kept, rest fold into tailscale.tag="__other__" (0/negative = unlimited) |
| `TS2OTEL_COLLECTORS__NODE_METRICS__ENABLED` | `false` | `restart` | OPTIONAL: scrape tailscaled per-node Prometheus /metrics and forward them centrally. Off by default; see docs/node-metrics.md |
| `TS2OTEL_COLLECTORS__NODE_METRICS__INTERVAL` | `60s` | `restart` | how often each target is scraped |
| `TS2OTEL_COLLECTORS__NODE_METRICS__TIMEOUT` | `10s` | `restart` | per-scrape HTTP timeout |
| `TS2OTEL_COLLECTORS__NODE_METRICS__MAX_RESPONSE_BYTES` | `4194304` | `restart` | per-target response cap (4 MiB) — bounds memory |
| `TS2OTEL_COLLECTORS__NODE_METRICS__MAX_SAMPLES` | `50000` | `restart` | per-target sample cap per scrape — bounds cardinality |
| `TS2OTEL_COLLECTORS__NODE_METRICS__MAX_DISTINCT_METRICS` | `2000` | `restart` | cap on DISTINCT forwarded metric NAMES over the process lifetime (targets choose their own names and each new one creates a permanent instrument); 0 = 2000 default, negative = unlimited (over-budget names are dropped and counted) |
| `TS2OTEL_COLLECTORS__NODE_METRICS__METRIC_ALLOW` | `[]` | `restart` | if non-empty, only forwarded metric names matching one of these anchored regexes are kept _(comma-separated list)_ |
| `TS2OTEL_COLLECTORS__NODE_METRICS__METRIC_DENY` | `[]` | `restart` | forwarded metric names matching any of these anchored regexes are dropped (after allow) _(comma-separated list)_ |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DROP_LABELS` | `[]` | `restart` | label keys stripped from every forwarded series (the tailscale.node identity label is never dropped) _(comma-separated list)_ |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__ENABLED` | `false` | `restart` | OPTIONAL: discover scrape targets from the Tailscale devices API (unioned with static targets) |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__INTERVAL` | `5m` | `restart` | how often the device inventory is re-scanned for targets |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__MAX_TARGETS` | `1000` | `restart` | cap emitted discovered targets per refresh (one target per selected port, not one per device) |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__SCHEME` | `http` | `restart` | http \| https — metrics endpoint scheme on each device |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__PORT` | `5252` | `restart` | tailscaled client-metrics port |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__PATH` | `/metrics` | `restart` | metrics endpoint path |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__ONLINE_ONLY` | `true` | `restart` | only devices currently connected to the control plane |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__EXCLUDE_EXTERNAL` | `true` | `restart` | skip shared/external devices |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__INCLUDE_TAGS` | `[]` | `restart` | only devices with one of these tags (empty = all), e.g. ["tag:server"] _(comma-separated list)_ |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__EXCLUDE_TAGS` | `[]` | `restart` | devices with any of these tags are skipped (wins over include_tags) _(comma-separated list)_ |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__ADDRESS_ORDER` | `ipv4` | `restart` | ipv4 \| ipv6 — preferred address family (falls back to the other) |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__INSTANCE_SOURCE` | `name` | `restart` | identity label per target: name (MagicDNS short name, unique+friendly — default) \| address (Tailscale host:port, always unique) \| hostname (OS hostname, NOT unique — collisions like "localhost" are auto-suffixed) |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__INCLUDE_HOST_LABELS` | `true` | `restart` | attach host.name/host.id for joins with tailscale.device.* |
| `TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__INCLUDE_TAGS_LABEL` | `true` | `restart` | attach tailscale.tags to each target's series |
| `TS2OTEL_SCHEDULER__INITIAL_STAGGER_WINDOW` | `3s` | `restart` | spread initial collector ticks; current single-runtime default is 3s |
| `TS2OTEL_COORDINATION__MODE` | `none` | `restart` | none (default singleton behavior) \| kubernetes (Lease-based active-passive) |
| `TS2OTEL_COORDINATION__LEASE_NAME` | `tailscale2otel` | `restart` | DNS-1123 Lease name; all replicas use the same name |
| `TS2OTEL_COORDINATION__NAMESPACE` | `default` | `restart` | DNS-1123 namespace containing the Lease |
| `TS2OTEL_COORDINATION__LEASE_DURATION` | `15s` | `restart` | leader lease expiry; must be > renew_deadline > retry_period |
| `TS2OTEL_COORDINATION__RENEW_DEADLINE` | `10s` | `restart` | step down when the apiserver cannot renew within this period |
| `TS2OTEL_COORDINATION__RETRY_PERIOD` | `2s` | `restart` | standby acquire and leader renew retry interval |
| `TS2OTEL_CHECKPOINT__STORE` | `file` | `restart` | file (persists window cursors across restarts; falls back to memory + WARN if the path isn't writable) \| memory (RAM only; cold-starts from initial_lookback after a restart) \| kubernetes (one gzip binaryData ConfigMap per collector namespace, owned by coordination.lease_name) |
| `TS2OTEL_CHECKPOINT__EVIDENCE_STORE` | `file` | `restart` | file (default; preserves ACL revision/audit provenance across restarts) \| memory (unsafe except disposable runs; produces a specific warning). Independent of store, so streamed deployments may use store: memory with evidence_store: file. |
| `TS2OTEL_CHECKPOINT__FILE_PATH` | `/var/lib/tailscale2otel/checkpoints.json` | `restart` | used when either store is file — both file-backed classes share this atomic JSON file, preserving existing checkpoint keys. Mount a writable, persistent path here. This default suits a CONTAINER (the image pre-seeds it for uid 65532); a native run that cannot write it falls back to the platform state dir (~/.local/state, ~/Library/Application Support, %LocalAppData%) and logs where it went. Set this explicitly and it is used as-is — an explicit path is never relocated. |
| `TS2OTEL_CHECKPOINT__WRITE_DEBOUNCE` | `0s` | `restart` | coalesce nearby file writes; 0 preserves synchronous Set durability |
| `TS2OTEL_INGRESS_WAL__ENABLED` | `false` | `restart` | opt in to durable local acceptance and oldest-first replay for receiver payloads |
| `TS2OTEL_INGRESS_WAL__DIRECTORY` | `/var/lib/tailscale2otel/ingress-wal` | `restart` | absolute, filepath-clean, non-root directory; mount durable state here when reschedule survival matters |
| `TS2OTEL_INGRESS_WAL__MAX_BYTES` | `268435456` | `restart` | encoded WAL byte ceiling (256 MiB); full WAL fails new requests closed; no TTL/eviction |
| `TS2OTEL_INGRESS_WAL__MAX_ENTRIES` | `10000` | `restart` | encoded entry ceiling; full WAL fails new requests closed; no TTL/eviction |
| `TS2OTEL_INGRESS_WAL__CORRUPTION` | `fail` | `restart` | only supported mode: fail closed rather than discard corrupt state |
| `TS2OTEL_STREAMING__ENABLED` | `false` | `restart` | run the Splunk-HEC receiver to INGEST pushed logs (set the relevant collectors' source: stream) |
| `TS2OTEL_STREAMING__LISTEN` | `:8088` | `restart` | bind address for the Splunk-HEC-compatible receiver |
| `TS2OTEL_STREAMING__PATH` | `/services/collector/event` | `restart` | HEC endpoint path Tailscale POSTs to |
| `TS2OTEL_STREAMING__TOKEN` | `""` | `restart` | shared secret; Tailscale sends HTTP Basic auth (base64 user:token), "Authorization: Splunk <token>" also accepted as a fallback (set via TS2OTEL_STREAMING__TOKEN); REQUIRED when enabled on a non-loopback bind (loopback may remain credential-free for local-only use) |
| `TS2OTEL_STREAMING__TOKEN_FILE` | `""` | `file_content` | read the value from this file instead (Docker secrets); set the value or the file, not both; content is whitespace-trimmed |
| `TS2OTEL_STREAMING__PUBLIC_URL` | `""` | `restart` | externally reachable receiver URL; REQUIRED only when auto_configure: true |
| `TS2OTEL_STREAMING__TLS__CERT_FILE` | `""` | `file_content` | HTTPS cert (Tailscale requires HTTPS; a `tailscale cert` works for private endpoints) |
| `TS2OTEL_STREAMING__TLS__KEY_FILE` | `""` | `file_content` | HTTPS key |
| `TS2OTEL_STREAMING__DECOMPRESS` | `auto` | `restart` | auto \| gzip \| zstd \| none — request body decompression |
| `TS2OTEL_STREAMING__AUTO_CONFIGURE` | `false` | `restart` | on startup, register THIS receiver as the tailnet's log-streaming sink for BOTH log types (network/flow AND configuration/audit), OVERWRITING any existing sink for either; needs enabled + public_url + the log_streaming OAuth scope |
| `TS2OTEL_STREAMING__MAX_BODY_BYTES` | `0` | `restart` | cap on the DECOMPRESSED body; 0 = 64 MiB default, negative = unlimited (over-cap = 413); when ingress_wal.enabled this receiver must set a positive value <= 64 MiB |
| `TS2OTEL_STREAMING__MAX_CONCURRENT_REQUESTS` | `0` | `restart` | how many requests may buffer a body AT ONCE (max_body_bytes caps one body, this caps their sum); 0 = 4 default, negative = unlimited (over-limit = 503 + Retry-After) |
| `TS2OTEL_STREAMING__PER_ROUTE_MAX_CONCURRENT_REQUESTS` | `0` | `restart` | per-tailnet admission cap; 0 selects an automatic fair share of the global budget |
| `TS2OTEL_WEBHOOK__ENABLED` | `false` | `restart` | run the receiver for real-time Tailscale webhook events |
| `TS2OTEL_WEBHOOK__LISTEN` | `:8089` | `restart` | bind address for the webhook receiver |
| `TS2OTEL_WEBHOOK__PATH` | `/tailscale/webhook` | `restart` | endpoint path Tailscale POSTs events to |
| `TS2OTEL_WEBHOOK__SECRET` | `""` | `restart` | HMAC-SHA256 verification secret (set via TS2OTEL_WEBHOOK__SECRET); REQUIRED when enabled on a non-loopback bind (loopback may remain credential-free for local-only use) |
| `TS2OTEL_WEBHOOK__SECRET_FILE` | `""` | `file_content` | read the value from this file instead (Docker secrets); set the value or the file, not both; content is whitespace-trimmed |
| `TS2OTEL_WEBHOOK__TLS__CERT_FILE` | `""` | `file_content` | serve native HTTPS when paired with key_file; leave both empty behind an HTTPS reverse proxy |
| `TS2OTEL_WEBHOOK__TLS__KEY_FILE` | `""` | `file_content` | private key paired with cert_file; both paths are validated as readable at startup |
| `TS2OTEL_WEBHOOK__TOLERANCE` | `5m` | `restart` | reject signed timestamps older than this (replay window); "0" disables the check |
| `TS2OTEL_WEBHOOK__DEDUP_AUDIT_EVENTS` | `false` | `restart` | best-effort: drop a webhook event already counted via the audit logs |
| `TS2OTEL_WEBHOOK__MAX_BODY_BYTES` | `0` | `restart` | cap on the raw body read before signature verification; 0 = 1 MiB default, negative = unlimited (over-cap = 413); when ingress_wal.enabled this receiver must set a positive value <= 64 MiB |
| `TS2OTEL_WEBHOOK__MAX_CONCURRENT_REQUESTS` | `0` | `restart` | how many requests may buffer a body AT ONCE, BEFORE the HMAC is checked (max_body_bytes caps one body, this caps their sum); 0 = 4 default, negative = unlimited (over-limit = 503 + Retry-After) |
| `TS2OTEL_WEBHOOK__PER_ROUTE_MAX_CONCURRENT_REQUESTS` | `0` | `restart` | per-tailnet admission cap; 0 selects an automatic fair share of the global budget |
| `TS2OTEL_PII_FILTER__EMAILS` | `true` | `restart` | user/actor login names (often email addresses) |
| `TS2OTEL_PII_FILTER__USER_DISPLAY_NAMES` | `true` | `restart` | actor display (human) names |
| `TS2OTEL_PII_FILTER__USER_IDS` | `true` | `restart` | numeric/opaque user IDs (user.id) |
| `TS2OTEL_PII_FILTER__HOSTNAMES` | `true` | `restart` | device + collector-host hostnames |
| `TS2OTEL_PII_FILTER__NODE_IDS` | `true` | `restart` | Tailscale node IDs |
| `TS2OTEL_PII_FILTER__TAILSCALE_IPS` | `true` | `restart` | 100.64.0.0/10 + fd7a:115c:a1e0::/48 addresses |
| `TS2OTEL_PII_FILTER__INTERNAL_IPS` | `true` | `restart` | RFC1918 / ULA / link-local addresses |
| `TS2OTEL_PII_FILTER__EXTERNAL_IPS` | `true` | `restart` | public/routable addresses |
| `TS2OTEL_PII_FILTER__SERVICE_ADDRS` | `true` | `restart` | VIP service names and optional display names |
| `TS2OTEL_PII_FILTER__ENDPOINT_PATHS` | `true` | `restart` | Tailscale API endpoint paths (self-obs) |
| `TS2OTEL_PII_FILTER__NETWORK_TOPOLOGY` | `true` | `restart` | route CIDRs + split-DNS domains + search paths |
| `TS2OTEL_PII_FILTER__TAILNET_NAME` | `true` | `restart` | tailnet identifier |
| `TS2OTEL_PII_FILTER__FREE_TEXT_DETAILS` | `true` | `restart` | audit old/new/details, target names, key descriptions, posture values |
| `TS2OTEL_PII_FILTER__COMMAND_TEXT` | `true` | `restart` | verbatim `kubectl exec` command line on Kubernetes-audit logs; the only attribute a human types at a shell, so it can carry a pasted secret. Turning it off KEEPS the bounded tailscale.k8s.command_class classification the exec metrics are built on |
| `TS2OTEL_SELF_OBSERVABILITY__ENABLED` | `true` | `restart` | emit tailscale2otel.up, api.requests, runtime metrics, etc. |
| `TS2OTEL_SELF_OBSERVABILITY__INSTANCE_ID` | `""` | `restart` | service.instance.id resource attr; empty => host name. Set via env, e.g. TS2OTEL_SELF_OBSERVABILITY__INSTANCE_ID=$POD_NAME |
| `TS2OTEL_ADMIN__ENABLED` | `true` | `restart` | run the admin HTTP server (probes + status page + optional pprof mount) |
| `TS2OTEL_ADMIN__LISTEN` | `127.0.0.1:9091` | `restart` | serves /healthz, /readyz, and the status page. Loopback by default: the status page is REFUSED (403) on a network-reachable bind without admin.auth.token, so widen this only together with a token |
| `TS2OTEL_ADMIN__LANDING_PAGE` | `true` | `restart` | serve the human status page at / and machine-readable /api/status.json |
| `TS2OTEL_ADMIN__STATUS_REFRESH_INTERVAL` | `5s` | `restart` | how often the status page re-polls /api/status.json (1s freshness ticker is independent) |
| `TS2OTEL_ADMIN__SUPPORT_BUNDLE_LOG_TAIL_RECORDS` | `200` | `restart` | bounded redaction-safe recent log records included in support bundles; 0 disables capture |
| `TS2OTEL_ADMIN__AUTH__TOKEN` | `""` | `restart` | gate the status page + pprof behind this token (set via TS2OTEL_ADMIN__AUTH__TOKEN); empty is allowed only on a loopback listen — on any other bind the status page + JSON APIs are REFUSED with 403 (/healthz and /readyz stay open) |
| `TS2OTEL_ADMIN__AUTH__TOKEN_FILE` | `""` | `file_content` | read the value from this file instead (Docker secrets); set the value or the file, not both; content is whitespace-trimmed |
| `TS2OTEL_ADMIN__AUTH__FAILURE_LIMIT` | `5` | `restart` | failures from one source inside failure_window before throttling; 0 disables |
| `TS2OTEL_ADMIN__AUTH__FAILURE_WINDOW` | `1m` | `restart` | rolling window for failed authentication attempts |
| `TS2OTEL_ADMIN__AUTH__FAILURE_BACKOFF` | `30s` | `restart` | throttle duration after the per-source limit is reached |
| `TS2OTEL_ADMIN__TLS__CERT_FILE` | `""` | `file_content` | serve the admin listener over HTTPS instead of plain HTTP; set together with key_file (both-or-neither) |
| `TS2OTEL_ADMIN__TLS__KEY_FILE` | `""` | `file_content` | HTTPS key for admin.tls.cert_file |
| `TS2OTEL_ADMIN__TLS__CLIENT_CA_FILE` | `""` | `file_content` | require a client certificate signed by this CA; needs cert_file/key_file |
| `TS2OTEL_ADMIN__TLS__CLIENT_AUTH` | `""` | `restart` | require_and_verify by default when client_ca_file is set; same modes as prometheus.tls |
| `TS2OTEL_FLOWS__ENABLED` | `true` | `restart` | keep a bounded, in-memory picture of recent traffic and serve /flows; needs admin.enabled + admin.landing_page (no effect otherwise) |
| `TS2OTEL_FLOWS__RETENTION` | `6h` | `restart` | how far back /flows can see, as one-minute buckets (1m–24h). Memory scales with this, and with the number of tailnets in multi-tailnet mode. Lost on restart — OTLP stays the system of record |
| `TS2OTEL_FLOWS__MAX_FUTURE_SKEW` | `5m` | `restart` | local-view admission only: reject records further ahead of this process clock (0–1h); OTLP emission is unchanged |
| `TS2OTEL_FLOWS__CAPACITY_PROFILE` | `default` | `restart` | trade memory for fidelity on every per-bucket dimension + the raw-connection ring: compact (~half), default (unchanged), or expanded (~double). Fixed, hard-coded presets only — never a raw number |
| `TS2OTEL_FLOWS__STORE__DIRECTORY` | `""` | `restart` | OPTIONAL, opt-in: a directory for the on-disk /flows backend (internal/flowstore/sqlitestore). Empty (default) is memory-only. Relative paths resolve against this config file, like ingress_wal.directory. Setting this writes flow rows, including user identities, to disk — they then survive restarts and land in backups |
| `TS2OTEL_FLOWS__STORE__RETENTION` | `720h` | `restart` | how far back the on-disk store keeps rows (1h–8760h/365d), independent of flows.retention which still only sizes the in-memory ring. Only takes effect once directory is set |
| `TS2OTEL_FLOWS__STORE__MAX_ROWS` | `5000000` | `restart` | hard cap on retained rows (10000–1000000000), enforced independently of retention so a traffic flood cannot fill the disk before the next sweep. Only takes effect once directory is set |
| `TS2OTEL_FLOWS__STORE__MAX_EXPORT_ROWS` | `50000` | `restart` | cap on rows a single CSV/JSON export may read (100–1000000), so a large window cannot be materialized into memory in one request. Only takes effect once directory is set |
| `TS2OTEL_FLOWS__STORE__QUEUE_SIZE` | `8192` | `restart` | bound on the write-behind queue between Record and the disk writer (64–1048576); a full queue drops and counts rather than blocking. Only takes effect once directory is set |
| `TS2OTEL_FLOWS__STORE__BATCH_SIZE` | `512` | `restart` | rows committed per write transaction (1–100000); must not exceed queue_size. Only takes effect once directory is set |
| `TS2OTEL_FLOWS__STORE__FLUSH_INTERVAL` | `5s` | `restart` | force a partial batch to disk on this timer (100ms–5m) so a quiet tailnet's last few rows do not sit in memory indefinitely. Only takes effect once directory is set |
| `TS2OTEL_FLOWS__STORE__QUERY_TIMEOUT` | `15s` | `restart` | give up a single read against the store after this long (1s–5m) rather than hang the admin page. Only takes effect once directory is set |
| `TS2OTEL_FLOWS__STORE__SWEEP_INTERVAL` | `1h` | `restart` | how often retention and the row cap are enforced (1m–24h). Only takes effect once directory is set |
| `TS2OTEL_FLOWS__STORE__INCREMENTAL_VACUUM_INTERVAL` | `0s` | `restart` | periodic SQLite page reclamation; 0 inherits sweep_interval |
| `TS2OTEL_FLOWS__STORE__INCREMENTAL_VACUUM_PAGES` | `1000` | `restart` | maximum pages reclaimed per vacuum tick |
| `TS2OTEL_EVENTS__ENABLED` | `true` | `restart` | keep a bounded, in-memory ring of recent audit/webhook events and serve /events; needs admin.enabled + admin.landing_page (no effect otherwise) |
| `TS2OTEL_EVENTS__MAX_EVENTS` | `5000` | `restart` | how many individual events /events can see (100–100000). A plain count, not a time span — oldest evicted first. Lost on restart — OTLP stays the system of record |
| `TS2OTEL_PROMETHEUS__ENABLED` | `false` | `restart` | backwards-compatible pull opt-in alongside OTLP; delivery.mode prometheus or dual also enables it |
| `TS2OTEL_PROMETHEUS__LISTEN` | `127.0.0.1:2112` | `restart` | bind for /metrics (default loopback-only 127.0.0.1:2112); keep distinct from admin.listen |
| `TS2OTEL_PROMETHEUS__AUTH__TOKEN` | `""` | `restart` | gate /metrics behind this token (Bearer or Basic password); empty + a network bind = REFUSED 403 unless allow_unauthenticated. Set via TS2OTEL_PROMETHEUS__AUTH__TOKEN |
| `TS2OTEL_PROMETHEUS__AUTH__TOKEN_FILE` | `""` | `file_content` | read the value from this file instead (Docker secrets); set the value or the file, not both; content is whitespace-trimmed |
| `TS2OTEL_PROMETHEUS__AUTH__ALLOW_UNAUTHENTICATED` | `false` | `restart` | acknowledge serving /metrics with NO token on a network-reachable bind (e.g. in-cluster scraping behind a NetworkPolicy); a loopback bind never needs this |
| `TS2OTEL_PROMETHEUS__MAX_REQUESTS_IN_FLIGHT` | `4` | `restart` | cap concurrent /metrics gathers (excess gets 503); must be positive while Prometheus is enabled |
| `TS2OTEL_PROMETHEUS__TIMEOUT` | `8s` | `restart` | give up on a single /metrics gather after this long (503); keep below the scraper's own timeout |
| `TS2OTEL_PROMETHEUS__COALESCE_GATHER` | `true` | `restart` | serve overlapping scrapes from the same in-flight gather (bounds duplicate work; costs slight staleness) |
| `TS2OTEL_PROMETHEUS__TLS__CERT_FILE` | `""` | `file_content` | serve the Prometheus /metrics listener over HTTPS instead of plain HTTP; set together with key_file (both-or-neither) |
| `TS2OTEL_PROMETHEUS__TLS__KEY_FILE` | `""` | `file_content` | HTTPS key for prometheus.tls.cert_file |
| `TS2OTEL_PROMETHEUS__TLS__CLIENT_CA_FILE` | `""` | `restart` | require scrapers to present a client certificate signed by this CA (mTLS); needs cert_file/key_file, composes with auth.token |
| `TS2OTEL_PROMETHEUS__TLS__CLIENT_AUTH` | `""` | `restart` | how hard to check the client cert: require_and_verify (default when client_ca_file set)\|verify_if_given\|require\|request\|none |
| `TS2OTEL_PROFILING__PPROF__ENABLED` | `false` | `restart` | mount net/http/pprof on the admin server (REQUIRES admin.enabled + admin.auth.token — heap dumps can expose in-memory secrets) |
| `TS2OTEL_PROFILING__PYROSCOPE__ENABLED` | `false` | `restart` | run the Pyroscope continuous-profiling push agent |
| `TS2OTEL_PROFILING__PYROSCOPE__SERVER_ADDRESS` | `""` | `restart` | REQUIRED when enabled, e.g. http://pyroscope:4040 or https://profiles-prod-NNN.grafana.net |
| `TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_USER` | `""` | `restart` | Grafana Cloud: the profiles instance ID (set via TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_USER) |
| `TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_PASSWORD` | `""` | `restart` | Grafana Cloud: a profiles:write access-policy token (..._BASIC_AUTH_PASSWORD) |
| `TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_PASSWORD_FILE` | `""` | `restart` | read the value from this file instead (Docker secrets); set the value or the file, not both; content is whitespace-trimmed |
| `TS2OTEL_PROFILING__PYROSCOPE__TENANT_ID` | `""` | `restart` | X-Scope-OrgID for multi-tenant servers (leave empty for Grafana Cloud) |
| `TS2OTEL_PROFILING__PYROSCOPE__UPLOAD_RATE` | `60s` | `restart` | how often profiles are flushed |
| `TS2OTEL_PROFILING__PYROSCOPE__TLS__INSECURE_SKIP_VERIFY` | `false` | `restart` | keep TLS but skip server-certificate verification — a footgun; prefer ca_file |
| `TS2OTEL_PROFILING__PYROSCOPE__TLS__CA_FILE` | `""` | `file_content` | PEM bundle of the CA to trust for the profiles endpoint (private CA / self-signed gateway) |
| `TS2OTEL_PROFILING__PYROSCOPE__TLS__CERT_FILE` | `""` | `file_content` | client certificate for mTLS to the profiles endpoint; set together with key_file (both-or-neither) |
| `TS2OTEL_PROFILING__PYROSCOPE__TLS__KEY_FILE` | `""` | `file_content` | client key for profiling.pyroscope.tls.cert_file |
| `TS2OTEL_PROFILING__PYROSCOPE__TAILNET_LABEL` | `off` | `restart` | off \| hashed \| name — whether continuous profiles carry a tailnet dimension. A tailnet name is a CUSTOMER identifier and profiles go to a different destination from metrics/logs, so this is opt-in and NOT covered by pii_filter. hashed = a stable 12-hex SHA-256 prefix (answers "which tenant is burning CPU" for an MSP without shipping the name; pseudonymous, not anonymous — a small name space is enumerable). Emitted only for a single configured tailnet; multi-tailnet gets no tag, since there is one profiler per process. TS2OTEL_PROFILING__PYROSCOPE__TAILNET_LABEL |
| `TS2OTEL_PROFILING__PYROSCOPE__SPAN_PROFILES__ENABLED` | `false` | `restart` | REQUIRES tracing.enabled AND profiling.pyroscope.enabled. CPU profiles ONLY — Go attaches pprof labels to CPU samples, so heap/mutex/block/goroutine profiles cannot carry span identity |
| `TS2OTEL_PROFILING__PYROSCOPE__CREDENTIAL_RELOAD__ENABLED` | `false` | `restart` | governs the background poller only |
| `TS2OTEL_PROFILING__PYROSCOPE__CREDENTIAL_RELOAD__INTERVAL` | `30s` | `restart` | poll period; minimum 5s. Ignored when enabled is false |
| `TS2OTEL_PROFILING__MUTEX_PROFILE_FRACTION` | `5` | `restart` | runtime.SetMutexProfileFraction; on by default (applied only when pprof or pyroscope is enabled), 0 = disabled |
| `TS2OTEL_PROFILING__BLOCK_PROFILE_RATE` | `100000` | `restart` | runtime.SetBlockProfileRate (ns); on by default (100µs), 0 = disabled |
| `TS2OTEL_TRACING__ENABLED` | `false` | `restart` | emit spans. TS2OTEL_TRACING__ENABLED |
| `TS2OTEL_TRACING__SAMPLER` | `parentbased_always_on` | `restart` | head sampler: always_on\|always_off\|traceidratio\|parentbased_always_on\|parentbased_traceidratio. TS2OTEL_TRACING__SAMPLER |
| `TS2OTEL_TRACING__SAMPLER_ARG` | `1.0` | `restart` | sample ratio in [0,1] for the *traceidratio samplers (ignored otherwise). TS2OTEL_TRACING__SAMPLER_ARG |
| `TS2OTEL_TRACING__REMOTE_PARENT` | `trust` | `restart` | how an INBOUND traceparent's sampled bit is treated by the stream/webhook receivers: trust (today's behavior) \| ignore (the local sampler alone decides, so a sender cannot force sampling) \| link (start a new local root trace and link the remote one). TS2OTEL_TRACING__REMOTE_PARENT |
| `TS2OTEL_TRACING__SAMPLERS__SCRAPE__SAMPLER` | `""` | `restart` | same enum as tracing.sampler. TS2OTEL_TRACING__SAMPLERS__SCRAPE__SAMPLER |
| `TS2OTEL_TRACING__SAMPLERS__SCRAPE__ARG` | `0.0` | `restart` | ratio in [0,1] for the *traceidratio samplers. TS2OTEL_TRACING__SAMPLERS__SCRAPE__ARG |
| `TS2OTEL_TRACING__SAMPLERS__RECEIVER__SAMPLER` | `""` | `restart` | TS2OTEL_TRACING__SAMPLERS__RECEIVER__SAMPLER |
| `TS2OTEL_TRACING__SAMPLERS__RECEIVER__ARG` | `0.0` | `restart` | TS2OTEL_TRACING__SAMPLERS__RECEIVER__ARG |
| `TS2OTEL_TRACING__SAMPLERS__BACKGROUND__SAMPLER` | `""` | `restart` | TS2OTEL_TRACING__SAMPLERS__BACKGROUND__SAMPLER |
| `TS2OTEL_TRACING__SAMPLERS__BACKGROUND__ARG` | `0.0` | `restart` | TS2OTEL_TRACING__SAMPLERS__BACKGROUND__ARG |
| `TS2OTEL_RESOURCE__SERVICE_NAMESPACE` | `""` | `restart` | service.namespace — promoted to a job-adjacent LABEL on every series. Keep it low-cardinality and stable across deploys. TS2OTEL_RESOURCE__SERVICE_NAMESPACE |
| `TS2OTEL_RESOURCE__DEPLOYMENT_ENVIRONMENT` | `""` | `restart` | deployment.environment.name — outside service.*, so it lands in target_info only and may vary per environment. TS2OTEL_RESOURCE__DEPLOYMENT_ENVIRONMENT |
| `TS2OTEL_RESOURCE__FROM_ENV` | `false` | `restart` | also read OTEL_RESOURCE_ATTRIBUTES / OTEL_SERVICE_NAME, filtered by the same rules. Off by default: it hands the ambient environment a channel onto a per-series label surface. TS2OTEL_RESOURCE__FROM_ENV |
| `TS2OTEL_VERSION_CHECKS__SELF__ENABLED` | `true` | `restart` | emit tailscale2otel.update_available (running build vs latest tailscale2otel GitHub release) |
| `TS2OTEL_VERSION_CHECKS__DEVICES__ENABLED` | `true` | `restart` | emit per-device tailscale.device.version_skew + fleet rollups (device client version vs latest Tailscale stable). Needs the devices collector. |
| `TS2OTEL_VERSION_CHECKS__DEVICES__OUTDATED_MINOR_THRESHOLD` | `3` | `restart` | a device this many minor releases behind counts toward tailscale.devices.outdated |
| `TS2OTEL_VERSION_CHECKS__CACHE_TTL` | `1h` | `restart` | how long a fetched "latest version" is cached before re-fetching (minimum 5m) |
| `TS2OTEL_VERSION_CHECKS__TIMEOUT` | `10s` | `restart` | per-request timeout for the external version fetch |
| `TS2OTEL_GRAFANA_ANNOTATIONS__URL` | `""` | `restart` | Grafana base URL, e.g. https://mystack.grafana.net. Setting it IS the opt-in; empty = feature off entirely. TS2OTEL_GRAFANA_ANNOTATIONS__URL |
| `TS2OTEL_GRAFANA_ANNOTATIONS__TOKEN` | `""` | `restart` | Grafana service-account token (needs annotations:create and nothing else). Keep it in env/file, never in YAML. TS2OTEL_GRAFANA_ANNOTATIONS__TOKEN |
| `TS2OTEL_GRAFANA_ANNOTATIONS__TOKEN_FILE` | `""` | `restart` | path to a file holding the token (Docker/k8s secret mount). Value XOR file — setting both is a config error. TS2OTEL_GRAFANA_ANNOTATIONS__TOKEN_FILE |
| `TS2OTEL_GRAFANA_ANNOTATIONS__DASHBOARD_UID` | `""` | `restart` | confine annotations to ONE dashboard. Empty (default) writes organization annotations, visible on every board and in Explore — which is the point of pushing them. TS2OTEL_GRAFANA_ANNOTATIONS__DASHBOARD_UID |
| `TS2OTEL_GRAFANA_ANNOTATIONS__TIMEOUT` | `10s` | `restart` | per-request timeout for POST /api/annotations. TS2OTEL_GRAFANA_ANNOTATIONS__TIMEOUT |
| `TS2OTEL_GRAFANA_ANNOTATIONS__MAX_PER_MINUTE` | `60` | `restart` | token-bucket CEILING on annotations written per process. Overage is dropped and counted, never delayed — a marker arriving after the moment it explains is worse than absent. TS2OTEL_GRAFANA_ANNOTATIONS__MAX_PER_MINUTE |
| `TS2OTEL_GRAFANA_ANNOTATIONS__QUEUE_SIZE` | `512` | `restart` | hand-off buffer between the collector goroutines and the single publisher. A full queue drops and counts rather than blocking collection. TS2OTEL_GRAFANA_ANNOTATIONS__QUEUE_SIZE |
| `TS2OTEL_GRAFANA_ANNOTATIONS__ROLLUP_INTERVAL` | `5m` | `restart` | bucket width for rolled-up categories: one region annotation per interval per category per tailnet, instead of one marker per event. TS2OTEL_GRAFANA_ANNOTATIONS__ROLLUP_INTERVAL |
| `TS2OTEL_GRAFANA_ANNOTATIONS__DEDUPE_RETENTION` | `48h` | `restart` | how long a published annotation's dedupe key is remembered, so a restart cannot republish it. Must comfortably exceed the longest source overlap window. TS2OTEL_GRAFANA_ANNOTATIONS__DEDUPE_RETENTION |
| `TS2OTEL_GRAFANA_ANNOTATIONS__STATE_FILE` | `""` | `restart` | where the dedupe set persists. Empty = annotations.json beside checkpoint.file_path. Deliberately NOT inside the checkpoint file, which the window pollers rewrite every tick. TS2OTEL_GRAFANA_ANNOTATIONS__STATE_FILE |
| `TS2OTEL_GRAFANA_ANNOTATIONS__EXTRA_TAGS` | `[]` | `restart` | extra tags added to every annotation, e.g. [env:prod]. Every annotation already carries tailscale2otel, category:<c> and rule:<id>. TS2OTEL_GRAFANA_ANNOTATIONS__EXTRA_TAGS _(comma-separated list)_ |
| `TS2OTEL_GRAFANA_ANNOTATIONS__CATEGORIES__CONFIG_CHANGE__ENABLED` | `true` | `restart` | ACL edits, device approval and churn, key lifecycle, user role changes, DNS and tailnet settings — the curated audit-log subset. Needs collectors.auditlogs. TS2OTEL_GRAFANA_ANNOTATIONS__CATEGORIES__CONFIG_CHANGE__ENABLED |
| `TS2OTEL_GRAFANA_ANNOTATIONS__CATEGORIES__CONFIG_CHANGE__ROLLUP` | `true` | `restart` | highest-volume category: rolled up by default so a busy tailnet draws a timeline rather than a picket fence. TS2OTEL_GRAFANA_ANNOTATIONS__CATEGORIES__CONFIG_CHANGE__ROLLUP |
| `TS2OTEL_GRAFANA_ANNOTATIONS__CATEGORIES__EXPIRY__ENABLED` | `true` | `restart` | a node key or auth key entering its expiry warning window — the marker that explains a device count stepping down. Needs collectors.keys / collectors.devices. TS2OTEL_GRAFANA_ANNOTATIONS__CATEGORIES__EXPIRY__ENABLED |
| `TS2OTEL_GRAFANA_ANNOTATIONS__CATEGORIES__EXPIRY__ROLLUP` | `true` | `restart` | a fresh deployment finds every currently-expiring key at once, and one summary beats fifty markers at the same instant. TS2OTEL_GRAFANA_ANNOTATIONS__CATEGORIES__EXPIRY__ROLLUP |
| `TS2OTEL_GRAFANA_ANNOTATIONS__CATEGORIES__POLICY_CHANGE__ENABLED` | `true` | `restart` | ACL revision and policy-diff markers from the policy snapshot family. TS2OTEL_GRAFANA_ANNOTATIONS__CATEGORIES__POLICY_CHANGE__ENABLED |
| `TS2OTEL_GRAFANA_ANNOTATIONS__CATEGORIES__POLICY_CHANGE__ROLLUP` | `false` | `restart` | policy changes are rare and individually meaningful. TS2OTEL_GRAFANA_ANNOTATIONS__CATEGORIES__POLICY_CHANGE__ROLLUP |
| `TS2OTEL_GRAFANA_ANNOTATIONS__CATEGORIES__INVENTORY__ENABLED` | `true` | `restart` | device additions, removals and material field changes. TS2OTEL_GRAFANA_ANNOTATIONS__CATEGORIES__INVENTORY__ENABLED |
| `TS2OTEL_GRAFANA_ANNOTATIONS__CATEGORIES__INVENTORY__ROLLUP` | `true` | `restart` | device churn can be high-volume; summarize it into one region per interval. TS2OTEL_GRAFANA_ANNOTATIONS__CATEGORIES__INVENTORY__ROLLUP |
| `TS2OTEL_GRAFANA_ANNOTATIONS__CATEGORIES__RISK__ENABLED` | `true` | `restart` | newly observed ACL, SSH and auto-approver risk findings. TS2OTEL_GRAFANA_ANNOTATIONS__CATEGORIES__RISK__ENABLED |
| `TS2OTEL_GRAFANA_ANNOTATIONS__CATEGORIES__RISK__ROLLUP` | `false` | `restart` | each new risk finding remains individually visible. TS2OTEL_GRAFANA_ANNOTATIONS__CATEGORIES__RISK__ROLLUP |

**File-only** — these take structured values (a map or a list of objects) and must be set in the YAML config, not via an environment variable: `tailnets` (`restart`), `otlp.headers` (`restart`), `otlp.metrics.headers` (`restart`), `otlp.logs.headers` (`restart`), `otlp.traces.headers` (`restart`), `collectors.devices.posture_compliance_checks` (`restart`), `collectors.node_metrics.targets` (`restart`), `collectors.node_metrics.discovery.port_overrides` (`restart`), `streaming.routes` (`restart`), `webhook.routes` (`restart`), `profiling.pyroscope.tags` (`restart`), `profiling.pyroscope.headers` (`restart`), `resource.attributes` (`restart`).

<!-- END GENERATED: env-vars -->
