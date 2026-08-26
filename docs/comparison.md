---
title: Why This Exporter
description: A factual, dated account of how tailscale2otel differs from the Tailscale admin console, scraping tailscaled's own :5252 metrics, or streaming Tailscale logs straight to a SIEM — and when to pick each.
tags:
  - Tailscale
  - OpenTelemetry
  - Metrics
---

# Why This Exporter

*Last reviewed: 2026-08.*

Tailscale already emits telemetry in three places before you add anything, and for some
estates one of those is the whole answer. This page states what `tailscale2otel` does
differently and why, so you can decide whether it earns a process in your stack. It describes
this codebase; it makes no claims about how anything else is implemented.

## What already exists

**The admin console.** Authoritative, real-time, and the right place to answer "what is my
tailnet doing right now". It is not where you keep history, it does not alert, and it lives
apart from the dashboards you already look at.

**`tailscaled`'s own `:5252` metrics.** Per-node data-plane truth: throughput, dropped
packets, routes, DERP and peer-relay state. It describes *that host*, and says nothing about
key expiry, ACL structure, auth keys or who joined last week. This exporter does not replace
it — it *scrapes* it, with automatic target discovery, and forwards the series verbatim rather
than renaming them into its own namespace. See [Node Metrics](node-metrics.md).

**Tailscale's native log streaming.** Configuration audit and network flow logs, pushed to an
endpoint of your choosing. If raw logs in a SIEM is the requirement, this is the shortest path
and there is no reason to put anything in between.

## Design choices specific to this exporter

**One process for the tailnet, not one per node.** A single static Go binary reads the
Tailscale API and derives 293 metrics and 17 log-event types across 16 collectors. The
collectors run on independent schedules and are isolated, so a failing source cannot stall the
others — which matters because the API surfaces here are not equally reliable or equally
rate-limited.

**Derived signals, not just forwarded logs.** Key and certificate expiry, client version
skew, per-DERP latency, NAT and connectivity quality, ACL size with structural risk scoring,
auth-key and OAuth-client expiry, outstanding invites, tailnet lock, MDM/EDR posture. These
are not in the log stream in a form you can alert on; producing them is most of what this
does. Streaming raw logs somewhere gets you the events, not the fleet's state.

**Four ingestion paths, with the trade-offs written down.**
[Streaming & Webhooks](streaming-webhooks.md) carries a compatibility matrix stating, per
path, which signals it can carry, its delivery guarantee and its durability boundary — that
`both` double-counts, that `objectstore` is capped at 14-day partitions under the partitioned
layout, that the push receivers are at-least-once *at Tailscale's discretion*. It also says
plainly that acknowledgement stops at the process: a checkpoint means "handed to the emitter",
not "queryable in your backend". Choosing an ingestion path is the decision most likely to
bite later, so it is documented as a decision rather than a default.

**Flow logs are bounded on purpose.** Per-connection records are inherently unbounded, so
throughput, packet and flow counters are aggregated through a top-N rollup for dashboards
while the per-connection detail stays as logs for drill-down. The bill is the constraint
being designed against, not an afterthought.

**Every signal has a declared job, enforced in CI.**
[Signal Coverage](signal-coverage.md) is generated from
`internal/catalog/signal_dispositions.json`, which CI gates against the in-code telemetry
catalog, the generated dashboard, and both alert-rule files — in both directions. A new
signal cannot land undecided, and a claim that something is charted or alerted on has to be
true. Nothing on that page comes from a text search over prose. This is the mechanism that
stops a metrics catalog drifting into fiction, which is the usual fate of one.

**OTLP and Prometheus at the same time.** Native OTLP metrics and logs, a Prometheus
`/metrics` endpoint, or both simultaneously — no collector in front to translate.
Choose the first delivery path in [Getting Started](getting-started.md#choose-a-destination): OTLP
push for an OTLP backend, Prometheus pull for a scraper, and stdout to inspect collection locally.
Do not send both OTLP and a scrape of the same metrics to one backend.

**Headscale works.** A supported subset runs against a self-hosted control plane: devices, users,
keys, ACL, and node metrics. Tailscale-only collectors stay disabled because Headscale does not
expose equivalent data.

## When to pick something else

**You want raw logs in a SIEM and nothing else.** Point Tailscale's log streaming at it
directly. Adding an exporter in the middle buys you nothing for that requirement.

**You care about one node's data plane.** Scrape that node's `:5252` endpoint. If you later
want it centrally alongside the tailnet-wide signals, this exporter's scraper will forward it
unchanged.

**Your tailnet is small and the console is enough.** 16 collectors, four ingestion paths
and a cardinality budget exist for fleets with expiry cliffs, ACL churn and a metrics bill.
Below that, the console is not a compromise.

**You need something the API does not expose.** The Tailscale API is the ceiling. See
[Admin API Compatibility](api/compatibility.md) for what is available, and where.

## See also

- [Metrics & Logs Reference](metrics.md) — all 293 metrics and 17 log-event types
- [Signal Coverage](signal-coverage.md) — what each signal is *for*
- [Architecture](architecture.md) — how the collectors and emitters fit together
- [Streaming & Webhooks](streaming-webhooks.md) — the ingestion-path matrix
- [Security](security.md) — OAuth scope and what the exporter can reach
- [Getting Started](getting-started.md) — destination choice and canonical launch commands
