---
title: Streaming & Webhooks
description: Ingest Tailscale flow/audit logs via the Splunk-HEC stream receiver or HMAC-verified webhooks instead of polling
tags:
  - Streaming
---

# Streaming & Webhooks

`tailscale2otel` can receive logs in real time rather than polling. Two optional receivers cover the two Tailscale push mechanisms: a **Splunk-HEC-compatible stream receiver** for network flow and configuration audit logs, and an **HMAC-verified webhook receiver** for real-time Tailscale events. Both are off by default.

## Poll vs. stream: pick one path per log type

The `flowlogs` and `auditlogs` collectors each accept a `source` field with three options:

| `source` | Description |
|---|---|
| `poll` (default) | `tailscale2otel` pulls logs from the Tailscale API on a schedule |
| `stream` | Tailscale pushes logs to the built-in HEC receiver; the windowing fields are ignored |
| `both` | Polls *and* accepts the stream — **discouraged** |

!!! warning "Running both paths double-counts"
    Setting `source: both` — or enabling the streaming receiver while a collector still uses `source: poll` — means the same log record can be delivered and emitted twice. Cross-source deduplication is only a best-effort failsafe; the exporter logs a **WARN** at startup when it detects both paths are active for the same log type. Pick exactly one method per log type.

Streamed log records pass through the same shared processors as polled records, so they produce identical OTEL metrics and log events regardless of which path delivers them.

## Splunk-HEC stream receiver

When `streaming.enabled: true`, `tailscale2otel` binds a Splunk-HEC-compatible HTTP endpoint that Tailscale's log-streaming feature can POST to. Configure the matching collectors to use `source: stream`.

```yaml
streaming:
  enabled: true
  listen: ":8088"                    # bind address for the receiver
  path: /services/collector/event    # HEC endpoint path (Tailscale POSTs here)
  token: ""                          # set via TS2OTEL_STREAMING__TOKEN (empty = unauthenticated, WARN)
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

!!! warning "`auto_configure` overwrites the existing sink"
    When `auto_configure: true`, the service **overwrites** whatever log-streaming sink is already configured for the tailnet. Never enable it against a tailnet whose streaming configuration you do not intend to replace.

**Self-observability.** The receiver emits these metrics for its own health:

| Metric | Description |
|---|---|
| `tailscale.stream.records` | Records successfully routed to a processor (`type`: `flow` or `audit`) |
| `tailscale.stream.rejected` | Requests/records not ingested (`reason`: `auth`, `unparsable`, or `too_large`) |
| `tailscale.stream.decode_errors` | Records classified as a known type but whose typed decode failed |
| `tailscale.stream.skipped` | Records extracted from an otherwise-valid request body but never routed to a processor (`reason`: `unclassified` = matched neither the flow nor audit shape, `unwrap_drop` = a non-object value was dropped while unwrapping the envelope before classification) |
| `tailscale.stream.inflight` | In-flight HTTP requests currently being processed (UpDownCounter) — useful for backpressure monitoring |
| `tailscale.stream.request.duration` | Wall-clock duration of HEC request handling, in seconds (histogram) |

## Webhook receiver

When `webhook.enabled: true`, `tailscale2otel` binds an HTTP endpoint that receives real-time Tailscale event notifications. Each event is emitted as an OTEL log record (with severity INFO or WARN depending on event type) and increments a `tailscale.webhook.events` counter keyed by event type. The receiver also emits `tailscale.webhook.rejected` (deliveries rejected, e.g. bad HMAC, keyed by `reason` — the signal to watch when a secret or timestamp tolerance is misconfigured), `tailscale.webhook.inflight` (in-flight requests, UpDownCounter), and `tailscale.webhook.request.duration` (handler wall-clock time, histogram) for backpressure and latency monitoring.

```yaml
webhook:
  enabled: false
  listen: ":8089"                    # bind address for the webhook receiver
  path: /tailscale/webhook           # endpoint path Tailscale POSTs events to
  secret: ""                         # HMAC-SHA256 secret (set via TS2OTEL_WEBHOOK__SECRET; empty = verification SKIPPED, WARN)
  tolerance: 5m                      # reject signed timestamps older than this (replay window); "0" disables
  dedup_audit_events: false          # best-effort, bidirectional: audit and webhook events sharing a normalized key are deduplicated against a shared set, first-arrival-wins (either copy can be the one that survives)
```

!!! note "No `auto_configure` for webhooks"
    Unlike `streaming.auto_configure`, there is no equivalent for the webhook receiver — the
    Tailscale API has no webhook-registration endpoint. Webhooks must be registered manually in the
    [Tailscale admin console](https://login.tailscale.com/admin/webhooks), pointed at this
    receiver's `listen`/`path`, with the same secret configured on both sides.

**HMAC verification.** Tailscale signs each webhook request with a `Tailscale-Webhook-Signature` header containing a Unix timestamp and one or more HMAC-SHA256 signatures (`t=<seconds>,v1=<hex>`). The receiver verifies the signature by computing `HMAC-SHA256(secret, "<seconds>.<body>")` and comparing against each `v1` value using a constant-time comparison. Multiple `v1` entries are accepted — a match against any one is sufficient, which supports secret rotation without downtime. The `tolerance` field (default `5m`) rejects requests whose timestamp is older than the window, limiting replay attacks.

**Event severity.** Most event types are emitted at INFO. The following are emitted at WARN: `nodeKeyExpired`, `nodeKeyExpiringInOneDay`, `nodeNeedsApproval`, `userNeedsApproval`, `nodeNeedsAuthorization`, `nodeNeedsSignature`, `nodeDeleted`, `webhookDeleted`, `userSuspended`, `userDeleted`.

## Security notes

!!! danger "Empty credentials silently disable auth"
    Both receivers use **opt-in authentication**: an empty `streaming.token` disables HEC receiver authentication, and an empty `webhook.secret` skips HMAC verification entirely — unauthenticated POSTs are then accepted. Always set these when exposing a receiver, especially on a wildcard bind or without TLS.

    These values are set most safely via environment variables (`TS2OTEL_STREAMING__TOKEN`, `TS2OTEL_WEBHOOK__SECRET`). A mistyped variable name (e.g. `TS2OTEL_WEBHOOK__SECRT`) leaves the value empty rather than failing loudly — the startup log WARNs on any `TS2OTEL_*` variable that matches no config key, so double-check that the credentials are actually set.

**TLS.** Tailscale requires HTTPS for the log-streaming sink. Use `streaming.tls.cert_file` and `streaming.tls.key_file` to serve the HEC receiver over HTTPS. A certificate obtained via `tailscale cert` works well for private tailnet endpoints.

**Data sensitivity.** Flow logs and audit logs carry source/destination IP addresses and ports, device names, and user identities. See [Configuration](configuration.md) for the `cardinality.*` knobs that shape which fields appear on flow metrics, and `SECURITY.md` for the full data-handling notes.
