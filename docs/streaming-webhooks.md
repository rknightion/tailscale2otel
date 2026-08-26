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

The default is API polling. Choose streaming when low latency matters and Tailscale can reach your
receiver, object storage when you need a durable batch source or backfill, and webhooks for tailnet
events rather than flow or audit delivery. The matrix below carries the exact compatibility and
durability rules.

## Ingestion paths: the compatibility matrix

This table is the authoritative answer to "which paths can carry which signal, and what does each one
guarantee". Everything else on this page elaborates one of its rows.

| Path | `flowlogs` | `auditlogs` | Tailscale events | Delivery | Durability boundary | Max backfill |
|---|:---:|:---:|:---:|---|---|---|
| `poll` (default) | ✓ | ✓ | — | at-least-once | checkpointed high-water mark; `replay_overlap` deliberately revisits completed time | `initial_lookback`, bounded by the API's own retention |
| `stream` (Splunk-HEC receiver) | ✓ | ✓ | — | at-least-once, **at Tailscale's discretion** | none by default; opt-in bounded ingress WAL (`ingress_wal`) makes local acceptance durable | none — push only, no history |
| `objectstore` | ✓ | ✓ | — | at-least-once | durable cursor, per-prefix scan positions, seen set and failed-object gaps, all in one checkpoint transaction | **14 day partitions** (`layout: partitioned`) or unbounded (`layout: flat`) — see below |
| `webhook` receiver | — | — | ✓ | at-least-once, HMAC-verified | none by default; same opt-in WAL | none |
| `both` | ✓ | ✓ | — | **double-counts** | as `poll` | as `poll` |

"✓" means the path carries that signal today. `objectstore` covers both log types because Tailscale
publishes each as its own export; `webhook` carries only the real-time event feed, which is a
different data source rather than a third delivery route for logs.

**Acknowledgement stops at this process.** Every path above is at-least-once *into* the exporter. What
happens after — whether the OTLP gateway accepted the batch, whether the backend stored it — is
outside every boundary in the table. A checkpoint that has advanced means "these records were handed
to the emitter", not "these records are queryable".

The network and configuration exports are **separate objects in separate key spaces**, so each signal
reads its own destination: `collectors.flowlogs.objectstore` and `collectors.auditlogs.objectstore`
(or, with a `tailnets:` list, each entry's `objectstore.flow` and `objectstore.audit`). Nothing is
inherited between them, and two destinations this process reads may not name the same bucket **and**
prefix — sharing one feed would have each engine fetch every object and then fail to decode the other
signal's records. One bucket with a prefix per signal is the normal arrangement.

!!! warning "Running both paths double-counts"
    Setting `source: both` — or enabling the streaming receiver while a collector still uses `source: poll` — means the same log record can be delivered and emitted twice. Cross-source deduplication is only a best-effort failsafe; the exporter logs a **WARN** at startup when it detects both paths are active for the same log type. Pick exactly one method per log type.

Streamed log records pass through the same shared processors as polled records, so they produce identical OTEL metrics and log events regardless of which path delivers them.

## Maximum effective backfill

**Under the default `layout: partitioned`, the furthest back object-store ingestion can ever reach is
14 day partitions — today plus the previous 13 days.** That is a permanent ceiling, not a per-cycle
one, and it does not depend on `initial_lookback`:

- one cycle enumerates at most 14 `YYYY/MM/DD/` partitions, walking **backwards** from the newest so a
  capped span keeps the recent days rather than getting stuck on the oldest;
- the cursor only ever moves forward, so the days beyond the cap are not enumerated on a later cycle
  either.

Setting `initial_lookback: 720h` against a bucket holding 30 days of exports therefore ingests the
most recent 14 partitions and **silently ignores the other 16**. Nothing reports it: the older objects
are never listed, so they produce no gap, no `skipped` count and no metric. `Warnings()` flags an
`initial_lookback` beyond the ceiling at startup for exactly this reason — it is the only place an
operator finds out.

To reach further back, use **`layout: flat`**, which has no day partitions to cap. It lists the prefix
itself and resumes from a durable scan position, so it walks arbitrarily far back over as many cycles
as it takes, bounded per cycle by `max_objects`. The cost is more LIST requests and higher discovery
latency for new objects — see [export layouts](configuration.md#export-layouts-partitioned-vs-flat).
For a one-off historical load, `flat` against a copy of the export is the supported route; the
partitioned reader is for steady-state ingestion.

Two notes on what "backfill" can mean here at all. Object-store history is bounded by whatever the
bucket's own lifecycle policy retains, and pushing old records into a backend does not make them
visible if that backend's retention window has already passed them — Grafana Cloud's flow-log retention
is why a longer historical load was declined as unsupported in #287.

## Object-store durability and failed gaps

Object-store records use the same processor as poll and stream, for both signals. Listing is bounded and resumes
per day prefix; after reaching the end it wraps so a late key sorted before the previous position is
still found.

The delivery boundary is at-least-once. With the file checkpoint store, successful object identities,
listing progress, and failed-object gaps are persisted together and survive restart. Transient GET
and stream-read failures retry with exponential backoff capped at one hour. Invalid gzip/zstd framing
is deterministic for an immutable object and enters quarantine immediately. An in-memory checkpoint
does not provide restart durability.

Malformed JSON and semantically invalid rows are record-level failures: valid rows in the same
NDJSON object are accepted and the object can complete. GET, decompressor, and scanner failures are
object-level gaps.

**One object is all-or-nothing.** Every row is decoded and validated first, and only then is the whole
set committed to the shared processor, so a scanner or read failure part-way through an object emits
**nothing at all** — not a partial prefix of its rows. The retry after restart therefore replays the
object exactly once rather than duplicating rows it had already emitted. (An earlier version of this
page said the opposite; it predated the two-phase prepare/commit engine.
`TestCollect_LateScannerErrorEntersDurableGapPath` and the `atomicity_test.go` suite are the
guarantee.)

What remains at-least-once is the object as a unit: a crash between emission and the checkpoint write
replays that whole object. And OTLP/backend acknowledgement stays outside this boundary entirely — a
committed row means "handed to the emitter", never "stored by the backend".

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
capture/observation time and local acceptance time. The **Ingestion** tab of the
`tailscale2otel-health` dashboard shows current freshness plus p95 event/capture delay; the
Grafana-managed stale-data rule is paused by default
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

## Exposing the receivers on Kubernetes (Ingress / Gateway API)

The Helm chart ships **no inbound resource by default** (see `deploy/CLAUDE.md`'s "No Kubernetes
Service" note). Since chart 0.21.0 it offers opt-in Ingress and Gateway API `HTTPRoute` objects, but
**only for the two receiver listeners, `streaming` and `webhook`**. **Admin and Prometheus never get
one, at any value** — they are introspection and scrape surfaces, and publishing either to the
internet is never the right default, so the chart does not offer that as a one-line mistake. Reach
those two the way `deploy/CLAUDE.md` already documents: a `Service` you manage yourself plus your own
`Ingress`, or a `PodMonitor`/`ServiceMonitor` for Prometheus scraping.

Both paths require a backing `Service` first (`service.streaming.enabled` /
`service.webhook.enabled` — see [Configuration](configuration.md)), and both refuse to render if the
listener has no credential configured (`config.streaming.token` / `config.webhook.secret`, or their
`_file` siblings): publishing an unauthenticated receiver to the internet is the exact failure mode
this feature exists to prevent, and `helm template` fails with an actionable message naming the
offending key rather than silently producing a broken or insecure manifest.

### Generic Ingress

```yaml
service:
  webhook:
    enabled: true
ingress:
  webhook:
    enabled: true
    host: webhook.example.com          # required — a host-less rule is a catch-all
    path: /
    pathType: Prefix
    tls:
      enabled: true                    # the default; Tailscale webhooks are HTTPS-only
      secretName: webhook-tls          # a pre-existing Secret, OR use an annotation instead (below)
```

`ingress.<listener>.tls.enabled` defaults to `true` and requires **either** a `secretName` **or** at
least one annotation (typically a cert-manager issuer, below) — otherwise the rendered Ingress would
claim TLS with nothing configured to terminate it, and `helm template` fails and says so. Setting
`tls.enabled: false` is allowed, but only makes sense when a mesh or sidecar upstream of the Ingress
controller terminates TLS for you: **Tailscale will not deliver webhooks, and will not accept a
streaming destination, over plaintext.**

### cert-manager automated TLS

```yaml
ingress:
  webhook:
    enabled: true
    host: webhook.example.com
    className: nginx
    annotations:
      cert-manager.io/cluster-issuer: letsencrypt-prod
    tls:
      enabled: true
      secretName: webhook-tls          # cert-manager provisions and populates this Secret
```

The annotation alone satisfies the TLS guard even with `secretName` left empty, but naming a
`secretName` is how cert-manager's ingress-shim knows where to write the certificate it issues.

### Gateway API (`HTTPRoute`)

Requires the Gateway API CRDs and a `Gateway` object already provisioned in-cluster — this chart does
not create either, the same way it never creates a `ServiceMonitor`'s Prometheus Operator CRDs.

```yaml
service:
  streaming:
    enabled: true
gateway:
  streaming:
    enabled: true
    parentRefs:                        # required — at least one, naming the Gateway to attach to
      - name: my-gateway
        namespace: gateway-system
        sectionName: https
    hostnames:
      - streaming.example.com
    path: /
```

An empty `parentRefs` list renders an `HTTPRoute` that attaches to nothing, so it is rejected at
render time exactly like the other guards above.

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
