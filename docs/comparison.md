---
title: Why This Exporter
description: A factual, dated account of how tailscale2otel differs from the Tailscale admin console, scraping tailscaled's own :5252 metrics, or streaming Tailscale logs straight to a SIEM — and when to pick each.
tags:
  - Tailscale
  - OpenTelemetry
  - Metrics
---

# Why this exporter

Use tailscale2otel when you want tailnet state and derived signals in the telemetry backend you
already operate. A single static Go binary reads the Tailscale API and derives 332 metrics and 30 log-event types across 17 collectors.

## Sources and alternatives

Tailscale's admin console is the place to administer the tailnet. For monitoring, this exporter
combines its API inventory with logs and optional per-node metrics, then sends the results to
OTLP or Prometheus.

If you need only one host's data-plane metrics, scrape that host's native `:5252` endpoint.
tailscale2otel can [discover and scrape those endpoints centrally](node-metrics.md), preserving
raw metric names and adding a bounded set of derived signals.

If you need only raw logs in a SIEM, use Tailscale's native log streaming directly. This exporter
adds processing when you also need traffic aggregates, identity enrichment, expiry signals or
configuration-change counters.

## What the exporter adds

Flow metrics use a bounded top-N rollup, while per-connection detail remains available in logs.
Device and policy collectors report fleet state such as key expiry, client version skew, posture,
routes and ACL risk. Optional snapshots and lifecycle events provide change history.

17 collectors, four ingestion paths and configurable cardinality limits cover different deployment
sizes. Pick one [ingestion source](streaming-webhooks.md) per log type. Checkpoints track local
processing progress; they do not prove a backend accepted the telemetry. The [gateway
guide](gateway.md) explains persistent buffering and its delivery limits.

The built-in [flow view](flow-view.md) and [event explorer](events.md) provide local investigation
without a backend. [Kubernetes coordination](high-availability.md) supports active-passive
replicas, with separate limits for shared cursors and per-pod history.

OTLP and Prometheus pull can run together when they serve separate destinations. Sending both
copies to one backend duplicates metrics. [Headscale](configuration.md#headscale-headscale-control-plane-connection)
runs a reduced collector set: devices, users, keys, ACL and node metrics.

CI checks the catalog against generated dashboards and alert rules. The [signal coverage
ledger](signal-coverage.md) records panel, variable and rule references, including structural
exceptions. The [metrics catalog](metrics.md) lists all 332 metrics and 30 log-event types.

## Limits

The exporter can report only what its configured sources expose. A missing API field, permission
or source event cannot be reconstructed from a metric. PAM authorization results, for example,
are not connection-health measurements. See the [feature guide](features.md) for each source's
setup and operating limits.
