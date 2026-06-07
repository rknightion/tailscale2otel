---
title: tailscale2otel
description: Polls the Tailscale API and exports OpenTelemetry-native metrics + logs over OTLP, optimized for Grafana Cloud
image: assets/social-card.png
---

# tailscale2otel

`tailscale2otel` polls the [Tailscale API](https://tailscale.com/api) for every available kind of
observability data and exports it as **OpenTelemetry-native metrics and logs** — optimized for
Grafana Cloud (OTLP) but compatible with any OTEL backend. Tailscale exposes a rich observability
surface (network flow logs, configuration audit logs, a detailed device inventory, users, keys,
settings, ACL, DNS) but no Prometheus endpoint, and it streams logs only to SIEM/storage sinks.
`tailscale2otel` synthesizes well-modelled,
[semantic-convention](https://opentelemetry.io/docs/specs/semconv/)-compliant OTEL telemetry from
that data so you get device-fleet health, network throughput by node and protocol, an audit and
event stream, and key-expiry signals out of the box — as a lightweight single static binary with no
external runtime dependencies.

## Features

- **Network flow logs → metrics + logs.** Aggregated `tailscale.network.io` / `.packets` / `.flows`
  counters (low cardinality) for dashboards and alerting, plus full-fidelity per-connection flow
  records as OTEL logs for drill-down. Source IPs are enriched to device names.
- **Configuration audit logs → logs + counter.** Every tailnet configuration change captured as a
  structured OTEL log event.
- **Device inventory, users, keys, settings, ACL, DNS, and more** → gauges covering online status,
  key expiry, per-user device counts, feature toggles, posture, services, contacts, and webhook
  endpoints.
- **Two ingestion paths for logs (pick one per log type):** poll the Tailscale API on a schedule, or
  receive logs via the built-in **Splunk-HEC-compatible streaming receiver** — both feed the same
  conversion pipeline.
- **Optional webhook receiver** for real-time Tailscale events, HMAC-verified.
- **Optional node-metrics scraper** that forwards `tailscaled` per-node Prometheus `/metrics`
  centrally over OTLP, as a drop-in for per-node scraping — including automatic target discovery
  from the devices API.
- **OTLP push** over gRPC or HTTP with first-class Grafana Cloud support; `stdout` mode for local
  debugging without a backend.
- **Admin status page** at `/` (plus `/healthz`, `/readyz`, and `/api/status.json`) showing live
  collector health, active-series cardinality, the metrics and log catalog, discovered nodes, and a
  redacted config snapshot — and opt-in continuous profiling via pprof or Pyroscope.

## Where to next?

| | |
|---|---|
| [Getting Started](getting-started.md) | Zero to first metrics in Grafana Cloud |
| [Installation](installation.md) | Docker, Helm, binary |
| [Configuration](configuration.md) | Every config key, default, and env-var |
| [Metrics](metrics.md) | Full catalog of emitted metrics and log events |
