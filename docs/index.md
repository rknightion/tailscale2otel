---
title: tailscale2otel
description: Polls the Tailscale API and exports OpenTelemetry-native metrics, logs, and optional traces over OTLP, optimized for Grafana Cloud
image: assets/social-card.png
---

# tailscale2otel

`tailscale2otel` polls the [Tailscale API](https://tailscale.com/api) for every available kind of
observability data and exports it as **OpenTelemetry-native metrics and logs** (plus an optional
**traces** pillar for the exporter's own self-observability) — optimized for
Grafana Cloud (OTLP) but compatible with any OTEL backend. Tailscale exposes a rich observability
surface (network flow logs, configuration audit logs, a detailed device inventory, users, keys,
settings, ACL, DNS) but no Prometheus endpoint, and it streams logs only to SIEM/storage sinks.
`tailscale2otel` synthesizes well-modelled,
[semantic-convention](https://opentelemetry.io/docs/specs/semconv/)-compliant OTEL telemetry from
that data so you get device-fleet health, network throughput by node and protocol, an audit and
event stream, and key-expiry and device version-skew signals out of the box — as a lightweight
single static binary with no external runtime dependencies.

## Features

- **Network flow logs → metrics + logs.** Aggregated `tailscale.network.io` / `.packets` / `.flows`
  counters (low cardinality) for dashboards and alerting, plus full-fidelity per-connection flow
  records as OTEL logs for drill-down. Source IPs are enriched to device names.
- **Configuration audit logs → logs + counters.** Every tailnet configuration change captured as a
  structured OTEL log event, plus a curated security-/lifecycle-categorized change counter
  (`tailscale.config.audit.changes`) for alerting on high-value changes without the full-stream noise.
- **Device inventory, users, keys, settings, ACL, DNS, and more** → gauges covering online status,
  per-device connectivity/NAT quality, exit-node and subnet-router analytics, fleet hygiene roll-ups
  (untagged/ephemeral/tag/version distributions), key expiry, per-user device counts, feature toggles,
  posture, services, contacts, device-share invites, and webhook endpoints. Key inventory spans auth
  keys, OAuth clients, and API tokens; ACL policy is scored for structural risk (wildcard /
  unrestricted / auto-approver / SSH-wildcard / posture-gated rules).
- **Self update-available + device version-skew** signals out of the box, fetched from the GitHub and
  Tailscale release feeds (both opt-out for air-gapped deployments).
- **Optional OTEL traces pillar** (`tracing.enabled`, off by default) — spans for each scrape cycle,
  Tailscale API request, and receiver HTTP request, with trace exemplars linking the
  `tailscale2otel.api.duration` histogram to the originating API span. Reuses the `otlp.*` endpoint.
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
