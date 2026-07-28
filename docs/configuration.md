---
title: Configuration
description: Full key-by-key configuration reference for tailscale2otel — layered defaults, YAML, and TS2OTEL_* environment variables
---

# Configuration Reference

This is the exhaustive, per-key reference for `tailscale2otel` configuration. It is the companion to
two other docs:

- **[`config.example.yaml`](https://github.com/rknightion/tailscale2otel/blob/main/config.example.yaml)** — a commented starter showing the common knobs.
  The fastest way to get started.
- **[`docs/metrics.md`](./metrics.md)** — every metric and log signal the exporter emits (and the
  OTLP→Prometheus name normalization you query in Grafana Cloud).

Use this page when you need the precise meaning, default, valid values, and gotchas of a specific
setting.

> This file is **hand-maintained** (unlike `docs/metrics.md`, which is generated). If you change the
> config schema in `internal/config/`, update this page too.

## Layered configuration

Configuration is loaded in three layers, lowest precedence first:

1. **Built-in defaults** — the exporter runs without a config file; any key you do not set keeps its
   default (defined in [`internal/config/defaults.go`](https://github.com/rknightion/tailscale2otel/blob/main/internal/config/defaults.go)).
2. **YAML file** (optional) — pass `-config path/to/file.yaml`; the file overrides defaults for any
   key it mentions. A non-existent path passed with `-config` is an error; omitting `-config`
   entirely is not.
3. **Environment variables** — highest precedence; override both defaults and the file.

## Environment-variable convention

Every config field is settable via an environment variable:

- **Prefix:** `TS2OTEL_`
- **Nesting delimiter:** `__` (double underscore) between levels
- **Within a name:** single underscores are preserved (e.g. `client_id` stays `CLIENT_ID`)

> For the **complete, generated list** of every `TS2OTEL_*` variable with its default and
> description, see [`env-vars.md`](env-vars.md). The samples below just illustrate the rule.

### Mapping examples

| Config key | Environment variable |
|---|---|
| `tailscale.auth.oauth.client_id` | `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID` |
| `tailscale.auth.oauth.client_secret` | `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET` |
| `tailscale.auth.apikey` | `TS2OTEL_TAILSCALE__AUTH__APIKEY` |
| `otlp.endpoint` | `TS2OTEL_OTLP__ENDPOINT` |
| `otlp.grafana_cloud.token` | `TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN` |
| `collectors.flowlogs.interval` | `TS2OTEL_COLLECTORS__FLOWLOGS__INTERVAL` |
| `collectors.flowlogs.source` | `TS2OTEL_COLLECTORS__FLOWLOGS__SOURCE` |
| `streaming.token` | `TS2OTEL_STREAMING__TOKEN` |
| `webhook.secret` | `TS2OTEL_WEBHOOK__SECRET` |
| `admin.auth.token` | `TS2OTEL_ADMIN__AUTH__TOKEN` |
| `prometheus.auth.token` | `TS2OTEL_PROMETHEUS__AUTH__TOKEN` |
| `prometheus.auth.token_file` | `""` | Read `prometheus.auth.token` from a file at startup instead of a literal value (Docker-secrets style). Setting both the value and the file is a config error. File content is whitespace-trimmed. |
| `prometheus.tls.cert_file` | `""` | HTTPS certificate for the Prometheus pull endpoint. Set together with `key_file` (both-or-neither); unset serves plain HTTP. |
| `prometheus.tls.key_file` | `""` | HTTPS key for `prometheus.tls.cert_file`. Both paths must exist and be readable at startup. |
| `self_observability.instance_id` | `TS2OTEL_SELF_OBSERVABILITY__INSTANCE_ID` |
| `profiling.pyroscope.basic_auth_password` | `TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_PASSWORD` |
| `profiling.pyroscope.basic_auth_password_file` | `""` | Read `profiling.pyroscope.basic_auth_password` from a file at startup instead of a literal value (Docker-secrets style). Setting both the value and the file is a config error. File content is whitespace-trimmed. |

### Scalar lists

Fields whose type is a list of strings accept a **comma-separated value** as an env var. Examples:

```sh
TS2OTEL_TAILSCALE__AUTH__OAUTH__SCOPES=all:read,log_streaming
TS2OTEL_COLLECTORS__NODE_METRICS__METRIC_ALLOW=tailscaled_inbound.*,tailscaled_outbound.*
TS2OTEL_COLLECTORS__NODE_METRICS__DROP_LABELS=job,prometheus_replica
TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__INCLUDE_TAGS=tag:server,tag:relay
TS2OTEL_COLLECTORS__DEVICES__ATTRIBUTE_NAMESPACES=intune,jamf,ip
```

### File-only fields

These fields cannot be set via flat env vars because they are maps or lists of structs:

- `otlp.headers` — use the YAML file (or use `otlp.grafana_cloud` for Grafana Cloud).
- `collectors.node_metrics.targets` — each target is a struct; static targets require a YAML file.
- `profiling.pyroscope.tags` — a string→string map; set via YAML.

### Unknown-variable advisory

A `TS2OTEL_*` env var that does not match any known config key is logged at startup as a **WARN** —
this almost always means a typo in the variable name. The exporter still starts; the variable is
ignored.

### Unknown YAML keys are a hard error

Unlike an unknown env var, an unrecognized key in the YAML **config file** fails `Load` outright
(`log_leevl: debug` or `collectors.devices.intervaal: 30s` refuse to start rather than being
silently ignored) — the error names the full dotted key path and, when a close match exists,
suggests it. Keys under a genuinely dynamic map (`otlp.headers.*`, a node-metrics target's
`headers`/`labels`) are always accepted.

!!! warning "Upgrading: a key that used to be ignored now stops startup"
    Before this change every unrecognized file key was silently dropped, so a config carrying a
    typo — or a key removed by an earlier release — started fine and quietly ran on defaults. Those
    same files now fail to load. That is the point (a setting that does nothing should not look
    like it does something), but it means an upgrade can fail at startup on a file that has "always
    worked". Run `tailscale2otel -config <file> -validate` before rolling out.

    A key this project **removed** is called out as removed rather than offered a spelling
    suggestion, because the nearest valid key is usually a different setting: for example
    `cardinality.flow.destination_service` (removed in 0.13.0) sits two edits from
    `cardinality.flow.destination_port`, and taking that suggestion would silently change your
    metric cardinality.

## Conventions

- **Default** is the value used when the key is not set in either the file or an env var.
- **Durations** use Go's syntax: `500ms`, `30s`, `5m`, `1h`, `168h` (= 7 days).
- **Validation** — invalid enum values and inconsistent combinations are rejected at startup by
  `Config.Validate()` (the exporter refuses to start). Softer issues are surfaced as startup
  **WARN** advisories by `Config.Warnings()` but do not block startup. Both are noted below.

## Contents

- [Top level](#top-level)
- [`headscale` — Headscale control-plane connection](#headscale-headscale-control-plane-connection)
- [`tailscale` — API connection & authentication](#tailscale-api-connection-authentication)
- [`tailnets` — multi-tailnet / MSP mode](#tailnets-multi-tailnet-msp-mode)
- [`otlp` — the OTLP exporter](#otlp-the-otlp-exporter)
- [`enrichment` — device-name cache](#enrichment-device-name-cache)
- [`cardinality` — metric/label cardinality controls](#cardinality-metriclabel-cardinality-controls)
- [`collectors` — per-source polling](#collectors-per-source-polling)
- [`checkpoint` — poll high-water marks](#checkpoint-poll-high-water-marks)
- [`ingress_wal` — durable local receiver acceptance](#ingress_wal-durable-local-receiver-acceptance)
- [`streaming` — Splunk-HEC log receiver](#streaming-splunk-hec-log-receiver)
- [`webhook` — event webhook receiver](#webhook-event-webhook-receiver)
- [`self_observability` — the exporter's own telemetry](#self_observability-the-exporters-own-telemetry)
- [`pii_filter` — PII / identifier redaction](#pii_filter-pii-identifier-redaction)
- [`admin` — admin HTTP server (probes + status page)](#admin-admin-http-server-probes-status-page)
- [`prometheus` — Prometheus pull endpoint](#prometheus-prometheus-pull-endpoint)
- [`profiling` — pprof & Pyroscope](#profiling-pprof-pyroscope)
- [`version_checks` — outbound "is a newer release available?" checks](#version_checks-outbound-is-a-newer-release-available-checks)
- [`tracing` — OTEL traces pillar](#tracing-otel-traces-pillar)

---

## Top level

| Key | Default | Description |
|-----|---------|-------------|
| `log_level` | `info` | Logging verbosity. One of `debug`, `info`, `warn`, `error`. |
| `provider` | `tailscale` | Control-plane backend. One of `tailscale` (default, fully back-compatible) or `headscale`. |

---

## `headscale` — Headscale control-plane connection

Used only when `provider: headscale`. Auth is a Bearer API key; keep it in an environment variable
(`TS2OTEL_HEADSCALE__API_KEY`), not in the YAML file.

Under `provider: headscale` only the `devices`, `users`, `keys`, `acl`, and `nodemetrics` collectors
run. The Tailscale-only collectors (`flowlogs`, `auditlogs`, `services`, `webhooks`, `contacts`,
`posture_integrations`, `log_stream`, `oauth_apps`, `settings`, `dns`) auto-disable; enabling them explicitly triggers
a startup warning.

**Reduced device signal set.** Headscale's API exposes fewer device fields than Tailscale, so under
`provider: headscale` the `devices` collector emits a *subset* of its usual signals — online status,
advertised/enabled routes (exit-node and subnet-router derivations still work), key expiry, last-seen,
and tag/user counts. Two booleans that Tailscale devices carry with no Headscale equivalent are
defaulted to the only value that could ever be correct, rather than treated as missing: `authorized`
(every node Headscale returns is registered, hence authorized, by definition) and `external` (Headscale
has no device-sharing feature, so no node it returns can ever be "external"). By contrast, the
following are genuine **no-data** gaps — the source fields are absent, so the affected signals are
**not emitted at all** rather than reporting a fabricated zero/false: per-DERP-region latency, posture
and posture attributes, tailnet-lock, `tailscale.device.update_available`, `tailscale.devices.ephemeral`,
OS/version distribution, and connectivity quality. Likewise device share-invites and user-invites are
unavailable.

**Reduced user signal set.** Headscale's user API has no per-user device-count or connection-state
concept, so `tailscale.user.devices` and `tailscale.user.connected` are **not emitted** under
`provider: headscale` (rather than reporting a fabricated 0/not-connected). `tailscale.user.last_seen`
and the aggregate `tailscale.users.count` are unaffected.

**Spent one-time pre-auth keys.** Headscale reports whether a non-reusable pre-auth key has already
been redeemed (`used`). A used one-time key is mapped to the same "invalid" state Tailscale's API uses
for a dead/revoked key, so it stops reporting a live `tailscale.key.expiry` gauge and can no longer
trigger the `tailscale.key.expiring` warning. Reusable keys are unaffected by use.

**Headscale server metrics.** Headscale also exposes its own Prometheus endpoint (the *control-plane
server*, default `:9090`) — distinct from per-node `tailscaled` `:5252`. Scrape it by adding it as a
static `node_metrics` target (see the `node_metrics` section); there is no dedicated knob for it.

| Key | Default | Description |
|-----|---------|-------------|
| `headscale.url` | `""` | Headscale control-plane base URL, e.g. `https://headscale.example.org`. Required when `provider: headscale`. Set via `TS2OTEL_HEADSCALE__URL`. |
| `headscale.api_key` | `""` | Bearer API key for the Headscale server. Required when `provider: headscale`. Set via `TS2OTEL_HEADSCALE__API_KEY`. |
| `headscale.api_key_file` | `""` | Read `headscale.api_key` from a file at startup instead of a literal value (Docker-secrets style). Setting both the value and the file is a config error. File content is whitespace-trimmed. |
| `headscale.max_response_bytes` | `4194304` (4 MiB) | Cap on ONE Headscale API response body before it is decoded. Must be `> 0`. Sized from a measured ~715 B/node, so the default covers roughly 5,800 nodes. These endpoints are **not paginated**, so a larger deployment needs a larger value — raise the container memory limit alongside it, since decoding costs several times the wire size. Above 64 MiB triggers a startup warning. The same fixed structural budgets as `tailscale.max_response_bytes` apply (nesting depth, string length, array elements). |
| `headscale.http.timeout` | `30s` | Per-request timeout for Headscale API calls. |
| `headscale.http.retry.max_attempts` | `0` | Accepted for config parity with `tailscale.http`, but **not applied** by the minimal v1 Headscale client (which honors only `timeout`). |
| `headscale.http.retry.base_delay` | `0s` | Accepted for parity; not applied in v1 (see above). |
| `headscale.http.retry.max_delay` | `0s` | Accepted for parity; not applied in v1 (see above). |
| `headscale.http.rate_limit` | `0` | Accepted for parity; not applied in v1 (see above). |

---

## `tailscale` — API connection & authentication

| Key | Default | Description |
|-----|---------|-------------|
| `tailscale.tailnet` | `-` | Your tailnet's name (e.g. `example.com`), or `-` (the default) for the authenticating principal's default tailnet — which works out of the box for a single-tailnet OAuth client. Set an explicit name only if the principal has access to multiple tailnets. |

### `tailscale.auth`

Prefer OAuth: its tokens are short-lived, auto-refreshing, and not bound to a user.

| Key | Default | Description |
|-----|---------|-------------|
| `tailscale.auth.method` | `oauth` | Authentication method. One of `oauth` (recommended), `apikey`, or `workload_identity` (fully keyless OIDC token exchange — no stored secret). |
| `tailscale.auth.oauth.client_id` | `""` | OAuth client ID. Required when `method: oauth`. Set via `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID`. |
| `tailscale.auth.oauth.client_secret` | `""` | OAuth client secret. Required when `method: oauth`. Set via `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET`. |
| `tailscale.auth.oauth.client_secret_file` | `""` | Read `tailscale.auth.oauth.client_secret` from a file at startup instead of a literal value (Docker-secrets style). Setting both the value and the file is a config error. File content is whitespace-trimmed. |
| `tailscale.auth.oauth.scopes` | `["all:read"]` | OAuth scopes requested for the token. Least-privilege read scopes are the default; add `log_streaming` if you use `streaming.auto_configure`. Comma-separated in env: `TS2OTEL_TAILSCALE__AUTH__OAUTH__SCOPES=all:read,log_streaming`. |
| `tailscale.auth.apikey` | `""` | Personal API key. Used **only** when `method: apikey`. Set via `TS2OTEL_TAILSCALE__AUTH__APIKEY`. |
| `tailscale.auth.apikey_file` | `""` | Read `tailscale.auth.apikey` from a file at startup instead of a literal value (Docker-secrets style). Setting both the value and the file is a config error. File content is whitespace-trimmed. |
| `tailscale.auth.workload_identity.client_id` | `""` | Federated OAuth client ID (workload identity federation). Required when `method: workload_identity`. Set via `TS2OTEL_TAILSCALE__AUTH__WORKLOAD_IDENTITY__CLIENT_ID`. |
| `tailscale.auth.workload_identity.id_token_file` | `""` | Path to the OIDC ID token (e.g. a Kubernetes projected service-account token) exchanged for a short-lived Tailscale API token. Re-read on every exchange, so in-place rotation just works. Scopes are fixed by the federated identity's admin-console configuration — there is no scopes field. |

> **WARN (advisory):** `method: apikey` triggers a startup warning — a personal API key expires in
> ≤90 days and stops working when the user who created it is suspended or removed. For an unattended
> exporter, prefer `method: oauth`.

### `tailscale.http`

The HTTP client used for all Tailscale API calls.

| Key | Default | Description |
|-----|---------|-------------|
| `tailscale.http.timeout` | `30s` | Per-attempt timeout for each Tailscale API call (connect + headers + body read). Retries and `Retry-After` backoff are NOT counted against it, so a retried request can exceed this; total attempts are bounded by `max_attempts`. |
| `tailscale.http.retry.max_attempts` | `4` | Maximum attempts per request (initial try + retries) under exponential backoff. |
| `tailscale.http.retry.base_delay` | `500ms` | Initial backoff delay. |
| `tailscale.http.retry.max_delay` | `10s` | Maximum backoff delay between retries. Also **caps a server-sent `Retry-After`**: a `429`/`503` carrying a longer `Retry-After` (numeric seconds or an HTTP date) waits at most `max_delay`, not the full server value, so an upstream cannot park a collector inside one request for hours. A `Retry-After` below `max_delay` is still honoured exactly. The wait counts toward `api.duration`, and request-context cancellation still interrupts it immediately. |
| `tailscale.http.rate_limit` | `0` | Global request rate cap in requests/second across **all** collectors. `0` = unlimited. |

### `tailscale` response-decode budgets

Caps on how large a single API response body may be before it is decoded. They exist so a malicious
or broken upstream (or a proxy in front of it) cannot stream an unbounded body into memory. Both are
**fleet-wide**: they apply to every `tailnets[]` entry, not per tailnet.

| Key | Default | Description |
|-----|---------|-------------|
| `tailscale.max_response_bytes` | `4194304` (4 MiB) | Cap on ONE snapshot-endpoint response body (devices, keys, dns, services, settings, posture, invites, …) before it is decoded. Must be `> 0`. Sized from a live capture at ~1.8 KiB/device, i.e. roughly 2,400 devices. These endpoints are **not paginated**, so a larger tailnet needs a larger value — raise the container memory limit alongside it, since decoding costs several times the wire size. |
| `tailscale.max_log_response_bytes` | `33554432` (32 MiB) | The same cap for the bulk log pulls (`logging/network`, `logging/configuration`), which are legitimately multi-MB: roughly 13,600 flow records at ~2.4 KiB each. Must be `> 0`. If you hit it, shorten the collector's poll window rather than raising this. |

A value above 64 MiB triggers a startup warning: decoding allocates several times the wire size, so a
budget that large can exceed a typical container memory limit before the cap ever engages.

> **Structural budgets are fixed and not configurable.** Alongside the byte caps, decoding is bounded
> by nesting depth (64), single-string length (4 MiB), and array elements per container (500,000).
> These bound a degenerate-but-valid body that would otherwise force a large allocation well before the
> byte ceiling is reached — `[0,0,0,…]` costs 2 bytes on the wire per element but roughly 16 decoded.
> Every limit is orders of magnitude above anything the real API emits (the deepest live payload
> measures 7 levels, the longest live string 645 bytes). Exceeding one is reported as a distinct error
> class from a byte-budget overrun, because the remedies differ: a too-large body may just be a big
> tailnet, whereas a too-complex one is not shaped like anything the Tailscale API produces.

> **A budget failure is not retried in a tight loop.** The limit is enforced while decoding a `200`
> response, after the HTTP round-trip has already returned, so it cannot drive the transport's retry
> chain. The collector re-polls on its normal interval instead.

> **Token fetches use the same timeout, end-to-end.** `tailscale.http.timeout` also bounds each OAuth
> client-credentials refresh and workload-identity token exchange — but there it covers the *whole* call
> (connect + headers + **body read**) with no retries and no backoff, unlike a normal API call where it
> bounds one attempt and `max_attempts` governs the retry chain. A token endpoint that sends valid
> headers and then stalls mid-body therefore fails within this timeout instead of hanging the refresh —
> and every collector queued behind that single shared refresh — indefinitely.

> **Cross-origin redirects are refused on credential-bearing requests.** Every authenticated call —
> API key, OAuth client-credentials, and workload-identity token exchange — is bound to the configured
> Tailscale origin. A redirect is followed only when its target is the exact same scheme, host, and
> port, with no injected userinfo; a scheme downgrade, an alternate port, and a subdomain all count as
> different origins. This stops an API key riding a redirect off-origin, and stops a `307`/`308`
> replaying the OAuth client secret or the projected workload-identity JWT in the POST body to another
> host. There is no allowlist knob, and the API origin is not configurable — it is always
> `https://api.tailscale.com`. A refusal is logged at ERROR with the diagnostic class `redirect_refused` and
> names the two origins only — never the credential, the body, or the full destination URL. Seeing it
> means a deliberate control fired, not a bug.

> **Tune `tailscale.http.timeout` together with `flowlogs`/`auditlogs` `max_window`.** After an outage,
> the next poll tick fetches and decodes a catch-up window as large as `max_window` in a single request.
> If streaming and decoding that much log data takes longer than `tailscale.http.timeout`, every attempt
> at that window fails identically (the checkpoint never advances — see the `max_window` field below) —
> a durable wedge that only clears with a config change or a smaller subsequent window. There is
> currently no automatic shrink-on-timeout: raise `tailscale.http.timeout` to comfortably cover decoding
> the largest configured `max_window` for your tailnet's flow/audit log volume, or lower `max_window` so
> a worst-case catch-up window reliably completes within the timeout.

> **Multi-tailnet:** the `tailscale.http` block is the **fleet-wide default** for every `tailnets[]`
> entry. Each entry's `http:` fields are backfilled field-by-field with the precedence *entry >
> `tailscale.http` > built-in defaults*, so an entry that omits `http:` still gets real retry/timeout
> defaults (a zero `max_attempts` would otherwise disable retries entirely), and setting a value once
> on `tailscale.http` — including via `TS2OTEL_TAILSCALE__HTTP__*` env vars — applies it to the whole
> list. An entry that sets its own `http:` field overrides the fleet default for that field only.

---

## `tailnets` — multi-tailnet / MSP mode

Optional list for observing **more than one tailnet from a single instance** (e.g. an MSP watching
several customer tailnets). Empty by default — an empty (or absent) `tailnets:` means the ordinary
single-tailnet `tailscale:` block above is used instead.

| Key | Default | Description |
|-----|---------|-------------|
| `tailnets` | `[]` | List of tailnet entries to fan out over. A non-empty list enables multi-tailnet mode. **File-only** — like `collectors.node_metrics.targets`, a list of structs cannot be set via flat `TS2OTEL_*` env vars; use a YAML config file. |
| `tailnets[].name` | — (required) | The tailnet's name (e.g. `acme.example.com`). Required, and must be unique within the list — a missing or duplicate name is rejected at startup. |
| `tailnets[].auth` | — | Same shape as [`tailscale.auth`](#tailscale-api-connection-authentication) (`method: oauth\|apikey` plus the matching `oauth`/`apikey` sub-fields). **Not inherited** from the top-level `tailscale.auth` — every entry is fully self-contained, including credentials. An entry with an invalid or missing `auth.method` is rejected at startup. |
| `tailnets[].http` | — | Same shape as [`tailscale.http`](#tailscalehttp). Unlike `auth`, this **is** backfilled field-by-field from the top-level `tailscale.http` block (itself defaulted), which is why `tailscale.http` doubles as the fleet-wide default for the whole list (see the note above). An entry that sets its own `http.*` field overrides the fleet default for that field only. |
| `tailnets[].objectstore.flow` | — | This tailnet's own flow-log export bucket. Same fields as [`collectors.flowlogs.objectstore`](#collectorsflowlogsobjectstore-the-s3-export-as-an-ingestion-source). Optional in general, **required on every entry** when `collectors.flowlogs.source: objectstore` — see the note below. |
| `tailnets[].objectstore.audit` | — | This tailnet's own **configuration**-log export bucket. Same fields again, and a destination of its own — never inherited from `objectstore.flow`. Optional in general, **required on every entry** when `collectors.auditlogs.source: objectstore`. |

> **Per-tailnet object-store destinations.** When a log collector's `source` is `objectstore` and a
> `tailnets:` list is present (**any** length, including one), each entry must carry its own complete
> destination for THAT signal — `objectstore.flow` for `collectors.flowlogs`, `objectstore.audit` for
> `collectors.auditlogs` — at minimum `endpoint`, `region` and `bucket`. The rules:
>
> - **No inheritance, no fallback.** Nothing is taken from `collectors.flowlogs.objectstore`; that
>   block is the destination for single-tailnet (no `tailnets:` list) mode only. An entry with no
>   destination of its own is a startup error naming the tailnet, never a silent fall-back to the
>   global block.
> - **No shared feeds — across tailnets OR across signals.** Any two destinations this process reads
>   whose normalized `endpoint` + `region` + `bucket` + `prefix` + `path_style` match are rejected at
>   startup, naming both. Two tailnets on one feed would each ingest every object and attribute a copy
>   to their own tailnet; two signals on one feed would each fetch every object and then fail to decode
>   the other's records. Give each one a distinct bucket, or a distinct `prefix` within one bucket (one
>   bucket with several prefixes is fine).
> - **Credentials are per entry and never cross runtimes.** Each entry's `access_key_id` /
>   `secret_access_key` / `session_token` are revealed only while that runtime's S3 client is built.
>   Because the list is file-only there is no `TS2OTEL_*` path into a list element, so a static
>   credential must come from the `*_file` sibling (a mounted Secret) or be left empty to use the
>   ambient chain (environment / IRSA / ECS-EKS container endpoint / instance profile) — which is the
>   same chain for every runtime,
>   so per-tailnet static credentials or per-tailnet roles are what actually separate access.
> - **Defaults.** Only the tuning fields (`interval`, `lookback`, `initial_lookback`, `max_objects`,
>   the `max_object_*` / `max_cycle_*` budgets) fall back to the built-in defaults, so an entry only
>   states what makes it different. Destination identity, `path_style`, `allow_insecure_http` and the
>   credentials are never defaulted — that fallback is exactly the inheritance the rules above forbid.
>   For a list entry, `0` reads as "unset" and takes the default; a negative value is still rejected.
> - **Source selection stays global per signal.** `collectors.flowlogs.source` and
>   `collectors.auditlogs.source` are each one value for the whole process; a runtime cannot poll one
>   signal while another runtime reads the same signal from a bucket. The two signals may differ from
>   each other.
> - **Checkpoint identity is the configured name.** The literal `tailnets[].name` keys the durable
>   `objectstore/v1/<tailnet>/…` namespace, including a literal `-`, so a resolved display name never
>   moves a runtime's state.
>
> **Migrating from a single global destination.** Moving from `tailscale:` +
> `collectors.flowlogs.objectstore` to a `tailnets:` list means copying that block under the entry as
> `objectstore.flow` (and giving each further tailnet its own bucket/prefix). Object-store checkpoints
> are keyed by tailnet, so the first tailnet keeps its own namespace only if its `tailnets[].name`
> equals the previous `tailscale.tailnet`; otherwise it cold-starts from `initial_lookback` and may
> re-ingest up to that window once.

> **Mutual exclusion with `tailscale.tailnet`.** `tailnets:` and an explicit `tailscale.tailnet` cannot
> both be set — a non-empty `tailnets` list alongside a `tailscale.tailnet` that names an actual
> tailnet is rejected at startup (the default `"-"` sentinel does not count as a conflict, since it's
> just "no explicit override"). Use one or the other, never both.
>
> **No inheritance of `tailscale.*` auth defaults.** Every `tailnets[]` entry needs its own `name` and
> `auth` — credentials are never inherited from the top-level `tailscale.auth` block (`http` is the one
> exception; see above). An `oauth` entry that omits `scopes` still gets the least-privilege default
> used everywhere else in this exporter: `["all:read"]` — never an unscoped token covering every scope
> the OAuth client holds.
>
> **Multi-tailnet receivers use explicit routes.** Set `streaming.routes[]` and/or `webhook.routes[]`
> in the YAML file; every route names exactly one `tailnets[].name`, so its request is routed to that
> runtime's processor, cache, emitter, token/secret, and cross-source dedup set. Route lists replace
> the legacy receiver identity fields and cannot be set through environment variables. A receiver
> without routes remains the compatible single-tailnet configuration.
>
> **Checkpoint namespacing.** Poll checkpoint keys are `<name>` (collector name only) in single-tailnet
> mode and `<tailnet>/<name>` in multi-tailnet mode. Switching between single- and multi-tailnet mode,
> or renaming/removing a tailnet, changes the key shape; the exporter migrates a matching legacy key
> automatically when exactly one unambiguous candidate exists, and otherwise leaves the stale key in
> the checkpoint file (logged) while the affected collector cold-starts from `initial_lookback`.
>
> **Telemetry identity.** Each tailnet gets its own `service.instance.id` (and thus its own
> `target_info`/Prometheus `instance`) plus the `tailscale.tailnet` resource attribute, so series from
> different tailnets never collide on the OTLP push path; query fleet-wide with
> `sum without(instance)(...)`. On the [`prometheus`](#prometheus-prometheus-pull-endpoint) pull
> endpoint, `tailscale_tailnet` is the label that keeps per-tailnet series distinct at the shared
> `/metrics` port — see the note in that section.

---

## `otlp` — the OTLP exporter

The single egress path for metrics and logs. `internal/telemetry` is the only component that touches
OTLP.

| Key | Default | Description |
|-----|---------|-------------|
| `otlp.protocol` | `http` | Transport. One of `grpc`, `http`, or `stdout`. `stdout` prints signals to the console for local debugging (no backend, no network). |
| `otlp.endpoint` | `https://otlp-gateway-prod-us-central-0.grafana.net/otlp` | OTLP endpoint (ignored when `protocol: stdout`). For `protocol: http` this is a full base **URL** — for Grafana Cloud use the `…/otlp` base and the per-signal `/v1/metrics` and `/v1/logs` paths are appended for you. For `protocol: grpc` it must instead be a bare **`host:port`** address (no scheme or path, e.g. `otlp-gateway-prod-us-central-0.grafana.net:443`); a URL-shaped value is rejected at startup. |
| `otlp.metric_interval` | `60s` | How often metrics are pushed. `60s` aligns with the default 1 data-point-per-minute scrape cadence and avoids Grafana Cloud DPM churn. |
| `otlp.metric_export_batch_size` | `10000` | Maximum datapoints per OTLP metric request. The metric SDK splits one cumulative collection into sequential requests at this boundary, preventing a single high-cardinality payload from blocking all metric delivery. This is not an exact byte limit: serialized size varies with metric names, labels, and values. Smaller values reduce request size at the cost of more requests per export interval. |
| `otlp.headers` | `{}` | Extra raw headers added to every OTLP request (an alternative to `grafana_cloud`). |

### `otlp.grafana_cloud`

Convenience for Grafana Cloud: when both are set, an `Authorization: Basic <base64(instance:token)>`
header is built for you (no need to hand-craft it in `otlp.headers`).

| Key | Default | Description |
|-----|---------|-------------|
| `otlp.grafana_cloud.instance_id` | `""` | Grafana Cloud OTLP instance/stack ID (the Basic-auth username). Set via `TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID`. |
| `otlp.grafana_cloud.token` | `""` | Grafana Cloud OTLP token (the Basic-auth password). Set via `TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN`. |
| `otlp.grafana_cloud.token_file` | `""` | Read `otlp.grafana_cloud.token` from a file at startup instead of a literal value (Docker-secrets style). Setting both the value and the file is a config error. File content is whitespace-trimmed. |

### `otlp.tls`

Transport security for `grpc`/`http`.

| Key | Default | Description |
|-----|---------|-------------|
| `otlp.tls.insecure` | `false` | Disable transport security **entirely** (plaintext transport) — this is **not** a certificate-verification skip. It applies after the endpoint scheme, so even an `https://`/gRPC-with-TLS endpoint is downgraded to plaintext when set. Because the exporter's `Authorization: Basic <instanceID:token>` header (built from `otlp.grafana_cloud`/`otlp.headers`) rides on whatever transport this selects, `insecure: true` sends that credential **unencrypted** on the wire. Use only for a trusted local Collector on a private/loopback link — never across an untrusted network. |
| `otlp.tls.ca_file` | `""` | Path to a CA bundle to trust for the server certificate. |
| `otlp.tls.insecure_skip_verify` | `false` | Keep TLS on but skip server-certificate verification (self-signed / private-CA OTLP gateways, testing only). Distinct from `insecure` (which disables TLS entirely). A footgun — prefer `ca_file` in production. |
| `otlp.tls.cert_file` | `""` | Client certificate (for mTLS). |
| `otlp.tls.key_file` | `""` | Client private key (for mTLS). |

> **`insecure` vs `insecure_skip_verify`.** `otlp.tls.insecure` disables TLS **entirely** (plaintext
> h2c / `http://`) — it is NOT a certificate-verify skip, and it sends any `Authorization: Basic`
> Grafana Cloud credential unencrypted. To reach an internal OTLP gateway with a self-signed / private-CA
> certificate over TLS, either add its CA to `otlp.tls.ca_file` (preferred) or set
> `otlp.tls.insecure_skip_verify: true` to keep TLS on while skipping verification (testing only —
> vulnerable to MITM).

---

## `enrichment` — device-name cache

The in-memory IP/nodeID→name cache, populated by the `devices` collector and used to enrich flow and
audit records.

| Key | Default | Description |
|-----|---------|-------------|
| `enrichment.cache_ttl` | `5m` | Staleness-alarm threshold for the device cache. If the cache hasn't refreshed within this window, a staleness signal is raised. |

> Enrichment depends on the `devices` collector. If `devices` is disabled, flow/audit IP→name
> resolution silently degrades to `unknown`/`external`.

### `enrichment.reverse_dns`

Optional async reverse-DNS (PTR) enrichment of external (non-Tailscale) flow addresses. Off by
default. When enabled, resolved hostnames replace the `external` bucket / raw IP in
`tailscale.src.node` / `tailscale.dst.node` on flow logs and metrics. Lookups are async and cached;
the hot path never blocks.

| Key | Default | Description |
|-----|---------|-------------|
| `enrichment.reverse_dns.enabled` | `false` | Turn on reverse-DNS enrichment of external flow addresses. |
| `enrichment.reverse_dns.server` | `""` | Resolver to query as `ip` or `ip:port` (default port 53). Empty = system resolver. |
| `enrichment.reverse_dns.timeout` | `2s` | Per-lookup timeout. |
| `enrichment.reverse_dns.cache_ttl` | `24h` | Positive-result cache TTL. |
| `enrichment.reverse_dns.negative_ttl` | `5m` | Failed-lookup cache TTL. |
| `enrichment.reverse_dns.max_entries` | `50000` | Cache size bound. |
| `enrichment.reverse_dns.acknowledge_cardinality` | `false` | Set `true` (once `cardinality.metric_limit` is sized) to silence the startup advisory that fires when reverse-DNS is enabled together with node-dimension flow labels. |

> **A cache "miss" does not always schedule a resolution.** The `tailscale.rdns.cache.lookups`
> metric's `miss` result covers every sighting that isn't a cached hit or cached negative — but a
> background resolution is only actually scheduled when the address isn't already in flight and the
> cache/worker pool has capacity. A repeat sighting of an address whose resolution is already pending,
> or one that arrives while the cache/worker pool is at capacity, still counts as a `miss` without
> issuing a new query. Don't alert on "misses without a matching query" as a resolver-health signal —
> that gap is expected under normal load, not a fault.

---

## `cardinality` — metric/label cardinality controls

These knobs trade detail for active-series count. They apply to the shared processors, so they take
effect no matter whether logs arrive by poll or by stream.

### Top-level cardinality keys

| Key | Default | Description |
|-----|---------|-------------|
| `cardinality.metric_limit` | `10000` | Hard per-instrument series cap. Beyond this the OTLP SDK collapses extra series into `otel_metric_overflow` (silent loss of detail). Size it above your busiest flow-metric cardinality. `0` or negative = unlimited. |
| `cardinality.derp_region_rollup` | `true` | Emit tailnet-wide per-DERP-region rollup gauges (`tailscale.derp.region.*`) from the devices collector. |
| `cardinality.subnet_route_rollup` | `true` | Emit the per-CIDR `tailscale.subnet_routes.routers` redundancy gauge (one series per subnet CIDR) from the devices collector. The fleet exit/subnet count aggregates emit regardless. |
| `cardinality.warning_threshold` | `2000` | The admin status page's cardinality view flags a source metric at/above this active-series count (self-observability only). `0` disables the warning level. |
| `cardinality.critical_threshold` | `8000` | The status page flags a source metric critically at/above this active-series count. Must be `>=` `warning_threshold` when both are set. A value above `metric_limit` can never fire (a metric's count pins at `metric_limit`) and triggers a startup advisory. `0` disables the critical level. |
| `cardinality.label_value_sample_cap` | `100` | Distinct values retained per `(metric, label)` by the self-observability cardinality tracker to power the status page's label-cardinality views. Beyond the cap the label is marked capped and its example values truncated (a memory guard for high-cardinality labels such as per-flow IPs). `0` disables label-value capture. |

### `cardinality.flow` — flow metric shaping

These knobs affect flow **metrics** only. Flow **logs** always carry full detail regardless.

!!! warning "`metrics_mode` gates the port toggles"
    `source_port` and `destination_port` apply **only to the raw families**, so under the default
    `metrics_mode: rollup` they are inert — setting one to `true` changes nothing and reports no
    error. The **Applies to** column below says which mode each knob needs. If a dimension you
    configured is missing from your metrics, check `metrics_mode` first.

| Key | Default | Applies to | Description |
|-----|---------|------------|-------------|
| `cardinality.flow.metrics_mode` | `rollup` | — | Which flow metric families to emit. `rollup` — bounded top-N `*.rollup` families (lowest cardinality; adds per-source-node `tailscale.network.unique.*` gauges). `all` — per-connection raw families shaped by the toggles below. `both` — emit both (≈2× series; summing them double-counts — a startup WARN fires). |
| `cardinality.flow.rollup_top_n` | `500` | `rollup`, `both` | Number of busiest source/destination node pairs kept per flush; the rest fold into `__other__`. `0` selects the default (500). |
| `cardinality.flow.source_port` | `false` | `all`, `both` | Add `source.port` to flow **metrics**. **Inert under `rollup`.** Ports are always present on flow **logs**. The single most expensive knob here — ephemeral source ports are effectively unbounded. |
| `cardinality.flow.destination_port` | `false` | `all`, `both` | Add `destination.port` to flow **metrics**. **Inert under `rollup`**, where `tailscale.dst.service` is the bounded stand-in. |
| `cardinality.flow.node_dims` | `true` | all modes | Include `tailscale.src.node`/`tailscale.dst.node` device names on flow metrics — *who talked to whom*. Off keeps totals accurate but drops the per-peer breakdown, and suppresses the `tailscale.network.unique.*` gauges (they are keyed by source node, so emitting them would reintroduce exactly the cardinality this removes). |
| `cardinality.flow.identity_dims` | `false` | all modes | Include the per-flow endpoint identity — `tailscale.{src,dst}.user`, `.tags` and `.os` — on flow **metrics**. Sourced from the `srcNode`/`dstNodes` blocks the control plane embeds in every flow record, so it costs no extra API call. Identity is a property of the **node**, so with `node_dims` on it widens the label set without multiplying the series count. **Requires `node_dims`** and is ignored without it: identity would otherwise become the only dimension splitting the metric, reintroducing the cardinality that turning `node_dims` off is meant to shed. Off by default because `user` is an email address. Flow **logs** carry these attributes regardless. PII filtering still applies: `tailscale.{src,dst}.user` is classified as an email. On the `*.rollup` families the `__other__` remainder drops identity — the fold is many nodes, so it has no single user to report. |
| `cardinality.flow.collapse_external` | `true` | all modes | Bucket unresolved/off-tailnet IPs as `external`/`unknown` instead of the raw address. Off = one series per distinct external IP. |
| `cardinality.flow.exit_node_attribution` | `true` | all modes | Emit the bounded `tailscale.exit_node.io`/`tailscale.exit_node.packets` counters attributing exit traffic to the relaying node (bounded by exit-node count). Independent of `metrics_mode`. |

**Always on, no toggle.** Two dimensions are emitted on both metric families unconditionally, because
each has a fixed, small value space:

- `tailscale.dst.service` — the IANA service name for the destination port and transport
  (`tcp/443` → `https`), from an embedded copy of the IANA registry. It is the bounded stand-in for
  the destination port: you can ask "how much HTTPS ran between these two nodes" without ephemeral
  ports splitting the series. Ports that map to no registered name omit the attribute entirely.
- `tailscale.path` — how the two nodes actually reached each other, read off the underlay endpoint:
  `direct` or `derp`. A relayed connection additionally carries `tailscale.derp.region_id`, the
  numeric region from the relay marker. Both appear on **physical traffic only**; the overlay traffic
  types describe what the tailnet carried rather than how, so they carry no path rather than one that
  would read as `direct`. `tailscale.derp.region_id` is **not** joinable with `tailscale.derp.region`
  on the device latency metrics — that one is a region *name*, this is a numeric *ID*, and the API
  exposes no DERP map to translate between them.

### `cardinality.per_entity` — per-entity gauge gates

When a toggle is `false`, only the low-cardinality aggregate `*.count` rollup is emitted; the
per-entity gauge series (one per device/user/key/…) are dropped. All default `true`.

| Key | Default | Description |
|-----|---------|-------------|
| `cardinality.per_entity.device` | `true` | Emit per-device gauges (online, last-seen, key-expiry, DERP latency, routes). `false` leaves only `tailscale.devices.count`. |
| `cardinality.per_entity.user` | `true` | Emit per-user gauges (devices, connected, last-seen). `false` leaves only `tailscale.users.count`. |
| `cardinality.per_entity.key` | `true` | Emit the per-key gauges (`tailscale.key.expiry`, `tailscale.key.scopes`, `tailscale.key.preauthorized`). `false` leaves only `tailscale.keys.count` (the "expiring soon" WARN log still fires). |
| `cardinality.per_entity.webhook` | `true` | Emit per-webhook gauges. `false` leaves only the aggregate count. |
| `cardinality.per_entity.service` | `true` | Emit per-service gauges. `false` leaves only the aggregate count. |

---

## `collectors` — per-source polling

Each collector has at least `enabled` and `interval`. The two **log** collectors (`flowlogs`,
`auditlogs`) additionally have `source` and a set of windowing fields; the rest are point-in-time
snapshots.

### Common fields

| Key | Applies to | Default | Description |
|-----|-----------|---------|-------------|
| `<collector>.enabled` | all | `true` (except `node_metrics`) | Whether the collector runs. |
| `<collector>.interval` | all | per-collector | Poll cadence. Snapshot collectors read once per interval; window (log) collectors poll one time-window per interval. |

### `source` and the windowing fields (`flowlogs` / `auditlogs` only)

`source` selects how the log collector obtains data:

- **`poll`** (default) — the exporter pulls logs from the Tailscale API on `interval`, one
  time-window per tick.
- **`stream`** — logs are **pushed** to the [`streaming`](#streaming-splunk-hec-log-receiver)
  receiver instead; the exporter does not poll this log type.
- **`objectstore`** — the exporter reads Tailscale's export objects from an S3-compatible bucket
  instead of calling the API. Available for **both** log types, each with its own destination:
  [`collectors.flowlogs.objectstore`](#collectorsflowlogsobjectstore-the-s3-export-as-an-ingestion-source)
  and [`collectors.auditlogs.objectstore`](#collectorsauditlogsobjectstore-the-configuration-log-export).
  The windowing fields below are ignored; the object-store block has its own interval and lookback.
- **`both`** — poll *and* accept the stream. **Discouraged:** the same record can be double-counted.
  Cross-source de-duplication is a best-effort failsafe, not a guarantee, and a startup WARN fires.

Pick exactly one method per log type. Which fields are honored depends on `source`:

| Field | Applies to | `poll` | `stream` | Purpose |
|-------|-----------|:------:|:--------:|---------|
| `enabled` | both | ✓ | ✓ | Turn the collector on/off. |
| `source` | both | ✓ | ✓ | Select the ingestion path. |
| `interval` | both | ✓ | — | Poll cadence (no poller runs under `stream`). |
| `lag` | both | ✓ | — | Query only up to `now − lag`, so late-arriving records aren't missed. Must be **≥ 0** (a negative lag pushes the window end into the future and permanently skips records that arrive within it — rejected at startup). |
| `initial_lookback` | both | ✓ | — | Cold-start reach-back when there is no checkpoint yet. Must be **> 0** — `0` (or negative) leaves the poll window's `from ≥ to` forever, so the collector never polls and never checkpoints; rejected at startup rather than silently stalling. |
| `max_window` | both | ✓ | — | Cap a single tick's window so a long outage catches up over several ticks. `0` (or negative) means **no cap** (the explicit sentinel). A *positive* `max_window ≤ interval` can never catch up (each tick advances at most `max_window`, so a backlog grows or stalls forever), and is now **rejected at startup** as a hard validation error — use `max_window > interval`, or `0` for no cap. Stream-only collectors are unaffected (they have no catch-up window). **Must be tuned together with [`tailscale.http.timeout`](#tailscalehttp)**: a catch-up window that takes longer to fetch+decode than the timeout fails every attempt identically and never advances (see the note under `tailscale.http`). |
| `replay_overlap` | `flowlogs` | ✓ | — | Reread this much before the durable high-water mark so a record that became available after the first completed query can still arrive. Default `5m`; `0` disables; maximum `1h`. This is distinct from `lag`: lag delays closing the newest window, while replay deliberately revisits already completed time. |
| `replay_seen_capacity` | `flowlogs` | ✓ | — | Maximum durable SHA-256 connection identities retained to suppress the intentional replay across restart. Default `131072`; `1..1048576` while replay is enabled. Raw node IDs and endpoints are never checkpoint keys. |
| `log_mode` | `flowlogs` | ✓ | ✓ | Log detail level — output shaping in the shared processor. |
| `max_log_records_per_window` | `flowlogs` | ✓ | ✓¹ | Cap on emitted flow LOG records (see below). |

¹ Under `poll` the budget is shared across the whole poll window; under `stream` it is applied per
received record. Either way, **metrics are never capped — only logs.**

> The four windowing fields exist purely to drive the poller, so they are **ignored when
> `source: stream`**. The `streaming`/`webhook` receivers and the pollers feed the *same* processors,
> which is why `log_mode` and the `cardinality.*` knobs apply on every path.
>
> **`source: stream` requires a live ingestion path.** It is rejected at startup unless
> `streaming.enabled: true`; in multi-tailnet mode it additionally requires `streaming.routes` so the
> collector has an unambiguous receiving runtime. Otherwise the collector would have no way to receive
> records and would silently ingest nothing. Use `source: poll` (the default) or `both` when the receiver is off.

### `collectors.devices`

| Key | Default | Description |
|-----|---------|-------------|
| `collectors.devices.enabled` | `true` | Emit device gauges + counts and **populate the enrichment cache**. |
| `collectors.devices.interval` | `60s` | Poll cadence. |
| `collectors.devices.collect_routes` | `false` | Also emit per-device subnet-route gauges. Read from the inline device data — **no extra API call**. |
| `collectors.devices.collect_connectivity` | `true` | Emit per-device NAT/connectivity health (`tailscale.device.connectivity.*`: hard_nat, endpoints, direct_capable, udp, ipv6) plus the fleet connectivity rollups (`tailscale.devices.hard_nat`/`direct_capable`/`client_supports`). Read from the inline device data — **no extra API call**. Per-device gauges additionally gated by `cardinality.per_entity.device`. |
| `collectors.devices.collect_posture` | `false` | Also fetch device posture attributes (one **extra API call per device per tick**) and emit posture log events. |
| `collectors.devices.collect_device_invites` | `true` | Also fetch outstanding device share invites per device (one **extra API call per device per tick**, N+1) and emit `tailscale.device_invites.count`. Requires the `device_invites:read` OAuth scope (covered by `all:read`). Per-device failures are non-fatal. |
| `collectors.devices.posture_log_mode` | `changes` | Controls the `tailscale.device.posture` log (requires `collect_posture`). `changes` — full dump on first scrape then deltas only. `always` — every scrape. `off` — suppress the log (the posture gauge metric is still emitted). |
| `collectors.devices.attribute_namespaces` | `["intune","jamf","kandji","crowdstrike","sentinelone","kolide","ip"]` | Device posture-attribute namespace prefixes promoted to `tailscale.device.attribute{,.info}` metrics (requires `collect_posture`). `["*"]` promotes every namespace; `[]` disables the attribute metrics. Comma-separated in env: `TS2OTEL_COLLECTORS__DEVICES__ATTRIBUTE_NAMESPACES=intune,jamf`. |
| `collectors.devices.collect_tag_rollup` | `true` | Emit the `tailscale.devices.by_tag` distribution gauge (one series per ACL tag). `false` keeps the other fleet-hygiene aggregates (`untagged`/`ephemeral`/`by_version`/`key_expiry`). |
| `collectors.devices.tag_rollup_limit` | `50` | Cap on distinct tag series for `tailscale.devices.by_tag`: the busiest N tags by device count keep their own series; the rest fold into a single `tailscale.tag="__other__"` series. `0` or negative = unlimited. |

### `collectors.flowlogs`

Network flow logs → aggregated traffic counters + per-connection flow logs.

| Key | Default | Description |
|-----|---------|-------------|
| `collectors.flowlogs.enabled` | `true` | Whether flow logs are collected. |
| `collectors.flowlogs.source` | `poll` | `poll` \| `stream` \| `objectstore` \| `both`. See [`source`](#source-and-the-windowing-fields-flowlogs-auditlogs-only) and [`objectstore`](#collectorsflowlogsobjectstore-the-s3-export-as-an-ingestion-source). |
| `collectors.flowlogs.interval` | `60s` | Poll cadence (poll only). |
| `collectors.flowlogs.lag` | `120s` | Tail-safety margin; query up to `now − lag` (poll only). Flow logs have a noticeable tail, hence the larger default than audit. |
| `collectors.flowlogs.initial_lookback` | `5m` | Cold-start reach-back (poll only). |
| `collectors.flowlogs.max_window` | `1h` | Catch-up cap for one tick (poll only). |
| `collectors.flowlogs.replay_overlap` | `5m` | Reread this much of completed poll history for late API records (`0` disables; maximum `1h`). Separate from the tail-safety `lag`. |
| `collectors.flowlogs.replay_seen_capacity` | `131072` | Bounded durable hashed connection identities used to suppress the replay across restart (`1..1048576` while enabled). |
| `collectors.flowlogs.trusted_reporter_node_ids` | `[]` | Optional allowlist of verified `FlowLog.NodeID` reporters, classified as `configured`. The reporter observation metric carries only the bounded trust/consistency classes, never these raw IDs. |
| `collectors.flowlogs.trusted_reporter_tags` | `[]` | Optional authoritative device tags that classify a verified reporter as `tagged`. Only the devices collector's control-plane cache can grant tag trust; tags embedded in the flow record never do. Other reporters are `untrusted`; with both trust lists empty, reporter trust is `unconfigured`. |
| `collectors.flowlogs.log_mode` | `per_connection` | Flow-log detail. One of `per_connection` (one log per 5-tuple), `per_record` (one summary log per node window), or `off` (no flow logs, metrics only). |
| `collectors.flowlogs.max_log_records_per_window` | `0` | Cap on flow LOG records emitted (`0` = unlimited). Excess is counted into `tailscale.network.flow.logs_dropped`. Metrics are never capped. |

#### `collectors.flowlogs.objectstore` — the S3 export as an ingestion source

Tailscale can export network flow logs to an S3-compatible bucket. Reading that export is the third
ingestion path (`collectors.flowlogs.source: objectstore`), and the cheapest one for a large tailnet:
the objects are immutable, already batched, and cost no API quota. It is also the only practical way
to backfill a long history.

The records in the bucket are the **same** records the API returns, so they go through the same
processor and produce the same signals. That is also why running `objectstore` alongside `poll` or the
stream receiver double-counts — pick one, exactly as for the other sources.

Every field below applies **only** when `source: objectstore`.

**This block is the destination for single-tailnet mode.** With a `tailnets:` list every entry carries
its own destination under `tailnets[].objectstore.flow` instead, with the same fields and no
inheritance from here — see
[per-tailnet object-store destinations](#tailnets-multi-tailnet-msp-mode). An endpoint that is not an
absolute `http://`/`https://` URL with a host is rejected at startup either way: the S3 client cannot
be built from it, and that is an immutable fault rather than something a retry fixes.

| Key | Default | Description |
|-----|---------|-------------|
| `collectors.flowlogs.objectstore.endpoint` | `""` | **Required.** Service URL, e.g. `https://s3.eu-west-2.amazonaws.com`, or a MinIO/Ceph address. Deliberately not derived from the region: a non-AWS implementation would be derived wrong. Must be an absolute `http`/`https` URL with a host. |
| `collectors.flowlogs.objectstore.region` | `""` | **Required.** Part of the request signature, so a wrong or missing value fails every request with HTTP 403 rather than degrading quietly. |
| `collectors.flowlogs.objectstore.bucket` | `""` | **Required.** The bucket Tailscale exports into. |
| `collectors.flowlogs.objectstore.prefix` | `""` | The export's root within the bucket, above the `YYYY/MM/DD` partitions. **No leading slash** — an S3 key prefix has none, and Tailscale writes none. One is accepted but warned about, because it forms part of this feed's durable checkpoint identity: removing it later reads as a brand-new feed, so the cursor and seen set start over and up to `initial_lookback` of already-ingested objects are re-emitted. |
| `collectors.flowlogs.objectstore.layout` | `partitioned` | How objects are arranged under `prefix`: `partitioned` or `flat`. Not autodetected — see [export layouts](#export-layouts-partitioned-vs-flat) below. Any other value is a startup error. |
| `collectors.flowlogs.objectstore.path_style` | `false` | Address as `<endpoint>/<bucket>/<key>` rather than `<bucket>.<endpoint>/<key>`. Required by most non-AWS implementations. Getting it backwards shows up as a DNS failure, not an HTTP error. |
| `collectors.flowlogs.objectstore.allow_insecure_http` | `false` | Permit plaintext HTTP to a **remote** object-store endpoint. HTTP loopback endpoints (`localhost`, `127.0.0.0/8`, `::1`) remain available without the override for local MinIO development. Enabling this sends signing credentials and temporary session tokens over the network without TLS and emits a startup warning; prefer HTTPS. |
| `collectors.flowlogs.objectstore.access_key_id` | `""` | Static credential. **Set via `TS2OTEL_*` env only.** Leave empty to use the ambient chain (below). |
| `collectors.flowlogs.objectstore.access_key_id_file` | `""` | Read the static access key ID from this path instead of an inline value. Set value **or** file, never both; content is whitespace-trimmed at startup. |
| `collectors.flowlogs.objectstore.secret_access_key` | `""` | Static credential. **Env only.** |
| `collectors.flowlogs.objectstore.secret_access_key_file` | `""` | Read the static secret access key from this path instead of an inline value. Set value **or** file, never both; content is whitespace-trimmed at startup. |
| `collectors.flowlogs.objectstore.session_token` | `""` | Static credential, temporary sessions only. **Env only.** |
| `collectors.flowlogs.objectstore.session_token_file` | `""` | Read the temporary session token from this path instead of an inline value. Set value **or** file, never both; content is whitespace-trimmed at startup. |
| `collectors.flowlogs.objectstore.interval` | `60s` | How often the bucket is listed. |
| `collectors.flowlogs.objectstore.lookback` | `1h` | How far back past the cursor each listing reaches, so an object that arrived late is still found. Setting it below `interval` is warned about: the overlap would be smaller than the gap between listings, so an object landing between two cycles could be missed. |
| `collectors.flowlogs.objectstore.initial_lookback` | `6h` | Cold-start reach-back, so a first run against a bucket holding months of exports does not try to ingest all of it. **Capped in effect at 14 days under `layout: partitioned`** — a larger value silently ingests only the most recent 14 day partitions and is warned about at startup. |
| `collectors.flowlogs.objectstore.max_objects` | `200` | Objects ingested per cycle. Exceeding it is not an error: the remainder is counted into `tailscale2otel.objectstore.skipped{reason="per_cycle_budget"}`, logged at WARN, reported by the `tailscale2otel.objectstore.backlog` gauge, and picked up next cycle. |
| `collectors.flowlogs.objectstore.max_object_wire_bytes` | `67108864` (64 MiB) | Maximum GET response bytes read from one object. A breach quarantines that object as a durable gap, including compressed objects that consume work without producing decoded rows. Must be positive. |
| `collectors.flowlogs.objectstore.max_object_decompressed_bytes` | `33554432` (32 MiB) | Maximum decompressed bytes accepted from one object. A breach quarantines that object as a durable gap. Must be positive. |
| `collectors.flowlogs.objectstore.max_object_records` | `100000` | Maximum records accepted from one object. A breach quarantines that object as a durable gap. Must be positive. |
| `collectors.flowlogs.objectstore.max_cycle_wire_bytes` | `536870912` (512 MiB) | Maximum GET response bytes read in one cycle. Once reached, the current object and untouched objects are deferred without creating gaps. Must be positive and at least `max_object_wire_bytes`. |
| `collectors.flowlogs.objectstore.max_cycle_decompressed_bytes` | `268435456` (256 MiB) | Maximum decompressed bytes processed in one cycle. Once reached, untouched objects are deferred to a later cycle without creating gaps. Must be positive and at least `max_object_decompressed_bytes`. |
| `collectors.flowlogs.objectstore.max_cycle_records` | `500000` | Maximum records processed in one cycle. Once reached, untouched objects are deferred to a later cycle without creating gaps. Must be positive and at least `max_object_records`. |

##### Export layouts: `partitioned` vs `flat`

Tailscale's own export always writes day partitions. Verified against a live export on 2026-07-27, for
both the network and configuration log types, the keys look like this — the date appears **twice**, in
the partitions and again in a self-contained basename:

```
<prefix>/YYYY/MM/DD/YYYY-MM-DD-HH-MM-SS.ndjson[.zst|.gz]
```

The extension follows the destination's `compressionFormat`: `.ndjson` for `none`, `.ndjson.zst` for
`zstd`, `.ndjson.gz` for `gzip` (all three observed live). The configured `s3KeyPrefix` is used
verbatim, so it must carry its own trailing slash. Tailscale also writes a **zero-byte object** for an
upload period with nothing to report; that is a normal empty object, not an error.

Tailscale's documentation describes a time-only basename instead
(`<prefix>/YYYY/MM/DD/HH:MM:SS.json[.zst|.gz]`). That form has never been observed from the live
publisher, but it is accepted too, so a change of publisher behaviour would not silently drop data.

`layout: partitioned` (the default) enumerates exactly the `YYYY/MM/DD/` partitions spanning the
listing window. That bound is what keeps a first run against a bucket holding months of exports from
walking all of it, and it is the right setting for every bucket Tailscale writes to directly.

A **copied or mirrored** export can end up flattened, with self-contained basenames and no partition
directories above them:

```
<prefix>/YYYY-MM-DD-HH-MM-SS.ndjson[.zst|.zstd|.gz|.gzip]
```

Those keys have always parsed, but under `partitioned` they are never **listed**, so they were never
discovered. `layout: flat` lists `prefix` itself with no delimiter, which finds them.

Choosing `flat` is an explicit decision and emits a startup advisory. There is no `auto`: the two
layouts are distinguishable only by listing the bucket, and guessing wrong changes what the durable
scan positions mean.

What to know before setting it:

- **`flat` is a superset, so a mixed bucket works.** An undelimited listing of `prefix` also returns
  everything beneath the day partitions, and both key shapes parse, so one bucket holding both is
  fully ingested.
- **It costs more LIST requests.** There are no partitions to bound the re-walk, so once caught up
  every cycle re-walks the prefix. Each cycle is still bounded — one listing of at most
  `max_objects * 4` keys — and resumes from a durable position, so no single cycle scans an unbounded
  bucket. But a large flat prefix takes several cycles per full sweep, and a newly written object is
  discovered only when the walk reaches its key, which raises ingestion latency accordingly.
- **A time-only key directly under a flat root stays unreadable.** `<prefix>/HH:MM:SS.json`
  carries no date — the date lives in the three directories the flattening removed — so it is counted
  into `tailscale2otel.objectstore.skipped{reason="unrecognized_key"}` rather than guessed at. Only
  the self-contained `YYYY-MM-DD-HH-MM-SS` basename is genuinely flat-readable.
- **Switching layout is safe in both directions.** The scan positions the other layout wrote are
  recognized as stale on the first cycle after the switch and deleted there, so nothing is left behind
  to be listed under one layout and never pruned under the other. `lookback` still bounds recovery:
  an object older than the overlap window is out of reach under either layout.

With a `tailnets:` list the key is `tailnets[].objectstore.flow.layout`, per entry, with the same
values and the same default.

**Credentials.** The three credential values are `config.Secret` fields: config dumps, structured
logs, validation errors, and the admin status surface redact or omit them. They are revealed only
when the S3 provider client is constructed. Each has a `_file` sibling for a mounted Secret; setting
both a value and its file is a startup error.

Leave all three values and files empty and the ambient chain is used, in this order:
the environment (`AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` / `AWS_SESSION_TOKEN`), then **web
identity** (`AWS_ROLE_ARN` + `AWS_WEB_IDENTITY_TOKEN_FILE` — this is IRSA on EKS), then the
**container credential endpoint** (`AWS_CONTAINER_CREDENTIALS_RELATIVE_URI` on ECS task roles, or
`AWS_CONTAINER_CREDENTIALS_FULL_URI` plus a token file on EKS Pod Identity), then the **EC2
instance profile** via IMDSv2. Set `AWS_EC2_METADATA_DISABLED=true` off EC2 to skip the last probe,
which otherwise costs a connection timeout on every refresh. Temporary credentials are refreshed
5 minutes before expiry.

The container endpoint is the only step whose address comes from the environment, so it is
constrained harder than the AWS SDKs constrain it: the host must be loopback, `169.254.170.2` (ECS)
or `169.254.170.23` / `fd00:ec2::23` (EKS Pod Identity); a hostname is refused unless *every* address
it resolves to is in that set; the destination is re-checked at connect time and dialled by literal,
so a DNS answer that changes in between cannot redirect the fetch; redirects are refused outright;
and userinfo in the URL is rejected. **This is stricter than the AWS SDKs, which apply their host
allow-list only to `http://` URLs and let `https://` reach any host.** An outbound credential fetch
aimed at an arbitrary host is an egress channel, and neither ECS nor EKS needs one — both agents
serve on the documented link-local addresses. If you are using a third-party local credential broker
on some other address, this exporter will refuse it and say so.

The shared config file (`~/.aws/credentials`, `AWS_PROFILE`, SSO login) is **not** supported. It is a
developer-laptop convenience; static environment credentials cover the same ground in one variable,
and container deployments use a role. This is a deliberate omission, not a gap — see
[#238](https://github.com/rknightion/tailscale2otel/issues/238) for the reasoning and the binary-size
measurement behind it.

**At-least-once durability boundary.** With the file checkpoint store, successful object identities,
bounded per-prefix listing progress, and failed-object gaps survive restart. A failed GET or stream
read is retried independently with exponential backoff capped at one hour, even after later objects
advance the timestamp cursor. Invalid gzip/zstd framing is deterministic for an immutable object and
is quarantined immediately. The three gap gauges report unresolved count, oldest age, and health
without object-key attributes; logs identify an object only by a 12-character SHA-256 digest.

Malformed JSON and semantically invalid flow rows are record-level failures: good rows in the same
NDJSON object are accepted and the object can complete. GET, decompressor, and scanner failures are
object-level gaps. A scanner failure can occur after good rows were emitted, so retry can duplicate
those rows after restart until object processing is atomic. OTLP/backend acknowledgement is outside
this boundary. An in-memory checkpoint can replay successful objects and loses pending gaps on
restart.

**Quarantine and acknowledgement.** A quarantined gap is not retried automatically and keeps
`tailscale2otel.objectstore.gap.healthy` at `0`. To acknowledge it, stop the process and remove only
its `objectstore/v1/<tailnet>/<provider>/<signal>/<feed>/gap/...` row from the owner-only checkpoint
JSON; the paired `.../seen/...` row keeps the immutable object from being fetched again. The tailnet
component is base64url-encoded and the feed is a one-way digest of endpoint, bucket, and prefix, so
raw provider identifiers do not enter checkpoint paths. To replace the object at the same key and
retry it, remove both the gap row and its paired seen row before restarting. On first startup after
upgrade, the previous `objectstore.flowlogs.*` rows are migrated atomically into this scoped layout;
existing scoped rows win if both layouts are present.


### `collectors.auditlogs`

Configuration/audit events → event logs + a counter.

| Key | Default | Description |
|-----|---------|-------------|
| `collectors.auditlogs.enabled` | `true` | Whether audit logs are collected. |
| `collectors.auditlogs.source` | `poll` | `poll` \| `stream` \| `objectstore` \| `both`. See [`source`](#source-and-the-windowing-fields-flowlogs-auditlogs-only) and [`objectstore`](#collectorsauditlogsobjectstore-the-configuration-log-export) below. |
| `collectors.auditlogs.interval` | `60s` | Poll cadence (poll only). |
| `collectors.auditlogs.lag` | `60s` | Tail-safety margin (poll only). |
| `collectors.auditlogs.initial_lookback` | `5m` | Cold-start reach-back (poll only). |
| `collectors.auditlogs.max_window` | `6h` | Catch-up cap for one tick (poll only). |

#### `collectors.auditlogs.objectstore` — the configuration-log export

Tailscale exports configuration (audit) logs to an S3-compatible bucket exactly as it does network flow
logs, as a separate destination with its own key space. Setting `collectors.auditlogs.source:
objectstore` reads that export.

**The fields, defaults, layout rules, budgets and credential handling are identical to
[`collectors.flowlogs.objectstore`](#collectorsflowlogsobjectstore-the-s3-export-as-an-ingestion-source)**
— read that section for all of them; only the key prefix differs
(`collectors.auditlogs.objectstore.*`). With a `tailnets:` list, each entry carries its own
`objectstore.audit` block instead, with no inheritance from here.

Two rules are specific to running both signals from object storage:

- **Nothing is shared between the two destinations.** The network and configuration exports are
  different objects; pointing the audit collector at the flow bucket decodes nothing and looks like an
  idle tailnet.
- **No two destinations this process reads may name the same feed** — the same endpoint, region,
  bucket, prefix and `path_style`. Both engines would fetch every object and then fail to decode the
  other signal's records, burning their budgets to produce undecodable-object errors. One bucket with a
  distinct prefix per signal is the normal arrangement, and it is what Tailscale's own console
  encourages via `s3KeyPrefix`.

The records carry their own `eventTime`, so an object-store audit event is timestamped identically to a
polled or streamed one and reaches the same processor. The export additionally carries a `logged`
publisher timestamp, which supplies the ingest freshness/lag view; the polled API carries a `type`
field the export does not. Neither field is required.

### Snapshot collectors

| Key | Default | Description |
|-----|---------|-------------|
| `collectors.users.enabled` / `.interval` | `true` / `300s` | User/role/status counts and per-user device & connection gauges. |
| `collectors.keys.enabled` / `.interval` | `true` / `300s` | Key inventory gauges (auth keys, OAuth clients, and API tokens via the unified key model), counts bucketed by `type`/`auth_kind`/`revoked`/`invalid`, and an "expiring soon" WARN log. Per-key `key.expiry`/`key.scopes`/`key.preauthorized` gauges are gated by `cardinality.per_entity.key`. |
| `collectors.keys.expiry_warn` | `168h` | Emit the "expiring soon" WARN log when a key expires within this window (default 7 days). |
| `collectors.settings.enabled` / `.interval` | `true` / `600s` | Tailnet feature-toggle gauges. |
| `collectors.acl.enabled` / `.interval` | `true` / `600s` | ACL size + a "policy changed" signal (detected by ETag), plus policy risk-scoring gauges (wildcard / unrestricted / auto-approver / SSH-wildcard / posture-gated rules). |
| `collectors.acl.validate` | `true` | Validate the tailnet's active policy each tick via `POST /tailnet/{tailnet}/acl/validate`. Despite the verb this is a **read** operation — upstream requires only the `policy_file:read` scope and it never modifies the policy; sending no body validates the *current* policy. It is the only non-GET call this exporter makes, so set `false` if you require a strictly GET-only client. Permission denial reports as unavailable, never as a passing validation. |
| `collectors.dns.enabled` / `.interval` | `true` / `600s` | Nameserver / search-path / split-zone counts, the MagicDNS and override-local flags, the count of exit-node-eligible resolvers, and a per-resolver info gauge (`tailscale.dns.resolver`) labeled by address, kind, domain, and exit-node eligibility. |
| `collectors.contacts.enabled` / `.interval` | `true` / `600s` | Tailnet security-contact gauges. |
| `collectors.webhooks.enabled` / `.interval` | `true` / `600s` | Configured webhook gauges and per-webhook status. |
| `collectors.webhooks.desired_events` | `[]` | Optional list of webhook event categories this tailnet is expected to subscribe to (e.g. `["nodeCreated","userSuspended"]`). When set, the collector reports which desired categories no endpoint covers, so a silently-unsubscribed alerting path becomes visible. Empty means coverage is still exported per category but nothing is flagged as missing. Values outside the documented event vocabulary fold to `other`. |
| `collectors.posture_integrations.enabled` / `.interval` | `true` / `600s` | MDM/EDR posture-integration gauges. |
| `collectors.log_stream.enabled` / `.interval` | `true` / `600s` | Log-streaming configuration gauges. |
| `collectors.oauth_apps.enabled` / `.interval` | `true` / `300s` | OAuth-application inventory (count, per-app scope/node-attribute gauges). Alpha API — idles silently (no error) on tailnets without it enabled. |

### `collectors.services`

| Key | Default | Description |
|-----|---------|-------------|
| `collectors.services.enabled` | `true` | Emit Tailscale VIP-Services gauges and counts. |
| `collectors.services.interval` | `600s` | Poll cadence. |
| `collectors.services.collect_hosts` | `false` | Also fetch per-service backing-host detail — one extra API call per service (N+1). Off by default. |

### `collectors.node_metrics`

Optional scraper that pulls `tailscaled` per-node Prometheus `/metrics` and forwards them centrally
over OTLP (counters as deltas, gauges as gauges, plus a per-target `tailscale.node.up`). **Off by
default**, and inert unless it has at least one static target or discovery enabled. Node identity is
carried as the `tailscale.node` label (Prometheus: `tailscale_node`), not an OTEL Resource and
deliberately not `instance` — Grafana Cloud's OTLP→Prometheus translation promotes the resource
attribute `service.instance.id` to the `instance` label, and that would clobber a per-series
`instance` attribute and collapse `tailscale.node.up` to one series. See
**[`docs/node-metrics.md`](./node-metrics.md)** for the operator how-to.

| Key | Default | Description |
|-----|---------|-------------|
| `collectors.node_metrics.enabled` | `false` | Master switch. Even when `true`, the scraper only runs if `targets` is non-empty **or** `discovery.enabled` is `true`. |
| `collectors.node_metrics.interval` | `60s` | Scrape cadence. |
| `collectors.node_metrics.timeout` | `10s` | Per-target scrape timeout. |
| `collectors.node_metrics.max_response_bytes` | `4194304` (4 MiB) | Per-target response-size cap. Must be `> 0` when enabled. |
| `collectors.node_metrics.max_samples` | `50000` | Per-target sample cap per scrape. Must be `> 0` when enabled. |
| `collectors.node_metrics.max_distinct_metrics` | `2000` | Cap on **distinct forwarded metric names** over the process lifetime. A scrape target chooses its own metric names and every unseen name creates an OTEL instrument that is never released, so `max_samples` (a per-scrape cap) does not bound them. `0` selects a default of `2000`; a negative value disables the budget. Names beyond the budget are dropped and counted rather than silently ignored. |
| `collectors.node_metrics.metric_allow` | `[]` | Anchored regexes on the forwarded metric **name**; if non-empty, a name must match one to be forwarded. Must compile. |
| `collectors.node_metrics.metric_deny` | `[]` | Anchored regexes; a name matching any is dropped (applied after `metric_allow`). Must compile. |
| `collectors.node_metrics.drop_labels` | `[]` | Label keys stripped from the forwarded series' **emitted** attributes. `tailscale.node` (the node-identity label) is never dropped. Dropping affects only the output labels: counter delta baselines are keyed off the full pre-drop source series (see below), so dropping a label that distinguishes two source counters merges them on output while each keeps its own correct delta. |

These filters apply **only** to forwarded samples — never to `tailscale.node.up` or the `discovery.*`
gauges.

> **Source-series identity vs. emitted labels.** Cumulative counters are forwarded as deltas, and each
> delta baseline is keyed off the *complete* scraped source series — its metric name, every raw label
> (before `drop_labels` and before any curated folding), and the target's stable identity (normalized
> URL + node-identity label). So two source series that collapse onto one emitted series — because
> `drop_labels` removed a distinguishing label, or a curated mapping folds one — keep separate baselines
> (and separate first-observation suppression and reset detection), and their individually-correct
> deltas sum on the merged output. Distinct targets never share a baseline even when they scrape
> identical series.
>
> **Duplicate targets are rejected.** Two static `targets[]` that resolve to the same effective identity
> (same normalized URL **and** same `instance`/node label) are a startup config error — such a pair
> would scrape one endpoint twice under one identity and corrupt each other's baselines. Targets that
> differ only by URL, or only by `instance`, are fine (e.g. a verify-on and a skip-verify scrape of the
> same URL, labeled distinctly). Discovered targets remain deduped against the static set by URL (static
> wins), and any residual same-identity duplicate at runtime is collapsed deterministically.

#### `collectors.node_metrics.targets[]`

A static list of endpoints to scrape (keys below are relative to each list entry). Native
`tailscaled` endpoints are plain HTTP and need no auth/TLS; the optional fields cover proxied/HTTPS
targets.

| Key | Default | Description |
|-----|---------|-------------|
| `url` | — (required) | Scrape URL, e.g. `http://100.64.0.10:5252/metrics`. Required for each target when the scraper is enabled. |
| `instance` | URL `host:port` | Overrides the `tailscale.node` identity label for this target. |
| `labels` | `{}` | Extra static labels merged onto every series from this target. |
| `bearer_token` | `""` | Static bearer token sent as `Authorization: Bearer …`. |
| `bearer_token_file` | `""` | Path read fresh each scrape; takes precedence over `bearer_token`. |
| `headers` | `{}` | Extra request headers (e.g. `X-Scope-OrgID`). |
| `tls.ca_file` / `tls.cert_file` / `tls.key_file` / `tls.server_name` | `""` | TLS trust/identity for HTTPS targets. |
| `tls.insecure_skip_verify` | `false` | Skip server-cert verification (footgun guard defaults off). |

#### `collectors.node_metrics.discovery`

Discover scrape targets dynamically from the Tailscale devices API (keys below are relative to the
`discovery` block). Discovered targets are unioned (deduped by URL, static wins) with the static
`targets`, on this block's **own** interval.

| Key | Default | Description |
|-----|---------|-------------|
| `enabled` | `false` | Turn on dynamic discovery. |
| `interval` | `5m` | How often the devices API is polled for targets (independent of the scrape `interval`). Must be `> 0`. |
| `max_targets` | `1000` | Cap on discovered targets per refresh. Must be `> 0`. |
| `scheme` | `http` | `http` \| `https`. The metrics-endpoint scheme applied to each device. |
| `port` | `5252` | Metrics port (1–65535). |
| `path` | `/metrics` | Metrics path. |
| `online_only` | `true` | Only devices currently connected to the control plane. |
| `exclude_external` | `true` | Skip shared/external devices. |
| `include_tags` | `[]` | If non-empty, only devices with one of these tags (e.g. `["tag:server"]`). |
| `exclude_tags` | `[]` | Devices with any of these tags are skipped (wins over `include_tags`). |
| `address_order` | `ipv4` | Preferred address family, `ipv4` \| `ipv6` (falls back to the other). |
| `instance_source` | `name` | Identity-label source: `name` (MagicDNS short name — unique per tailnet **and** human-friendly; the default), `address` (Tailscale host:port — always unique), or `hostname` (OS hostname — **not** unique; collisions like `localhost` are auto-suffixed with the address + a WARN). |
| `include_host_labels` | `true` | Attach `host.name`/`host.id` for joins with `tailscale.device.*`. |
| `include_tags_label` | `true` | Attach `tailscale.tags`. |

---

## `checkpoint` — poll high-water marks

Checkpoints record how far each **polled** log collector (`flowlogs`/`auditlogs` with
`source: poll` or `both`) has read, so a restart resumes without gaps or large overlaps. The store is
**unused** if you stream both log types or disable them.

| Key | Default | Description |
|-----|---------|-------------|
| `checkpoint.store` | `file` | `file` \| `memory`. See below. |
| `checkpoint.file_path` | `/var/lib/tailscale2otel/checkpoints.json` | Where the file store persists. Used only when `store: file`. The parent directory is created automatically; if it cannot be made writable the exporter logs a WARN and falls back to `memory`. |

- **`file`** (default) — the high-water mark is persisted to `file_path` with an atomic write on each
  tick and reloaded at startup, so polling **resumes from the exact high-water mark** across restarts
  (minor boundary overlap is de-duplicated). For the checkpoint to actually survive a restart, mount a
  writable, **persistent** path at the file's directory (a volume in Kubernetes/Docker). If the path is
  not writable (e.g. a read-only root filesystem with no volume, or a local run without access to
  `/var/lib`), the exporter logs a WARN and transparently falls back to `memory` rather than erroring.
- **`memory`** — the high-water mark lives in RAM only and is lost on restart. After a restart the
  poller cold-starts from `initial_lookback`, so any **downtime longer than `initial_lookback` leaves
  a gap**. Needs no volume; fine for streamed or stateless deployments where the checkpoint is unused
  or disposable.

> **Startup sweep of orphaned staging files.** Each save is staged through a uniquely named temporary
> file in the checkpoint directory and then renamed into place, so a crash can never leave a partially
> written checkpoint. A `SIGKILL` or power loss between those two steps does leave the staging file
> behind, and because the names are unique they would otherwise accumulate one per hard kill. On
> startup the exporter removes staging files matching `.<checkpoint-file>.<random>.tmp` that have gone
> untouched for over an hour.
>
> The one-hour guard is what makes this safe: a staging file exists for milliseconds in normal
> operation, so a second instance's in-flight save can never be old enough to be swept. The checkpoint
> file itself, symlinks, directories, and every other file in the directory are never touched, and a
> sweep failure is logged and ignored rather than blocking startup. The threshold is fixed and not
> configurable.

---

## `ingress_wal` — durable local receiver acceptance

The process-global ingress WAL is an opt-in durability boundary for accepted streaming and webhook
request bodies. It is disabled by default, so the default remains stateless. When enabled, a
successful receiver ACK means the accepted payload was fsynced into the local WAL: the raw
authenticated webhook body or the fully validated decompressed streaming body. It means **durable
local acceptance only**: it does not mean OTLP export completed or the backend acknowledged the data.

Replay is at-least-once. A crash after export but before the local completion commit can replay the
same body and create duplicates. There is no TTL, age-based cleanup, or eviction. An exhausted byte
or entry limit refuses new receiver requests, and a file/directory fsync failure or corrupt state
fails closed rather than acknowledging data whose durability is uncertain.

| Key | Default | Description |
|-----|---------|-------------|
| `ingress_wal.enabled` | `false` | Enable durable local acceptance and oldest-first replay for receiver request bodies. With both receivers disabled, this is a valid drain-only configuration for clearing already persisted entries. It does not require the admin server or a persistent volume. |
| `ingress_wal.directory` | `/var/lib/tailscale2otel/ingress-wal` | WAL directory. When enabled, it must be an absolute, filepath-clean path and must not be the filesystem root. The existing parent must be writable; the WAL creates and secures the final directory. |
| `ingress_wal.max_bytes` | `268435456` (256 MiB) | Encoded byte ceiling. Must be `> 0` and `< 9223372036854775807`. Counts pending entries and staging/recovery state; full means new receiver requests fail closed. |
| `ingress_wal.max_entries` | `10000` | Encoded entry ceiling. Must be `> 0`; full means new receiver requests fail closed. |
| `ingress_wal.corruption` | `fail` | Corruption policy. `fail` is the only supported value: malformed, truncated, checksum-invalid, or incompatible state blocks startup/drain instead of being discarded. |

The WAL is process-global and provider-neutral: `provider: headscale` is valid. It does not require
an enabled receiver, so an operator can disable both receivers and drain already accepted entries.
It also has no dependency on the admin listener.

Each enabled receiver must set its own `max_body_bytes` to a positive value no larger than
`67108864` (64 MiB) while the WAL is enabled. The receiver cap bounds one accepted payload before it
becomes an encoded WAL entry. The usual `0` receiver defaults and negative unlimited values remain
valid when the WAL is disabled, and dormant WAL fields are not validated.

The directory is owner-only and held under an exclusive writer lock for the process lifetime. A
second writer, a symlink/non-regular object, or state with unsafe permissions is refused. Keep one
process per WAL and run it under the same filesystem user after restart. WAL entries contain
sensitive raw or decompressed receiver payloads; do not expose, share, or back them up without the
same access controls as the source data. Durable filesystem WAL construction is supported on Linux
and macOS; Windows builds retain stateless operation, but enabling the WAL is unsupported.

Persisted identities include the configured runtime/tailnet, source, and signal. Before renaming or
removing a configured identity, stop new ingress for it and run the same configuration in drain-only
mode until its entries are gone. Renaming first leaves the old identity with no valid replay route
and intentionally fails closed.

For Docker Compose, the existing named `checkpoints` volume already mounts
`/var/lib/tailscale2otel`, so it holds checkpoints and the WAL without stranding the old volume. In
the Helm chart, the default `emptyDir` survives container restarts within one pod but is lost on pod
replacement, rescheduling, or node loss. Use `persistence.enabled=true` (optionally with
`persistence.existingClaim`) for reschedule durability. The chart keeps its existing `64Mi`
checkpoint-only PVC default; for the default 256 MiB encoded WAL ceiling, request at least `512Mi`
for entries, staging files, and metadata.

---

## `streaming` — Splunk-HEC log receiver

Optional receiver for Tailscale's log streaming (a Splunk-HEC sink). When you enable it, set the
relevant log collector(s) to `source: stream` so each log type is ingested by exactly one path.
**Off by default.**

| Key | Default | Description |
|-----|---------|-------------|
| `streaming.enabled` | `false` | Run the HEC receiver. |
| `streaming.listen` | `:8088` | Listen address. |
| `streaming.path` | `/services/collector/event` | HEC event path. |
| `streaming.token` | `""` | Shared secret for the receiver. Tailscale's log-streaming sender authenticates with **HTTP Basic auth** — `Authorization: Basic base64(<user>:<token>)`, where the password is this token (any username is accepted). The `Authorization: Splunk <token>` scheme is also accepted, as a fallback for other Splunk-HEC-compatible senders, but is not what Tailscale itself sends. Set via `TS2OTEL_STREAMING__TOKEN`. |
| `streaming.token_file` | `""` | Read `streaming.token` from a file at startup instead of a literal value (Docker-secrets style). Setting both the value and the file is a config error. File content is whitespace-trimmed. |
| `streaming.public_url` | `""` | Externally reachable receiver URL. **Required when `auto_configure: true`**. Must be an absolute HTTP(S) URL with a valid host and port. HTTPS may use a public endpoint. Tailscale's private-HTTP contract accepts a shared-node hostname/FQDN or IPv6 literal but rejects every IPv4 literal; such HTTP URLs receive a startup warning because local validation cannot prove node sharing or policy. The configured path and query are preserved exactly. |
| `streaming.tls.cert_file` / `.key_file` | `""` | HTTPS is required by Tailscale; a `tailscale cert` works for private tailnet endpoints. |
| `streaming.decompress` | `auto` | Request-body decompression: `auto` \| `gzip` \| `zstd` \| `none`. |
| `streaming.auto_configure` | `false` | On startup, PUT this receiver as a Splunk-HEC log-streaming sink. **Requires `enabled: true`, `public_url`, and an OAuth client with the `log_streaming` scope.** |
| `streaming.max_body_bytes` | `0` | Cap on the **decompressed** request body. `0` selects a 64 MiB default; a negative value disables the cap. An over-cap POST is rejected with HTTP 413. When `ingress_wal.enabled=true` and this receiver is enabled, set an explicit value `> 0` and `<= 67108864` (64 MiB). |
| `streaming.max_concurrent_requests` | `0` | How many requests may buffer a body **at once**. `max_body_bytes` caps one body; this caps their sum, so N simultaneous in-limit POSTs cannot exceed the process memory budget. `0` selects a default of `4`; a negative value disables the limit. An over-limit POST is rejected with HTTP 503 + `Retry-After: 1`. Raise it only alongside the container/process memory limit — worst-case buffering is roughly this × `max_body_bytes`. |
| `streaming.routes` | `[]` | File-only multi-tailnet routes: `tailnet`, exact rooted `path`, `token` or `token_file`, optional `public_url`, and per-route `auto_configure`. Every route tailnet must match one configured `tailnets[]` runtime; paths and tailnets are unique. Non-empty routes replace legacy `path`/token/public-url/auto-configure identity. |

> **Validation:** `auto_configure: true` errors at startup unless both `streaming.enabled: true` and
> a non-empty `streaming.public_url` are set. Running the poller and this receiver for the same log
> type triggers a dual-ingestion **WARN**.
>
> Private HTTP log streaming also needs the receiver node shared to Tailscale's logging service,
> policy access for `logstream@tailscale`, and OAuth authority covering `device_invites` and
> `policy_file`. The exporter warns because it cannot prove those control-plane prerequisites.

> **The receiver fails closed without a token.** An empty `streaming.token` is accepted only when
> `streaming.listen` is a **loopback** address (`127.0.0.1`, `::1`, `localhost`). On any other bind —
> including the `:8088` default and any tailnet address — every request is refused with **HTTP 403**
> and `rejected{reason=auth_required}`, and a loud `ERROR` is logged at startup. An unauthenticated
> receiver on a reachable port lets anyone inject arbitrary flow/audit records, so it is refused
> rather than silently accepted. A tailnet address counts as reachable: every peer on the tailnet can
> connect to it. To run without a token, bind to loopback and put an authenticating proxy in front.

> **Resource limits.** Three internal, non-configurable caps bound what one request can cost, on top
> of `max_body_bytes` and `max_concurrent_requests`: at most **500,000 records** per request (a body of
> concatenated tiny objects would otherwise amplify ~50× into multi-GB of allocation — rejected with
> HTTP 413 + `rejected{reason=too_many_records}`); envelope unwrapping is bounded to **4 levels** of
> nesting (deeper wrappers are skipped and counted, the batch still succeeds); and a **30s** handler
> response deadline as defence in depth. The record and depth caps are the load-bearing controls — the
> deadline bounds the response, not the work.

> **Batch delivery is all-or-nothing.** The receiver parses and type-checks a whole POST before routing
> a single record, so a request is never acknowledged `200` after silently dropping part of its payload.
> A structurally corrupt/truncated body (`rejected{reason=malformed}`) or a record that classifies as a
> known type but fails typed decoding (`rejected{reason=decode_error}`, e.g. after an unhandled
> wire-format change) rejects the **whole** request with a `4xx` and emits nothing, so the sender
> retries rather than treating the loss as delivered. A record whose type is not recognised at all stays
> forward-compatible: it is skipped and counted (`skipped`) and the batch still succeeds. This replaces
> the earlier valid-prefix salvage — salvaging a truncated batch and ACKing it `200` was itself a
> durability hole.

---

## `webhook` — event webhook receiver

Optional receiver for real-time Tailscale events (HMAC-verified). **Off by default.**

| Key | Default | Description |
|-----|---------|-------------|
| `webhook.enabled` | `false` | Run the webhook receiver. |
| `webhook.listen` | `:8089` | Listen address. |
| `webhook.path` | `/tailscale/webhook` | Webhook path. |
| `webhook.secret` | `""` | Shared secret for HMAC-SHA256 verification. Empty is accepted only on a loopback `webhook.listen`; any network-reachable bind refuses requests with HTTP 403 before reading their bodies. Set via `TS2OTEL_WEBHOOK__SECRET`. |
| `webhook.secret_file` | `""` | Read `webhook.secret` from a file at startup instead of a literal value (Docker-secrets style). Setting both the value and the file is a config error. File content is whitespace-trimmed. |
| `webhook.tls.cert_file` / `.key_file` | `""` | Serve the webhook listener over native HTTPS when both readable files are set. Tailscale webhook endpoints require HTTPS. Leave both empty for an HTTPS reverse proxy; setting only one is a startup error. Certificate issuance/ACME is out of scope. |
| `webhook.tolerance` | `5m` | Allowed clock skew in **both** directions: a signed timestamp older than `now - tolerance` **or** newer than `now + tolerance` is rejected (the boundary itself is allowed). The two-sided check matters because a correctly signed but future-dated request would otherwise stay replayable until its future timestamp plus this window — turning a short skew allowance into a much longer one. `0` disables the timestamp check. |
| `webhook.max_body_bytes` | `0` | Cap on the **raw** request body read before signature verification. `0` selects a 1 MiB default; a negative value disables the cap. An over-cap POST is rejected with HTTP 413 and counted into `tailscale.webhook.rejected{reason="too_large"}`. Distinct from `streaming.max_body_bytes`, which caps a *decompressed* body. When `ingress_wal.enabled=true` and this receiver is enabled, set an explicit value `> 0` and `<= 67108864` (64 MiB). |
| `webhook.max_concurrent_requests` | `0` | How many requests may buffer a body **at once**, before the HMAC is verified. The signature covers the whole body, so buffering necessarily precedes authentication; `max_body_bytes` caps one body and this caps their sum, so unauthenticated senders cannot multiply it. `0` selects a default of `4`; a negative value disables the limit. An over-limit POST is rejected with HTTP 503 + `Retry-After: 1` and counted into `tailscale.webhook.rejected{reason="overloaded"}`. Worst-case buffering is roughly this × `max_body_bytes`. |
| `webhook.dedup_audit_events` | `false` | Best-effort: drop a webhook event already counted via the audit logs (shares a cross-source de-dup set with the audit processor). |
| `webhook.routes` | `[]` | File-only multi-tailnet routes: `tailnet`, `secret` or `secret_file`. Every route tailnet must match one configured `tailnets[]` runtime and is unique. A delivery is routed only when every event carries the same non-empty matching tailnet, before that route's HMAC is verified; non-empty routes replace legacy `path`/secret identity. |

---

## `self_observability` — the exporter's own telemetry

| Key | Default | Description |
|-----|---------|-------------|
| `self_observability.enabled` | `true` | Emit the exporter's own health metrics (`tailscale2otel.*`: scrape duration/success/errors, API requests/retries, cardinality, …). |
| `self_observability.instance_id` | `""` | Sets the `service.instance.id` resource attribute so multiple exporter instances are distinguishable. Empty falls back to the host name. In Kubernetes set via env: `TS2OTEL_SELF_OBSERVABILITY__INSTANCE_ID=$POD_NAME`. |

---

## `pii_filter` — PII / identifier redaction

Runtime opt-out toggles for each identifier category. All 13 categories default to **`true`**
(identifiers are emitted as-is). Set a category to `false` to drop those identifiers from metrics,
logs **and traces** at collection time. Gauges whose only meaningful identity is a redacted category
are suppressed entirely. Categories are independent — you can redact external IPs while keeping
Tailscale IPs, for example.

> **Traces are covered by the same policy** (since #212). Span attributes whose key maps to a
> disabled category are dropped before export, and redacted values are additionally scrubbed from the
> span **status description** and from **span-event** attributes — which is what keeps a full API URL
> out of `exception.message` when a request fails. Concretely, `endpoint_paths: false` removes
> `url.full` and `tailscale.endpoint` from API spans, and `hostnames: false` removes `host.name`.
> When no category is disabled the filter is not installed at all, so the default configuration pays
> nothing and exported spans are byte-identical.
>
> Two things traces do **not** filter: **span names** are safe by construction rather than by policy
> (`endpointLabel` already strips the tailnet segment and elides variable ID segments before the name
> is built), and **resource attributes** go through the separate existing resource gate. If you add a
> span name that interpolates an identifier, this filter will not catch it.

| Key | Default | Description |
|-----|---------|-------------|
| `pii_filter.emails` | `true` | User/actor login names (frequently email addresses, e.g. `user.name`). |
| `pii_filter.user_display_names` | `true` | Actor display (human) names (e.g. `user.full_name`). |
| `pii_filter.user_ids` | `true` | Numeric/opaque user IDs (e.g. `user.id`). |
| `pii_filter.hostnames` | `true` | Device and collector-host hostnames. |
| `pii_filter.node_ids` | `true` | Tailscale node IDs (e.g. the `nodeId` field on a device). |
| `pii_filter.tailscale_ips` | `true` | Tailscale overlay addresses: `100.64.0.0/10` (IPv4) and `fd7a:115c:a1e0::/48` (IPv6). |
| `pii_filter.internal_ips` | `true` | RFC 1918 / ULA / link-local addresses (non-Tailscale private ranges). |
| `pii_filter.external_ips` | `true` | Public/routable (non-private) IP addresses. |
| `pii_filter.service_addrs` | `true` | VIP service names from the Tailscale Services collector. |
| `pii_filter.endpoint_paths` | `true` | Tailscale API endpoint paths carried on self-observability metrics and spans. The path embeds the tailnet name and device IDs, so `false` drops `url.full` and `tailscale.endpoint` from exported spans and scrubs the URL out of span status descriptions and error events. |
| `pii_filter.network_topology` | `true` | Route CIDRs, split-DNS domains, and search paths from the DNS/ACL collectors. |
| `pii_filter.tailnet_name` | `true` | The tailnet identifier (e.g. `example.com` or the numeric tailnet ID). Disabling it also omits the universal `tailscale.tailnet` attribute from every metric, log, and span. **On the OTLP push path** each tailnet stays distinct (its own `service.instance.id` target). **On the Prometheus `/metrics` pull path** `tailscale_tailnet` is the only per-tailnet distinguisher, so disabling it in multi-tailnet mode makes the per-tailnet series identical — they collapse to one (the scrape still returns 200; a startup warning flags the lost breakdown). |
| `pii_filter.free_text_details` | `true` | Audit `old`/`new`/`details` payloads, target names, key descriptions, and posture values. Also governs **span status descriptions** — see the note below. |

> **Note:** these toggles gate emission only — they do not encrypt or hash values. Setting a
> category to `false` simply omits that class of identifier from emitted telemetry entirely.

> **Scope: exported telemetry only.** These toggles do not apply to the admin server's own
> surfaces. In particular the [flow view](flow-view.md) shows device names, addresses and users in
> full regardless of what is set here — it is local, in-memory introspection behind the admin
> token, not something the process sends anywhere.

> **`host:port` values are classified by their address, not their string shape.** Some IP-valued
> attributes — notably the node-metrics identity default `tailscale.node` — can appear as `host:port`
> (`100.64.0.1:5252`) or bracketed IPv6 (`[fd7a:115c:a1e0::1]:5252`). These are classified by the
> address portion alone, so they are gated by the matching `tailscale_ips` / `internal_ips` /
> `external_ips` toggle — never by `hostnames`. A value that merely looks like `host:port` but whose
> host segment is not a parseable IP (a genuine hostname such as `laptop-1:5252`) still falls back to
> `hostnames`, unchanged.
>
> Addresses are **normalised before classification**, so a category cannot be bypassed by changing the
> textual representation. Surrounding whitespace is trimmed, and an IPv4-mapped IPv6 address is
> unmapped first — `::ffff:100.64.0.1` is a Tailscale CGNAT address and is gated by `tailscale_ips`,
> not `external_ips`.

> **Unclassifiable values on IP-only attributes fail closed.** Three attributes are IP-valued by
> definition and have no hostname fallback: `source.address`, `destination.address`, and
> `tailscale.dns.resolver.address`. If one of them carries a non-empty value that will not parse as an
> address, and any IP category is disabled, the value is **dropped** rather than emitted — the filter
> cannot tell which category it would have belonged to, so it declines to guess. When every IP category
> is enabled, such a value is kept unchanged. The rejected value is never logged.

> **Span status descriptions follow the free-text policy.** Collector errors and recovered panic text
> reach span status descriptions, which are free text like an exception message. With
> `pii_filter.free_text_details` set to `false`, a status description is replaced unless it is one of a
> fixed set of code-defined strings (the receivers' reject reasons and the standard HTTP status texts),
> which pass through as-is. A description outside that set fails closed, so a newly added message loses
> diagnostic value until it is listed — never the reverse. Diagnosis survives regardless: the span's
> error **status code** and its bounded `error.type` attribute (`panic`, `timeout`, or `error`) are
> always kept. No status string is ever used as a metric label.

> **The filter covers log message bodies too, not only attributes.** A disabled category's identifiers
> are removed from log record **bodies** as well as from metric labels and log **attributes** — so an
> operator who turns a category off does not get it leaked back through the human-readable body. Two
> shapes of body are handled:
>
> - **Standalone free-text bodies** — a raw upstream message or error whose whole content is free text
>   (`tailscale.webhook.*`, `tailscale.device.tailnet_lock_error`, `tailscale.logstream.error`). When
>   `pii_filter.free_text_details` is `false` the body is replaced entirely with `[redacted]`; the
>   generic event name, severity, and low-cardinality attributes still convey what happened.
> - **Mixed bodies** — a body that embeds an identifier which is also carried as an attribute (flow
>   addresses on `tailscale.network.flow`, the key description on `tailscale.key.expiring`, the app name
>   on `tailscale.oauth_app.info`). Only the disabled-category value is masked, in place, wherever it
>   appears; the non-PII structure (transport, byte counts, scope counts, …) is preserved. A flow body's
>   Tailscale source address, for example, is masked when `tailscale_ips` is `false` but kept when it is
>   `true`, independent of the `hostnames` toggle.
>
> When every category is enabled (the default) bodies are byte-identical to before — redaction only
> engages once a category is turned off. A handful of bodies are generic by construction and so are
> never affected: `tailscale.acl.risky_rule` (`"Unrestricted ACL rule in section %q"` — the rule text
> lives only in the `tailscale.acl.rule` attribute) and `tailscale.key.scopes`
> (`"Tailscale key (%s) has %d scope(s)"` — the description lives only in the
> `tailscale.key.description` attribute); both attributes are still gated by
> `pii_filter.free_text_details`. (`tailscale.audit.details` is an unrelated audit-log attribute.)

---

## `admin` — admin HTTP server (probes + status page)

Always-off-by-default HTTP server exposing liveness/readiness probes plus an optional status page.
The status page surfaces operational metadata (collector health, cardinality, discovered nodes,
**redacted** config) but never secret values. Bind it to a tailnet/loopback address, not the public
internet.

| Key | Default | Description |
|-----|---------|-------------|
| `admin.enabled` | `true` | Run the admin server (`/healthz`, `/readyz`, and — unless disabled — the status page). |
| `admin.listen` | `:9091` | Listen address. For defense-in-depth bind to loopback (`127.0.0.1:9091`) or a tailnet IP. |
| `admin.landing_page` | `true` | Serve the human status page at `/` and machine-readable `/api/status.json`. |
| `admin.status_refresh_interval` | `5s` | How often the status page's JS re-polls `/api/status.json` to patch the live view. The 1s freshness ticker is independent. |
| `admin.auth.token` | `""` | When set, the status page and pprof require this token as the HTTP Basic password (browsers prompt) **or** `Authorization: Bearer <token>`. When **empty**, the status page and JSON APIs are served only on a **loopback** `admin.listen`; on any other bind they are refused with HTTP 403 (see below). `/healthz` and `/readyz` are never gated either way. Set via `TS2OTEL_ADMIN__AUTH__TOKEN`. |
| `admin.auth.token_file` | `""` | Read `admin.auth.token` from a file at startup instead of a literal value (Docker-secrets style). Setting both the value and the file is a config error. File content is whitespace-trimmed. |
| `admin.tls.cert_file` | `""` | HTTPS certificate for the admin server. Set together with `key_file` (both-or-neither); unset serves plain HTTP. |
| `admin.tls.key_file` | `""` | HTTPS key for `admin.tls.cert_file`. Both paths must exist and be readable at startup. |

> **The status page fails closed.** With no `admin.auth.token`, the landing page and every JSON API
> (`/`, `/api/status.json`, `/api/cardinality.json`, `/api/config.json`, `/api/rdns/purge`) are served
> only when `admin.listen` is a **loopback** address. On any other bind they are refused with **HTTP
> 403** (no `WWW-Authenticate` challenge — this is misconfiguration, not a missing credential, and a
> 401 would make browsers prompt for a password that does not exist), a startup **WARN** fires, and
> each refusal is counted with `reason=auth_required`.
>
> This matters because `/api/status.json` otherwise discloses, with no credential, every observed
> device's name, hostname, OS version, user, addresses and tags — across **all** tailnets in
> multi-tailnet/MSP mode — plus the OTLP endpoint, the TLS-insecure flag and the enabled collectors.
> A tailnet address counts as reachable, not loopback: every peer on the tailnet can connect to it.
>
> `/healthz` and `/readyz` are registered outside the auth wrapper and stay open on every bind, so
> container and Kubernetes probes are unaffected. Defaults are unchanged (`admin.listen: ":9091"`) —
> an existing deployment still starts; only the data-bearing endpoints go dark until you set a token
> or move to loopback.

---

## `flows` — built-in flow view

Keeps a bounded, pre-aggregated picture of recent tailnet traffic **in memory** and serves it at
`/flows` on the admin server: a topology graph, a timeline, top talkers/pairs/ports, identity
breakdowns and a recent-connection list. It is a convenience view, not a second telemetry pipeline —
OTLP remains the system of record, and the store is lost on restart.

| Key | Default | Description |
|-----|---------|-------------|
| `flows.enabled` | `true` | Build the store and serve `/flows`. Requires `admin.enabled` **and** `admin.landing_page`; with either off the store is not built at all and a startup advisory says so. |
| `flows.retention` | `6h` | How far back `/flows` can see, as a ring of one-minute buckets. Must be between `1m` and `24h` — this sizes process memory, not a database. |
| `flows.max_future_skew` | `5m` | Largest amount a record may lead the process clock and still enter the local view (`0`–`1h`). Rejection is counted by `tailscale.network.store.dropped`; OTLP emission is unchanged. |

Notes:

- **Both ingestion paths feed it.** The poll collector and the streaming receiver share one flow
  processor, so the view is complete regardless of `collectors.flowlogs.source`.
- **It obeys `pii_filter`.** The store sits behind the OTLP redactor, so it applies the same policy
  itself: disabling `pii_filter.emails` removes users from the view, `pii_filter.hostnames` removes
  device names, `pii_filter.tailscale_ips` removes the raw endpoints from the connection list.
- **It is bounded in every dimension.** Per-minute caps fold overflow into `__other__` and the page
  reports the truncation rather than implying complete coverage. Memory scales with `retention`, and
  in multi-tailnet mode each tailnet keeps its own store.
- **It never slows ingestion.** Recording is a short lock and a handful of map writes; there is no
  I/O and no backpressure onto the export path.

---

## `prometheus` — Prometheus pull endpoint

An opt-in, off-by-default `GET /metrics` endpoint on a **dedicated listener** (`prometheus.listen`,
default `:2112`). When enabled it attaches an additional `metric.Reader` (a per-provider Prometheus
registry) alongside the OTLP push path, so **both export paths are active at once** — Prometheus
scraping and OTLP push are independent and complementary; enabling one does not disable the other.

The endpoint is fully separate from the admin server (`admin.listen`) and must bind to a different
address. It serves only `GET /metrics`; no status page or probes are exposed here.

> **Multi-tailnet:** each tailnet's metrics carry a `tailscale_tailnet="<name>"` data-point label
> that keeps multi-tailnet series distinct at a shared `/metrics` endpoint. This label is what
> prevents a collision, so disabling `pii_filter.tailnet_name` removes it and the per-tailnet series
> collapse (the endpoint uses first-wins and still returns 200 rather than a 500 — see the
> `pii_filter.tailnet_name` note above).
> A `target_info` info metric is also emitted per provider. On **Grafana Cloud** the primary metrics
> path is OTLP (which uses the `target_info` join for resource attributes); the Prometheus endpoint
> is an additional pull-compatible path for existing Prometheus-only infrastructure. Per-tailnet
> identity (`tailscale.tailnet`, `tailscale2otel.provider`) is a signal-scoped metric/log/trace
> attribute rather than a resource attribute, so it needs no `target_info` join on either export path.

| Key | Default | Description |
|-----|---------|-------------|
| `prometheus.enabled` | `false` | Run the Prometheus pull endpoint on its own dedicated listener. Off by default. |
| `prometheus.listen` | `:2112` | Listen address for `/metrics`. Must differ from `admin.listen`. For defense-in-depth bind to loopback (`127.0.0.1:2112`) or a tailnet IP rather than a wildcard. |
| `prometheus.auth.token` | `""` | Optional shared secret gating `/metrics`. Accepted as the HTTP Basic password (any username) **or** `Authorization: Bearer <token>`. Empty = open (unauthenticated). Set via `TS2OTEL_PROMETHEUS__AUTH__TOKEN`. |

> **WARN (advisory):** if `prometheus.enabled` is `true` on a wildcard bind (empty host, e.g.
> `:2112`) with no `prometheus.auth.token`, a startup warning fires — the endpoint exposes every
> series (including device hostnames, flow identifiers, and tailnet name) to anyone who can reach
> the port. Set a token or bind to loopback/tailnet.

> **Validation:** every *enabled* HTTP listener — `admin.listen`, `prometheus.listen`,
> `streaming.listen`, and `webhook.listen` — must bind a distinct address. If any two enabled servers
> share an address the exporter errors at startup (otherwise only one would win the `net.Listen` race
> and the other would die silently).

### Prometheus `scrape_configs` snippet

```yaml
scrape_configs:
  - job_name: tailscale2otel
    static_configs:
      - targets: ["host:2112"]
    # If prometheus.auth.token is set:
    authorization:
      credentials: "<token>"
```

---

## `profiling` — pprof & Pyroscope

Optional continuous/on-demand profiling. Everything here is **off by default** and carries no
Tailscale data. The pprof handlers mount on the admin server.

| Key | Default | Description |
|-----|---------|-------------|
| `profiling.pprof.enabled` | `false` | Mount `net/http/pprof` handlers on the admin server so Alloy's `pyroscope.scrape` (or `go tool pprof`) can pull profiles. |
| `profiling.pyroscope.enabled` | `false` | Run the Pyroscope continuous-profiling push agent. |
| `profiling.pyroscope.server_address` | `""` | Pyroscope/Grafana Cloud Profiles URL. **Required when `pyroscope.enabled`.** |
| `profiling.pyroscope.basic_auth_user` | `""` | Grafana Cloud: the profiles instance ID (Basic-auth user). Set via `TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_USER`. |
| `profiling.pyroscope.basic_auth_password` | `""` | Grafana Cloud: an access-policy token with `profiles:write` (Basic-auth password). Set via `TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_PASSWORD`. |
| `profiling.pyroscope.tenant_id` | `""` | `X-Scope-OrgID` for multi-tenant servers (leave empty for Grafana Cloud). |
| `profiling.pyroscope.upload_rate` | `60s` | How often profiles are flushed to the server. |
| `profiling.pyroscope.tags` | `{}` | Extra static labels merged onto every profile, e.g. `{ env: prod }`. Must be set via YAML (map field). |
| `profiling.mutex_profile_fraction` | `5` | `runtime.SetMutexProfileFraction`; on by default (samples 1/5 of contention events). Applied only when `pprof` or `pyroscope` is enabled. `0` disables the mutex profile. |
| `profiling.block_profile_rate` | `100000` | `runtime.SetBlockProfileRate` (ns); on by default (records blocking events averaging ≥100µs). Applied only when `pprof` or `pyroscope` is enabled. `0` disables the block profile. |

> **Validation / advisories:**
> - `pprof.enabled` errors at startup unless `admin.enabled: true` **and** `admin.auth.token` is set
>   (heap/goroutine dumps can expose in-memory secrets, so pprof must not be served unauthenticated).
> - `pyroscope.enabled` errors at startup without `pyroscope.server_address`.
> - A `grafana.net` `server_address` with an empty `basic_auth_password` triggers a **WARN** —
>   Grafana Cloud Profiles requires the Basic-auth credentials.
> - When enabled, Pyroscope pushes the full profile set: CPU, memory (alloc/inuse), goroutines,
>   mutex/block contention, and **goroutine-leak**. Goroutine-leak relies on the Go
>   `goroutineleakprofile` runtime experiment, which the release binaries and container image are
>   built with (`GOEXPERIMENT=goroutineleakprofile`); a binary built without it silently omits that
>   one profile type. The mutex/block sampling rates above are applied only when a consumer (`pprof`
>   or `pyroscope`) is enabled.

---

## `version_checks` — outbound "is a newer release available?" checks

Optional outbound checks that compare the running build / device client versions against the latest
releases. Both sub-checks make external HTTPS calls and are **fail-open** (a failed or blocked fetch
silently emits nothing, never errors). Disable both for air-gapped deployments.

| Key | Default | Description |
|-----|---------|-------------|
| `version_checks.self.enabled` | `true` | Emit `tailscale2otel.update_available` (0/1 flag) comparing the running build to the latest tailscale2otel GitHub release. Independent of `self_observability.enabled`. |
| `version_checks.devices.enabled` | `true` | Emit per-device `tailscale.device.version_skew` (minor releases behind latest Tailscale stable), `tailscale.fleet.latest_version` (info gauge), and `tailscale.devices.outdated` (fleet count). Requires the `devices` collector; a WARN fires if the collector is disabled. |
| `version_checks.devices.outdated_minor_threshold` | `3` | A device at least this many minor releases behind the latest Tailscale stable counts toward `tailscale.devices.outdated`. Must be ≥ 1. |
| `version_checks.cache_ttl` | `1h` | How long a fetched "latest version" is cached before re-fetching. Must be ≥ 5m (validated). |
| `version_checks.timeout` | `10s` | Per-request timeout for the external version fetch. Must be > 0. |

> **Advisories:**
> - `version_checks.devices.enabled=true` with `collectors.devices.enabled=false` triggers a **WARN** —
>   the per-device version-skew metrics need the devices collector to run.

---

## `tracing` — OTEL traces pillar

Optional OTEL traces pillar. **Off by default.** When enabled, the exporter emits spans for its own
internal work — reusing `otlp.*` for the endpoint/protocol/headers/TLS (no separate trace endpoint).
When `tracing.enabled` is true, the metric exemplar filter also flips to trace-based, so the
`tailscale2otel.api.duration` latency histogram carries trace exemplars that link directly to the
corresponding API request span.

| Key | Default | Description |
|-----|---------|-------------|
| `tracing.enabled` | `false` | Emit spans. When true, also enables trace-based exemplars on `tailscale2otel.api.duration`. Set via `TS2OTEL_TRACING__ENABLED`. |
| `tracing.sampler` | `parentbased_always_on` | Head sampler. One of `always_on`, `always_off`, `traceidratio`, `parentbased_always_on`, `parentbased_traceidratio`. Mirrors `OTEL_TRACES_SAMPLER` semantics. Set via `TS2OTEL_TRACING__SAMPLER`. |
| `tracing.sampler_arg` | `1.0` | Sample ratio in `[0,1]` for the `*traceidratio` samplers; ignored by the others. Set via `TS2OTEL_TRACING__SAMPLER_ARG`. |

> **Advisories:**
> - `tracing.enabled=true` with `sampler_arg=0` and a `*traceidratio` sampler triggers a **WARN** —
>   no spans will be recorded at ratio 0.

### Span names and key attributes

When `tracing.enabled` is true the following spans are emitted:

| Span name | Emitted by | Key attributes |
|---|---|---|
| `scrape <collector>` | Scheduler (one per scrape cycle) | `tailscale.collector` (collector name); span status `Error` on failure |
| `tailscale.api <endpoint>` | Tailscale API transport (one per logical request) | `url.full` (full path incl. tailnet/device ID — useful for "which device's request was slow/failed"), `http.request.method`, `http.response.status_code`, `http.request.resend_count`, `server.address`; retry events carry `attempt`/`status`/`sleep_ms` |
| `stream.receive` | HEC stream receiver (one per HTTP request) | `tailscale.stream.flows`, `tailscale.stream.audits`, `tailscale.stream.skipped`, `http.request.body.size`; span status `Error` on auth/parse failure |
| `webhook.receive` | Webhook receiver (one per HTTP request) | `tailscale.webhook.events`, `http.request.body.size`; span status `Error` on auth/parse failure |

**PII note:** Spans are unaggregated (like logs), so useful identifiers such as the tailnet name and device ID
appear on `url.full` by design — they help operators answer "which device's request failed or was slow?"
Tier-1 secrets (auth headers/tokens, OAuth/webhook/logstream credentials) and large response/request
bodies are never attached. Per-record source/destination IPs are not put on receiver spans; they flow to
the flow/audit log records instead.
