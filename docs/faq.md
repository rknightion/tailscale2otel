---
title: FAQ
description: Frequently asked questions about tailscale2otel's data model, API footprint, and operation, each answer pointing to the authoritative page.
tags:
  - faq
  - configuration
  - security
---

# Frequently Asked Questions

Short answers to common questions. Each answer links to the authoritative page for the full
detail — treat those linked pages as the source of truth.

## What it sees

### Does tailscale2otel capture packets?

No. It reads the Tailscale API — device inventory, flow logs, audit logs, and the other
control-plane surfaces — it never touches the network stack or a packet capture. One consequence
worth knowing: TSMP rejection records (ACL drops, refused connections) show up in the flow log
because Tailscale reports them, but never appear in a `tcpdump` on any interface, including the
Tailscale one. See [Flow view](flow-view.md) and [Architecture](architecture.md).

### Can it tell me whether a connection went direct or through DERP?

Yes, but only after the fact and only for physical (non-overlay) traffic. Flow records carry
`tailscale.path` (`direct`/`derp`) and, when relayed, a numeric `tailscale.derp.region_id`. Device
gauges like `tailscale.device.connectivity.direct_capable` and `.hard_nat` report *eligibility* —
NAT type and UDP support — not the live path; confirming an actual connection is direct or relayed
needs the flow record's `tailscale.path`. See [Metrics](metrics.md) and
[Node metrics](node-metrics.md) for the node-local DERP/peer-relay counters.

### Why do some flow records have no destination node?

A relayed connection reports the DERP loopback marker in place of an endpoint address, so
`tailscale.dst.node` is meaningless on it — the DERP region (`tailscale.derp.region_id`) describes
the relay, not a peer. Filter on `tailscale.src.node` instead, which is unaffected. This also means
a relayed connection is not resolvable to a device by destination and reports as `unknown` there.
See [Metrics](metrics.md).

### A device is missing from the dashboards — where did it go?

Two independent things can cause this. First, IP-to-name resolution for flow/audit records depends
entirely on the `devices` collector's in-memory cache; if that collector is disabled, addresses
fall back to `unknown` (in-tailnet) or `external` (off-tailnet) rather than a device name — the raw
IPs are still exported, only the label is missing. Second, per-device gauges are gated by
`cardinality.per_entity.device`; disabling it collapses per-device series into tailnet-wide
aggregates. See [Troubleshooting](troubleshooting.md).

### Is data scoped per-tailnet, or does everything blur together?

Per-tailnet, deliberately. Each configured tailnet (single `tailscale:` block, or an entry in a
`tailnets:` list for MSP/multi-tailnet fleets) gets its own OTEL provider stamping
`tailscale.tailnet` as a signal-scoped attribute and its own `service.instance.id`, so tailnets
never collide on a shared Grafana Cloud backend. Process-level signals (runtime, OTLP delivery
health) are process-global and cover every configured tailnet at once, labelled as such. See
[Architecture](architecture.md) and [Configuration](configuration.md).

## API footprint and authentication

### What Tailscale API scopes does it need?

The default OAuth scope is `all:read` — a least-privilege read-only grant covering every collector.
Two things need more: `streaming.auto_configure` (which registers the built-in receiver as the
tailnet's log-streaming sink) needs `log_streaming` added, and `collectors.acl.validate` (on by
default) calls `POST /tailnet/{tailnet}/acl/validate` — despite the verb this is a read-only
operation gated by `policy_file:read`, not a write. It's the only non-`GET` call the exporter
makes; set `collectors.acl.validate: false` if you require a strictly GET-only client. See
[Configuration](configuration.md) and [Security](security.md).

### Is a personal API key good enough, or should I use OAuth?

OAuth is strongly preferred. A personal API key (`method: apikey`) expires in 90 days or less and
is revoked the moment its creating user is suspended or removed from the tailnet — the exporter
logs a WARN advisory at startup whenever one is configured. OAuth tokens are short-lived,
auto-refreshed, and tied to no user account. See [Getting Started](getting-started.md) and
[Troubleshooting](troubleshooting.md).

### How often does it poll, and does that risk hitting Tailscale's API limits?

Cadences are tiered by how fast the data changes: `devices`, `flowlogs`, `auditlogs`, and
`node_metrics` poll every 60s by default, as does opt-in `k8s_audit`; `users`, `keys`, and
`oauth_apps` run every 300s; and slow-moving collectors
(`settings`, `acl`, `dns`, `contacts`, `webhooks`, `posture_integrations`, `log_stream`, `services`,
and PAM inventory) every 600s. The independent PAM session poller runs every 60s. Each of the 17 collectors runs
in its own goroutine with a small randomised
start-up stagger so they don't all hit the API in the same instant, and the API client has built-in
retry and rate-limit handling. See [Getting Started](getting-started.md) and
[Architecture](architecture.md).

## Operation

### Which delivery mode should I start with?

Choose Grafana Cloud OTLP when Grafana Cloud (or another OTLP receiver) is the backend,
Prometheus pull when an existing scraper is the backend, and stdout when proving collection locally.
`delivery.mode: prometheus` disables inherited OTLP export; `dual` is for separate pull and OTLP
destinations, not two ingest paths into the same backend. Follow the runnable routes in
[Getting Started](getting-started.md#choose-a-destination) and the exact behavior in
[Configuration](configuration.md#delivery-modes).

### Can I run multiple replicas for high availability?

No — run exactly one instance per tailnet (or one instance covering an entire MSP fleet via a
`tailnets:` list). There is no cross-process coordination: checkpoints, the dedup set, and the
device-enrichment cache are all in-process state. A second replica polling or streaming the same
tailnet double-counts every flow log, audit log, and webhook event independently of whichever
poll-vs-stream choice you made, and the in-process dedup set cannot see a second process at all.
See
[Troubleshooting](troubleshooting.md#running-more-than-one-instance-against-the-same-tailnet-double-counts).

### I get duplicate flow/audit records — what's wrong?

Almost always one of two causes: either both the poll and stream paths are active for the same log
type (pick exactly one — the app logs a startup WARNING when both are on), or a second instance is
pointed at the same tailnet (see above). A best-effort bounded dedup set catches exact duplicates as
a failsafe, but it is not a substitute for correct configuration. See
[Troubleshooting](troubleshooting.md).

### Should I poll or stream flow/audit logs?

Poll (the default) needs no inbound network exposure — the window collector pulls from the
Tailscale Logs API on a schedule. Stream runs a built-in Splunk-HEC-compatible receiver that
Tailscale pushes to in near-real time, at the cost of an internet-reachable HTTPS endpoint. Both
paths feed the identical `flowlog.Processor`/`audit.Processor`, so the emitted signals are the same
either way — the choice is about latency and exposure, not data shape. See
[Streaming & Webhooks](streaming-webhooks.md) and [Architecture](architecture.md).

### Does it work against Headscale instead of hosted Tailscale?

Yes, with a reduced collector set. Setting `provider: headscale` runs only `devices`, `users`,
`keys`, `acl`, and `nodemetrics` — the Tailscale-only collectors (`flowlogs`, `auditlogs`,
`services`, `webhooks`, `contacts`, `posture_integrations`, `log_stream`, `settings`, `dns`)
auto-disable because Headscale's API doesn't expose the equivalent data, and some device/user
signals are reduced. See
[Configuration → `headscale`](configuration.md#headscale-headscale-control-plane-connection).

## Security

### Does the exported telemetry carry PII?

By default flow and audit logs carry IP addresses, device/hostnames, and user identities — treat
your OTLP backend as a trusted sink and scope its credentials accordingly. `pii_filter` (all
categories on by default) lets you redact specific identifier classes before export, and several
cardinality knobs (`cardinality.flow.source_port`, `.collapse_external`, `.node_dims`) reduce what
leaves the tailnet in the first place. Note that disabling the `devices` collector does **not**
remove IPs from the payload — it only degrades name resolution. See [Security](security.md).

### Is the admin status page safe to expose beyond localhost?

Only with a token set. With no `admin.auth.token`, the page is served on a loopback bind only —
setting a non-loopback `admin.listen` without a token doesn't expose the page, it makes it answer
403 to everyone. `/healthz` and `/readyz` are never gated. The in-memory `/flows` view is not
covered by `pii_filter` at all — it shows device names, addresses, and users in full to anyone
holding the admin token. See [Getting Started](getting-started.md) and [Security](security.md).
