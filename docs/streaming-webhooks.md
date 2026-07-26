---
title: Streaming, Object Store & Webhooks
description: Ingest Tailscale flow and audit signals through push receivers or durable object-store exports instead of polling
tags:
  - Streaming
---

# Streaming & Webhooks

`tailscale2otel` can receive logs in real time rather than polling, or read network flow exports from
an object store. Two optional receivers cover the Tailscale push mechanisms: a
**Splunk-HEC-compatible stream receiver** for network flow and configuration audit logs, and an
**HMAC-verified webhook receiver** for real-time Tailscale events. Both are off by default.

## Poll vs. stream: pick one path per log type

`flowlogs` accepts `poll`, `stream`, `objectstore`, or `both`. `auditlogs` accepts `poll`, `stream`,
or `both`:

| `source` | Description |
|---|---|
| `poll` (default) | `tailscale2otel` pulls logs from the Tailscale API on a schedule |
| `stream` | Tailscale pushes logs to the built-in HEC receiver; the windowing fields are ignored |
| `objectstore` (flow only) | `tailscale2otel` reads Tailscale's partitioned flow-log export from an S3-compatible bucket |
| `both` | Polls *and* accepts the stream — **discouraged** |

!!! warning "Running both paths double-counts"
    Setting `source: both` — or enabling the streaming receiver while a collector still uses `source: poll` — means the same log record can be delivered and emitted twice. Cross-source deduplication is only a best-effort failsafe; the exporter logs a **WARN** at startup when it detects both paths are active for the same log type. Pick exactly one method per log type.

Streamed log records pass through the same shared processors as polled records, so they produce identical OTEL metrics and log events regardless of which path delivers them.

## Object-store durability and failed gaps

Object-store flow records use the same processor as poll and stream. Listing is bounded and resumes
per day prefix; after reaching the end it wraps so a late key sorted before the previous position is
still found.

The delivery boundary is at-least-once. With the file checkpoint store, successful object identities,
listing progress, and failed-object gaps are persisted together and survive restart. Transient GET
and stream-read failures retry with exponential backoff capped at one hour. Invalid gzip/zstd framing
is deterministic for an immutable object and enters quarantine immediately. An in-memory checkpoint
does not provide restart durability.

Malformed JSON and semantically invalid rows are record-level failures: valid rows in the same
NDJSON object are accepted and the object can complete. GET, decompressor, and scanner failures are
object-level gaps. A scanner can fail after it emitted valid rows, so retry may duplicate those rows
after restart until object processing is atomic. OTLP/backend acknowledgement is outside this
boundary.

Gap diagnostics do not expose bucket keys. Logs use a 12-character SHA-256 object digest, and these
gauges have no attributes:

| Metric | Description |
|---|---|
| `tailscale2otel.objectstore.gaps` | Pending plus quarantined object gaps |
| `tailscale2otel.objectstore.gap.oldest.age` | Age in seconds of the oldest unresolved gap |
| `tailscale2otel.objectstore.gap.healthy` | `1` only when no gaps remain |

A quarantined object is not retried automatically and keeps gap health at `0`. To acknowledge it,
stop the process and remove only its
`objectstore/v1/<tailnet>/<provider>/<signal>/<feed>/gap/...` row from the owner-only checkpoint JSON.
The paired `.../seen/...` row prevents another fetch. The tailnet is base64url-encoded and the feed is
a digest, so raw provider identifiers do not enter checkpoint paths. To replace the object at the same
key and retry, remove both the gap row and its paired seen row before restarting. The first startup
after upgrade atomically migrates the previous `objectstore.flowlogs.*` rows into this scoped layout.

## Splunk-HEC stream receiver

When `streaming.enabled: true`, `tailscale2otel` binds a Splunk-HEC-compatible HTTP endpoint that Tailscale's log-streaming feature can POST to. Configure the matching collectors to use `source: stream`.

```yaml
streaming:
  enabled: true
  listen: ":8088"                    # bind address for the receiver
  path: /services/collector/event    # HEC endpoint path (Tailscale POSTs here)
  token: ""                          # set via TS2OTEL_STREAMING__TOKEN (empty on a non-loopback listen = every request REFUSED with 403)
  public_url: ""                     # externally reachable URL; required only when auto_configure: true
  tls:
    cert_file: ""                    # HTTPS cert (Tailscale requires HTTPS; `tailscale cert` works for private endpoints)
    key_file: ""                     # HTTPS key
  decompress: auto                   # auto | gzip | zstd | none
  auto_configure: false              # register this receiver as the tailnet's log-streaming sink on startup
  max_body_bytes: 0                  # cap on the decompressed body; 0 = 64 MiB default, negative = unlimited

collectors:
  flowlogs:
    enabled: true
    source: stream
    log_mode: per_connection          # per_connection | per_record | off (applies to both poll and stream)

  auditlogs:
    enabled: true
    source: stream
```

**What the receiver ingests.** Each POST body contains one or more log records. The receiver classifies each record by shape: records with a `nodeId` and traffic fields are decoded as flow logs; records with an `actor` and `action` are decoded as configuration audit events. Unrecognized records are counted as skipped.

**Body decompression.** With `decompress: auto` (the default) the receiver reads the `Content-Encoding` header and decompresses gzip or zstd bodies automatically.

**Auto-configure.** Setting `auto_configure: true` (with `enabled: true`, a `public_url`, and an OAuth client carrying the `log_streaming` scope) causes `tailscale2otel` to register itself as the tailnet's log-streaming sink on startup.

`public_url` is preflighted before any API write. HTTPS may name a public endpoint. Plain HTTP is
accepted only for a private shared-node hostname/FQDN or IPv6 literal; every IPv4 literal is rejected.
The exporter warns on accepted HTTP endpoints because it cannot prove the other upstream
prerequisites: node sharing, policy access for `logstream@tailscale`, and OAuth authority covering
`device_invites` and `policy_file`. The configured path and query are passed through unchanged.

### Multi-tailnet routes

For a `tailnets:` deployment, configure a file-only `streaming.routes` list instead of the legacy
`path`, `token`, `public_url`, and `auto_configure` identity fields. Each route has an exact rooted
`path`, one `tailnet`, one token source, optional `public_url`, and its own `auto_configure` flag.
The path picks the matching runtime before token verification; an unknown path or token never falls
back to another tailnet. Auto-configure iterates only routes that opt in.

Webhook routes are file-only `webhook.routes` entries with `tailnet` and `secret`/`secret_file`.
The shared webhook endpoint reads a bounded request only far enough to require one identical,
non-empty tailnet on every event, selects that route, and then verifies its HMAC. Missing, unknown,
or mixed-tailnet batches are rejected without processor, dedup, or receiver-telemetry effects.

!!! warning "`auto_configure` overwrites the existing sink"
    When `auto_configure: true`, the service **overwrites** whatever log-streaming sink is already configured for the tailnet. Never enable it against a tailnet whose streaming configuration you do not intend to replace.

**Self-observability.** The receiver emits these metrics for its own health:

| Metric | Description |
|---|---|
| `tailscale.stream.records` | Records successfully routed to a processor (`type`: `flow` or `audit`) |
| `tailscale.stream.rejected` | Whole requests not ingested (`reason` includes authentication/exposure failures, size/admission limits, malformed/decode failures, and `semantic_invalid`) |
| `tailscale.stream.decode_errors` | Records classified as a known type but whose typed decode failed |
| `tailscale.stream.skipped` | Records extracted from an otherwise-valid request body but never routed to a processor (`reason`: `unclassified` = matched neither the flow nor audit shape, `unwrap_drop` = a non-object value was dropped while unwrapping the envelope before classification) |
| `tailscale.stream.inflight` | In-flight HTTP requests currently being processed (UpDownCounter) — useful for backpressure monitoring |
| `tailscale.stream.request.duration` | Wall-clock duration of HEC request handling, in seconds (histogram) |

All accepted ingestion paths also feed bounded cross-source freshness telemetry:
`tailscale2otel.ingest.event.age`, `capture.delay`, `last_event_timestamp`, and
`timestamp_skew`, keyed only by `source` and `signal`. Event time remains distinct from upstream
capture/observation time and local acceptance time. The Events & Logs dashboard shows current
freshness plus p95 event/capture delay; the Grafana-managed stale-data rule is paused by default
because webhook and audit sources can be legitimately quiet.

## Webhook receiver

When `webhook.enabled: true`, `tailscale2otel` binds an HTTP endpoint that receives real-time Tailscale event notifications. Each event is emitted as an OTEL log record (with severity INFO or WARN depending on event type) and increments a `tailscale.webhook.events` counter keyed by event type. The receiver also emits `tailscale.webhook.rejected` (deliveries rejected, e.g. bad HMAC, keyed by `reason` — the signal to watch when a secret or timestamp tolerance is misconfigured), `tailscale.webhook.inflight` (in-flight requests, UpDownCounter), and `tailscale.webhook.request.duration` (handler wall-clock time, histogram) for backpressure and latency monitoring.

```yaml
webhook:
  enabled: false
  listen: ":8089"                    # bind address for the webhook receiver
  path: /tailscale/webhook           # endpoint path Tailscale POSTs events to
  secret: ""                         # HMAC-SHA256 secret (set via TS2OTEL_WEBHOOK__SECRET; empty works only on loopback, otherwise requests get 403)
  tls:
    cert_file: ""                    # native HTTPS certificate; set with key_file
    key_file: ""                     # native HTTPS key; leave both empty behind an HTTPS reverse proxy
  tolerance: 5m                      # reject signed timestamps older than this (replay window); "0" disables
  dedup_audit_events: false          # best-effort, bidirectional: audit and webhook events sharing a normalized key are deduplicated against a shared set, first-arrival-wins (either copy can be the one that survives)
```

!!! note "No `auto_configure` for webhooks"
    Unlike `streaming.auto_configure`, there is no equivalent for the webhook receiver — the
    Tailscale API has no webhook-registration endpoint. Webhooks must be registered manually in the
    [Tailscale admin console](https://login.tailscale.com/admin/webhooks), pointed at this
    receiver's `listen`/`path`, with the same secret configured on both sides.

**HMAC verification.** Tailscale signs each webhook request with a `Tailscale-Webhook-Signature` header containing a Unix timestamp and an HMAC-SHA256 signature (`t=<seconds>,v1=<hex>`). The receiver verifies the signature by computing `HMAC-SHA256(secret, "<seconds>.<body>")` and comparing it in constant time. The parser tolerates repeated `v1` entries for forward compatibility, but the public sender contract does not promise old/new-secret overlap during rotation; update the receiver secret immediately after rotating it in Tailscale. The `tolerance` field (default `5m`) rejects requests whose timestamp is outside the allowed past or future clock-skew window.

**HTTPS.** Tailscale requires webhook destinations to use HTTPS on port 80 or 443. Set
`webhook.tls.cert_file` and `webhook.tls.key_file` together to terminate TLS in the exporter, or
leave both empty and terminate HTTPS at a reverse proxy. The exporter validates configured files at
startup but does not issue or renew certificates.

For version-1 events, the emitted log carries a typed allowlist of documented structured fields:
node ID, device name, owner/actor, admin URL, key expiration, user, and old/new roles where the
event type defines them. Unknown, null, or wrong-typed values are omitted. Policy `oldPolicy` and
`newPolicy` blobs are never emitted. The identity and URL fields pass through the same `pii_filter`
categories as equivalent collector data.

Authenticated events are also deduplicated per event using a canonical JSON digest. The in-memory
set is bounded to 65,536 entries and retains them for 25 hours, covering the documented 24-hour
retry horizon. A duplicate still receives HTTP 200 and increments `tailscale.webhook.duplicates`;
the set is intentionally not persisted, so a process restart can admit a retry again. This is
separate from the optional audit/webhook cross-source deduplication.

**Event severity.** Most event types are emitted at INFO. The following are emitted at WARN: `nodeKeyExpired`, `nodeKeyExpiringInOneDay`, `nodeNeedsApproval`, `userNeedsApproval`, `nodeNeedsAuthorization`, `nodeNeedsSignature`, `nodeDeleted`, `webhookDeleted`, `userSuspended`, `userDeleted`.

## Security notes

!!! danger "Empty receiver credentials are loopback-only"
    An empty `webhook.secret` skips HMAC verification only on a loopback `webhook.listen`. On any
    other bind the receiver refuses every request with HTTP 403 before reading its body. The HEC
    receiver applies the same fail-closed boundary to an empty `streaming.token`. Use an
    authenticating proxy if either receiver must listen on loopback without its native credential.

    These values are set most safely via environment variables (`TS2OTEL_STREAMING__TOKEN`, `TS2OTEL_WEBHOOK__SECRET`). A mistyped variable name (e.g. `TS2OTEL_WEBHOOK__SECRT`) leaves the value empty rather than failing loudly — the startup log WARNs on any `TS2OTEL_*` variable that matches no config key, so double-check that the credentials are actually set.

**TLS.** Tailscale requires HTTPS for public log-streaming sinks and for webhook destinations. Use
the paired `streaming.tls.*` or `webhook.tls.*` files for native TLS, or put the corresponding
listener behind an HTTPS reverse proxy. A certificate obtained via `tailscale cert` works well for
private tailnet endpoints.

**Data sensitivity.** Flow logs and audit logs carry source/destination IP addresses and ports, device names, and user identities. See [Configuration](configuration.md) for the `cardinality.*` knobs that shape which fields appear on flow metrics, [Security](security.md) for the PII-redaction categories, and [`SECURITY.md` on GitHub](https://github.com/rknightion/tailscale2otel/blob/main/SECURITY.md) for the full data-handling and vulnerability-reporting notes.

## Receiver source

Both receivers are small, self-contained packages if you want to check the parsing or verification
behaviour yourself:

- [`internal/stream`](https://github.com/rknightion/tailscale2otel/tree/main/internal/stream) — the Splunk-HEC-compatible log-streaming receiver
- [`internal/webhook`](https://github.com/rknightion/tailscale2otel/tree/main/internal/webhook) — HMAC-verified webhook receiver
- [`internal/dedup`](https://github.com/rknightion/tailscale2otel/tree/main/internal/dedup) — the cross-source de-duplication failsafe

Tailscale does not publicly document the exact HEC payload envelope, so the receiver parses
defensively. If you capture an envelope that fails to decode, please
[open an issue](https://github.com/rknightion/tailscale2otel/issues/new) — that is how the decoder
gets better.
