---
title: Troubleshooting
description: Diagnose tailscale2otel authentication, API, configuration, collection, and OTLP export failures with concrete checks.
tags:
  - Troubleshooting
---

# Troubleshooting

This page covers the most common `tailscale2otel` problems, their root causes, and concrete fixes.
All config keys reference the full key path; see [Configuration](configuration.md) for defaults and
env-var equivalents.
For release changes, start with the [upgrade and rollback checklist](upgrading.md#upgrade-and-rollback-checklist),
then use this page for symptoms observed after the restart.

---

## Authentication failures

### API key stopped working

**Cause.** A personal API key (`tailscale.auth.method: apikey`) expires in at most 90 days and is
bound to the creating user. If that user is suspended or removed from the tailnet, the key is
immediately revoked.

**Fix.** Switch to OAuth, which issues short-lived, auto-refreshing tokens that are not tied to any
user and never expire on a fixed schedule:

```yaml
tailscale:
  auth:
    method: oauth
    oauth:
      client_id: ""      # set via TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID
      client_secret: ""  # set via TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET
      scopes:
        - all:read
```

If you must keep `method: apikey`, the startup log will always contain a **WARN** advisory — that is
expected and intentional.

### 401 responses logged at ERROR with OAuth

**Cause.** A 401 returned while OAuth is active (or an OAuth token-exchange failure with 401/403)
is logged at **ERROR** by the API transport. This means the OAuth client credentials are wrong, the
client has been deleted, or it lacks the required scopes.

**Fix.** Verify the `client_id` and `client_secret` match an active Tailscale OAuth client, and
check that the client carries at least the `all:read` scope. If you use
`streaming.auto_configure`, the `log_streaming` scope is also required:

```yaml
tailscale:
  auth:
    oauth:
      scopes:
        - all:read
        - log_streaming   # only needed for streaming.auto_configure
```

!!! tip
    Non-401 4xx responses (e.g. 403 from the flowlogs endpoint on an idle tailnet) are not logged
    as errors by the transport — they surface only as a collector **WARN** "collector failed" to
    avoid per-tick spam.

---

## No data arriving

### Prometheus endpoint is empty, refused, or never appears in the scraper

Start with the pull-only starter and run the checks in order:

```sh
tailscale2otel -config examples/config/prometheus-only.yaml
curl --fail http://127.0.0.1:2112/metrics
tailscale2otel -prometheus-check -config examples/config/prometheus-only.yaml
```

The first command must remain running while the other two execute. A successful `curl` proves the
listener; a successful `-prometheus-check` proves a bounded first exposition. Finally inspect the
scraper's target status — that is the proof its network path and scrape configuration work. For a
remote listener, configure `prometheus.auth.token` (or `token_file`) and use the same Bearer token
in the scraper. An unauthenticated non-loopback bind requires the explicit
`allow_unauthenticated: true` acknowledgement.

If the same backend also receives this exporter over OTLP, switch to `delivery.mode: prometheus` or
stop the duplicate scrape. See [Getting Started](getting-started.md#prometheus-pull) and
[Delivery modes](configuration.md#delivery-modes).

### Bare gateway URL returns 404 silently

**Cause.** When `otlp.protocol: http`, `tailscale2otel` calls `<endpoint>/v1/metrics`,
`<endpoint>/v1/logs` and — when tracing is enabled — `<endpoint>/v1/traces`. It appends the
per-signal paths for you. If you set `otlp.endpoint` to a bare gateway URL that does not end with
`/otlp` (e.g. `https://otlp-gateway-prod-us-central-0.grafana.net` instead of `…/otlp`), those paths
land at the wrong base and the gateway returns 404.

**What a 404 actually does.** A 404 is a delivery FAILURE, not a silently-accepted exchange — an
earlier version of this page claimed otherwise and was wrong (corrected 2026-07-29 under #383, and
now pinned by the wire-contract suite in `internal/telemetry`, which drives a real 404 and asserts
the failure is recorded). It increments `tailscale2otel_export_failures_total` and shows on the admin
status page's delivery panel with error class `invalid`. What makes it feel silent is that the
failure is only visible in the exporter's OWN telemetry: nothing arrives at the backend, so a
dashboard built on the exported data looks blank rather than broken. Check the admin status page or
the process logs, not the absence of data.

**Fix.** Set `otlp.endpoint` to the base URL ending in `/otlp`:

```yaml
otlp:
  endpoint: https://otlp-gateway-prod-us-central-0.grafana.net/otlp
```

The per-signal suffixes (`/v1/metrics`, `/v1/logs`, `/v1/traces`) are appended automatically. See
[Configuration](configuration.md#otlp-the-otlp-exporter) for the Grafana Cloud default.

### Wrong `otlp.protocol`

**Cause.** Setting `otlp.protocol: stdout` prints all signals to the console instead of sending
them to a backend. This is correct for local debugging but will leave your metrics store empty.

**Fix.** Set the protocol to match your backend transport:

```yaml
otlp:
  protocol: http   # or grpc
```

!!! tip
    `protocol: stdout` is deliberate for local debugging without a backend — run with it to
    confirm signals are emitted before pointing at a real endpoint.

---

## Flow-log / audit-log double-counting

**Cause.** `flowlogs` and `auditlogs` each have a `source` field that controls whether records come
from the API poller, the Splunk-HEC stream receiver, or both. Setting `source: both` — or running
the streaming receiver while a collector still polls the same log type — feeds the same records
through the same processor twice. Cross-source de-duplication is a best-effort failsafe and does not
guarantee exact-once delivery. The exporter logs a startup **WARN** when this condition is detected.

**Fix.** Pick exactly one ingestion path per log type:

```yaml
collectors:
  flowlogs:
    source: poll     # or stream — not both
  auditlogs:
    source: poll     # or stream — not both
```

See [Streaming & Webhooks](streaming-webhooks.md) for when to prefer `stream` over `poll`.

!!! tip "Confirm the dedup failsafe is firing"
    `tailscale2otel_dedup_hits_total` counts duplicate keys suppressed per set — a non-zero value
    means the best-effort cross-source de-duplicate set actually caught overlapping records. It is a
    diagnostic that both paths are active, not a substitute for picking one path.

---

## Running more than one instance against the same tailnet double-counts

**Cause.** `tailscale2otel` is designed to run as exactly **one instance per tailnet** (or, with
`tailnets:`, one instance covering the whole MSP fleet). It is not a stateless, horizontally
scalable scraper: each poller run and each streamed/webhook record is converted and emitted once,
with no cross-process coordination. Pointing a second instance at the same tailnet — a second
replica, a leftover process from a botched deploy, or a duplicate `tailscale:`/`tailnets:` entry
across two config files — makes both instances poll and stream the same flow logs, audit logs, and
webhook events independently, so every one of those metrics and log records is emitted twice (or
more). This is a distinct failure mode from `source: both` above: it happens even when every
collector correctly uses a single `source`, because the duplication is across *processes*, not
across ingestion paths within one process. The cross-source dedup set (`internal/dedup`) only
covers overlap within a single running instance and cannot see a second process at all.

**Fix.** Confirm exactly one running instance (container, pod, or systemd unit) targets each
tailnet, and that `checkpoint.file_path` (when `checkpoint.store: file`) is not shared read/write
by two instances at once. For multi-tailnet/MSP fleets, use one instance with a `tailnets:` list
rather than one instance per tailnet.

---

## Flow/audit enrichment shows `unknown` or `external`

**Cause.** IP-to-device-name resolution for flow logs and audit records depends on the in-memory
device-enrichment cache, which is populated by the `devices` collector. If `devices` is disabled,
no cache is ever built and every address falls back to `unknown` (tailnet nodes) or `external`
(off-tailnet addresses).

**Fix.** Ensure the `devices` collector is enabled (it is on by default):

```yaml
collectors:
  devices:
    enabled: true
```

The `tailscale2otel.enrich.cache_size` gauge (→ `tailscale2otel_enrich_cache_size_ratio`) shows how
many devices are currently in the cache; `tailscale2otel.enrich.cache_age` (→
`tailscale2otel_enrich_cache_age_seconds`) shows how stale it is.

---

## `/flows` 404s after upgrading to 4.0.0

**Symptom:** the flow view was working, and after the upgrade `/flows` returns 404. The status page
shows a "Flow view disabled" block, overall health reads `degraded`, and the log carries one line:

```text
flow view disabled: persistent flow store failed to open ... cannot prove its tailnet identity
```

**Cause:** databases written before 4.0.0 are named `flows-<tailnet>.db` and carry no tailnet
identity row. The rows hold user and device identities, and that filename is influenceable and
cannot prove which tailnet they came from, so the service will not adopt one silently. Nothing has
been deleted — the file is untouched.

**Fix:** stop the service and tell the binary which tailnet the database belongs to. Verify that is
right before running it; naming the tailnet is the ownership assertion the filename cannot make.

This is the forward-only migration in the [upgrade and rollback checklist](upgrading.md#upgrade-and-rollback-checklist).

```sh
tailscale2otel -config config.yaml -adopt-flow-db your-tailnet.example
```

It stamps the identity row, moves the file to the digest-qualified name, and reports how many rows
came across. Run it once per tailnet. It is safe to re-run and safe to interrupt. If you would rather
start clean, move the old file out of `flows.store.directory` instead.

See [Adopting a database written before 4.0.0](flow-view.md#adopting-a-database-written-before-400).

---

## Cardinality overflow — series silently dropped

**Cause.** Every metric instrument is bounded by `cardinality.metric_limit` (default `10000`).
When the number of distinct active series for a single instrument reaches this cap, the OTLP SDK
collapses all further series into a single `{otel_metric_overflow="true"}` series. Per-series detail
is silently lost; only the overflow sentinel remains. The most common trigger is enabling per-port
dimensions (`cardinality.flow.source_port` or `cardinality.flow.destination_port`) on a busy tailnet.

**Diagnosis.** Watch two self-observability signals:

- `tailscale2otel_series_overflowing_ratio{metric_name="..."}` — `1` when the named metric hit the
  cap during the last export interval.
- `tailscale2otel_series_active{metric_name="..."}` — the active series count, which pins at the
  cap when exceeded.
- A series with label `otel_metric_overflow="true"` appearing in your metrics store (e.g.
  `tailscale_network_io_bytes_total{otel_metric_overflow="true"}`) is the direct indicator.
- `tailscale2otel_series_limit` shows the configured cap (emitted only when a positive limit is set).

**Fix.** Either raise the cap or reduce cardinality:

```yaml
cardinality:
  metric_limit: 50000        # raise the per-instrument series cap

  flow:
    source_port: false        # disable per-port dimensions (largest driver)
    destination_port: false
    metrics_mode: rollup      # use bounded top-N rollup instead of per-connection raw families
    rollup_top_n: 500         # keep only the busiest N src/dst pairs
```

Setting `cardinality.metric_limit: 0` removes the cap entirely, at the cost of unbounded memory
growth under high-cardinality conditions.

---

## Node-metrics label collision (`tailscale_node` vs. `instance`)

**Cause.** The node-metrics scraper adds a `tailscale_node` label to every forwarded `tailscaled`
series to identify which node the series came from. Deliberately, it does **not** use `instance`:
on Grafana Cloud, the OTLP-to-Prometheus translation promotes the exporter's own
`service.instance.id` resource attribute to the `instance` label. If the per-node label were also
called `instance`, it would overwrite the collector-host value and collapse every scraped node's
series onto the same `instance`, making per-node queries impossible.

If you see `tailscale_node_up_ratio` missing from your store, or all forwarded `tailscaled_*`
series sharing the same `instance` label value rather than being distinguished by node name, check
that your dashboards or recording rules query on `tailscale_node`, not `instance`.

**Fix.** No configuration change is required — the label is `tailscale_node` by design. Update any
dashboard queries or alert rules that reference `instance` for these series to use `tailscale_node`
instead.

!!! tip
    The `tailscale.node.up` gauge (→ `tailscale_node_up_ratio`) is the canonical per-node health
    signal. It carries the `tailscale_node` label and is always emitted regardless of
    `metric_allow`/`metric_deny` filters. Use it for scrape-health alerting.

---

## Tracing enabled but no spans appear

**Cause.** With `tracing.enabled: true` but a `*traceidratio` sampler and `tracing.sampler_arg: 0`,
the sampler records **no** spans. The startup log emits a WARN for this combination.

**Fix.** Set a non-zero `tracing.sampler_arg` (e.g. `1.0` to record everything, `0.1` for 10%), or
use the `always_on` sampler. Also confirm the OTLP backend's access token carries `traces:write` —
on Grafana Cloud, missing that scope drops trace export while metrics/logs still flow.

---

## Suspected misconfiguration at runtime

**Cause.** `Validate()` errors and advisory `Warnings()` are logged at startup, but they are also
surfaced as live gauges so you can alert without scraping logs.

**Diagnosis.** Query `tailscale2otel_config_warnings_ratio` (count of advisory warnings) and
`tailscale2otel_config_valid_ratio` (`0` when `Validate()` failed). Both are emitted each export
cycle. The admin status page's **Config** tab lists every active advisory with the setting it
concerns and its remediation, and `/api/status.json` carries the same list as `advisories[]` — so you
do not have to find the startup log of a pod that may since have restarted.

---

## Generating a support bundle

**Cause.** Reporting an issue used to mean manually gathering the startup log, `-validate` output,
config, and version, and redacting them by hand yourself — slow, and easy to get wrong.

**Fix.** With the admin server enabled (`admin.enabled: true`), download
`GET /api/support-bundle.zip` (behind the same admin auth as every other admin route) to get a
deterministic, bounded zip archive containing: the running `version`, every configuration
`diagnostics` finding (the same checks `-validate` reports, but ALL of them, not just the first),
the full effective configuration with every secret reduced to `{secret, set, source}` (never a raw
value), component/API/OTLP-export state, and the metric/log-event catalogs — plus a `manifest.json`
stating exactly which files are included.

The device inventory (device names, hostnames, users, IP addresses) is PII-heavy and is **excluded
by default**; add `?include_devices=1` to opt in only when you intend to share it. Flow-log records
and raw audit/webhook log content are **never** included by this bundle — there is no opt-in for
either — so nothing beyond the bounded flow/event *counts* already on the status page travels with
it.

---

## Confirming the effective config without exposing secrets

**Cause.** Layered config (defaults < YAML file < `TS2OTEL_*` env) makes it easy to lose track of
which value actually took effect, or which layer set it — especially across a multi-tailnet `tailnets:`
list.

**Fix.** `tailscale2otel -print-effective-config -config <file>` loads the config exactly like a
normal run and prints every effective key, redacted the same way the admin status page and support
bundle are (`{secret, set, source}` for any secret, never a raw value), as a deterministic JSON array
on stdout. Add `-print-effective-config-format yaml` for YAML instead, or
`-print-effective-config-provenance` to also see which layer (`default`, `file`, or `env`) produced
each key — still never the secret's content, just its origin. There is no flag that disables
redaction; this command never prints a raw secret.

---

## Still stuck?

Nothing here matching your symptom is worth reporting — undiagnosable failure modes are bugs in this
page as much as in the code.

- **[Search existing issues](https://github.com/rknightion/tailscale2otel/issues)** — someone may have
  hit it already.
- **[Open a new issue](https://github.com/rknightion/tailscale2otel/issues/new)** — attach the support
  bundle above (or, without the admin server, the startup log, the output of
  `tailscale2otel -validate -config <file>`, and the version from `tailscale2otel -version`).
- **[Check the latest release notes](https://github.com/rknightion/tailscale2otel/releases/latest)** —
  the behaviour may have changed since your build.

Please redact tailnet names, device names, and IP addresses from anything you paste that did not
come from the support bundle above.
