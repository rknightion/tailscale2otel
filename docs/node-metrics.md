---
title: Node Metrics
description: Enable and scrape the optional per-node tailscaled :5252 metrics endpoint
---

# How to expose `tailscaled` metrics

This is the operator how-to for getting per-node Tailscale metrics **out of `tailscaled`** and
into the optional `node_metrics` scraper. It is the companion to the
[Metrics & Logs Reference](./metrics.md) — that page documents every signal `tailscale2otel`
emits (including the [Node metrics scraper](./metrics.md#node-metrics-scraper) section that
describes how forwarded series look once they land in Grafana Cloud); **this** page covers the
upstream side: how a node *publishes* its metrics, what the metrics are, the access control you
need, and how to point the scraper at them.

These per-node metrics are **separate** from the tailnet-wide signals `tailscale2otel` derives from
the Tailscale API/log stream. They come straight off each host's `tailscaled` daemon and describe
that node's own data plane (throughput, dropped packets, routes, DERP/peer-relay state). The
scraper forwards them **verbatim** — they are *not* renamed into the curated `tailscale.*`
namespace.

---

## TL;DR

1. On each node you want to monitor, enable the client metrics endpoint
   (Tailscale **v1.78.0+**):

   ```sh
   tailscale set --webclient
   ```

2. Open **TCP port 5252** to your monitoring host with a tailnet ACL **grant** (see
   [Access control](#access-control-the-only-auth-you-need)).

3. Point the scraper at `http://<node-tailscale-ip>:5252/metrics` in
   [`collectors.node_metrics`](#wiring-up-the-scraper). Native endpoints are **plain HTTP with no
   token and no TLS**, so you configure nothing else.

---

## What endpoints `tailscaled` can expose

### Client (host) metrics — the supported path

Since **Tailscale v1.78.0**, the client can serve its own metrics in **Prometheus text format**.
Enable it once per node:

```sh
tailscale set --webclient
```

Once enabled, the same `/metrics` payload is reachable two ways:

| Scope | URL | Reachable from |
|---|---|---|
| **Local** (on the node itself) | `http://100.100.100.100/metrics` | the node only — `100.100.100.100` is the Tailscale "quad-100" local address |
| **Over the tailnet** | `http://<node-tailscale-ip>:5252/metrics` | any tailnet peer **allowed by ACL** to reach TCP `5252` |

Both are **plain HTTP**. There is **no bearer token and no TLS** on the native endpoint — see
[Access control](#access-control-the-only-auth-you-need) for how reachability is actually gated.

For the scraper you want the **tailnet** form (`:5252`), because `tailscale2otel` runs on a
different host from the nodes it scrapes. The quad-100 local form is handy for a quick
`curl http://100.100.100.100/metrics` sanity check while you are logged into the node.

### CLI alternatives (no listener)

If you cannot or do not want to open a port — for example you already run a
[Prometheus node_exporter textfile collector](https://github.com/prometheus/node_exporter#textfile-collector)
on the host — `tailscale` can print or dump the same metrics locally:

```sh
tailscale metrics print          # write the Prometheus-text metrics to stdout
tailscale metrics write <file>   # write them to a file (e.g. a textfile-collector .prom)
```

These feed an existing scrape pipeline on the node rather than being scraped by `tailscale2otel`
directly.

### The `tailscaled` debug server — avoid for monitoring

`tailscaled --debug=localhost:8080` exposes `/debug/metrics`. This is **localhost-only,
unauthenticated, and an UNSTABLE superset** of the client metrics whose names and contents can
change between releases. **Prefer the documented client metrics** (`--webclient` → `:5252`) for
anything you intend to alert or build dashboards on; treat `/debug/metrics` as an interactive
debugging aid only.

---

## Documented metric families

These are the stable client-metric families exposed on `:5252/metrics`. Names are emitted **as-is**
by `tailscaled`; the scraper forwards them verbatim (Grafana Cloud's standard OTLP→Prometheus
normalization still applies on ingest — see the [reference](./metrics.md#node-metrics-scraper)).

### Counters

| Metric | Meaning |
|---|---|
| `tailscaled_inbound_packets_total` / `tailscaled_outbound_packets_total` | Packets received / sent, **by `path`**. |
| `tailscaled_inbound_bytes_total` / `tailscaled_outbound_bytes_total` | Bytes received / sent, **by `path`**. |
| `tailscaled_inbound_dropped_packets_total` / `tailscaled_outbound_dropped_packets_total` | Packets dropped on the inbound / outbound path. |
| `tailscaled_peer_relay_forwarded_packets_total` / `tailscaled_peer_relay_forwarded_bytes_total` | Packets / bytes this node forwarded while acting as a peer relay. |

### Gauges

| Metric | Meaning |
|---|---|
| `tailscaled_advertised_routes` | Number of subnet routes this node advertises. |
| `tailscaled_approved_routes` | Number of its advertised routes that are approved. |
| `tailscaled_peer_relay_endpoints` | Number of peer-relay endpoints currently configured. |
| `tailscaled_health_messages` | Count of active client health-warning messages. |
| `tailscaled_home_derp_region_id` | The node's current home DERP region ID. |

### The `path` label and cardinality

> **Cardinality warning.** The throughput counters
> (`tailscaled_{inbound,outbound}_{packets,bytes}_total`) carry a **`path` label** whose values are
> `direct_ipv4`, `direct_ipv6`, `derp`, `peer_relay_ipv4`, and `peer_relay_ipv6`. Series count
> therefore scales as **nodes × `path` × metric**. Scraping a large fleet multiplies quickly:
> budget for it, and prefer scraping a representative subset (e.g. relays and exit nodes) over
> every node if you only need fleet-level throughput. This is the same per-`path` fan-out called
> out for flow-volume handling — see the flow-volume note (todos §11.3 / S4-7) when sizing DPM and
> ingest cost.

> **Version note.** Nodes older than **v1.78.0** do not serve `:5252/metrics`. The scraper marks
> such a target as down (`tailscale.node.up` → `0`) rather than emitting node series for it.

---

## Access control — the only auth you need

The native client-metrics endpoint has **no application-layer auth**. Access is gated entirely at
the **tailnet layer**: a peer can only reach `:5252` on a node if your tailnet policy **grants** it.
This is the standard Tailscale model — reachability *is* the authorization.

Open TCP port `5252` from your monitoring identity to the nodes you want scraped with a **grant**
in your tailnet policy file. For example, allow hosts tagged `tag:monitoring` (where
`tailscale2otel` runs) to reach port `5252` on hosts tagged `tag:server`:

```json
{
  "grants": [
    {
      "src": ["tag:monitoring"],
      "dst": ["tag:server"],
      "ip": ["5252"]
    }
  ]
}
```

Tighten `src`/`dst` to match your tagging scheme. Because there is no token to manage and no TLS to
terminate, **the grant is the access-control boundary** — keep it as narrow as possible.

---

## Wiring up the scraper

The scraper lives under [`collectors.node_metrics`](https://github.com/rknightion/tailscale2otel/blob/main/config.example.yaml) and is **off by
default**.

> **Static targets and per-target labels/headers must be set via a config file**, not environment
> variables. The `targets` field is a list of structs; flat `TS2OTEL_*` env vars can only override
> scalar fields. Scalar scraper settings (enabled, interval, timeout, metric_allow, etc.) can be
> overridden via env as usual — e.g. `TS2OTEL_COLLECTORS__NODE_METRICS__INTERVAL=30s`.

A minimal static-target config for native endpoints needs nothing more than the URL:

```yaml
collectors:
  node_metrics:
    enabled: true
    interval: 60s
    timeout: 10s
    max_response_bytes: 4194304  # per-target response cap (4 MiB)
    max_samples: 50000           # per-target sample cap per scrape
    targets:
      - url: "http://100.64.0.10:5252/metrics"
        instance: "relay-1"          # defaults to the URL host:port if omitted
        labels: { role: "relay" }    # extra static labels merged onto every series
```

Native `tailscaled` endpoints are **plain HTTP** — leave all the auth/TLS fields unset and the
scrape is a plain `GET` with no added headers.

### What the scraper emits

- **Verbatim series.** Each scraped `tailscaled` metric is re-emitted with its **original name and
  labels preserved** (not renamed into `tailscale.*`).
- **Counters → deltas, gauges → gauges.** Cumulative counters (and histogram/summary components)
  are converted to monotonic deltas across scrapes; gauges are forwarded as gauges.
- **Per-target `tailscale.node.up`.** A health gauge (→ `tailscale_node_up_ratio`) is emitted per
  target reporting whether the last scrape of that node succeeded.
- **Node identity = the `instance` label.** Every forwarded series carries an `instance` label
  (your `instance:` value, or the URL `host:port` if omitted) — identity is a **label, not an OTEL
  Resource**. See the [reference](./metrics.md#node-metrics-scraper) for how these query in Grafana.

### Scrape and discovery safety limits

The scraper enforces bounded work per node. `max_response_bytes` caps the number of bytes read from
one target response (default `4194304`, 4 MiB), and `max_samples` caps the number of valid samples
accepted from one scrape (default `50000`). A target that exceeds either limit is treated as a failed
scrape and reports `tailscale.node.up=0` for that collection.

When dynamic discovery is enabled, `discovery.max_targets` caps the number of discovered devices that
can become scrape targets in one refresh (default `1000`). Use `include_tags`/`exclude_tags` together
with this cap to keep automatic discovery scoped to the nodes you intend to scrape. Both accept
comma-separated values as env vars:

```sh
TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__INCLUDE_TAGS=tag:server,tag:relay
TS2OTEL_COLLECTORS__NODE_METRICS__DISCOVERY__EXCLUDE_TAGS=tag:exit-node
```

### Optional per-target auth & TLS (proxied / HTTPS targets only)

You only need these fields if you front a node's metrics behind an **HTTPS reverse proxy** or an
**authenticating gateway** — for example a Prometheus-compatible proxy that fans several nodes out
behind one TLS endpoint. **Native `tailscaled` endpoints need none of them.**

```yaml
targets:
  - url: "https://relay-2.ts.net:5252/metrics"
    bearer_token_file: "/run/secrets/scrape-token"   # read fresh each scrape (rotation-safe)
    # bearer_token: "..."                             # inline alternative; bearer_token_file wins
    headers: { X-Scope-OrgID: "1" }                   # extra request headers (e.g. tenant routing)
    tls:
      ca_file: "/etc/ssl/ca.pem"                      # custom CA to trust
      cert_file: "/etc/ssl/client.pem"                # client cert (mTLS)
      key_file:  "/etc/ssl/client-key.pem"            # client key  (mTLS)
      server_name: "relay-2.ts.net"                   # SNI / cert name override
      insecure_skip_verify: false                     # DEFAULT false — leave it false
```

Field notes:

| Field | Purpose |
|---|---|
| `bearer_token` | Static `Authorization: Bearer <token>` header. |
| `bearer_token_file` | Path read **fresh on every scrape** (rotation-safe); **takes precedence** over `bearer_token`. |
| `headers` | Arbitrary extra request headers (e.g. tenant routing). |
| `tls.ca_file` | Custom CA bundle to trust for the HTTPS target. |
| `tls.cert_file` / `tls.key_file` | Client certificate / key for mutual TLS. |
| `tls.server_name` | Overrides the SNI / expected certificate name. |
| `tls.insecure_skip_verify` | **Defaults to `false`** — an HTTPS target is verified unless you explicitly opt out. Setting `true` disables verification and is a deliberate footgun; avoid it. |

A target with a non-empty `tls` block gets its own dedicated HTTP client/transport; targets without
one share the default client.

---

## Quick verification

From the node itself (local quad-100 address):

```sh
curl -s http://100.100.100.100/metrics | head
```

From the monitoring host (over the tailnet, after the ACL grant is in place):

```sh
curl -s http://<node-tailscale-ip>:5252/metrics | head
```

You should see Prometheus-text output beginning with `# HELP` / `# TYPE` lines and the
`tailscaled_*` families above. If the tailnet `curl` hangs or is refused, the grant for port `5252`
is missing or too narrow. If it returns 404 / empty, `--webclient` is not enabled on that node, or
the node predates v1.78.0.

Once the scraper is running, confirm each target is healthy in Grafana:

```promql
tailscale_node_up_ratio
```

A value of `1` per `instance` means the last scrape of that node succeeded.

---

## Sources

- Tailscale **client metrics reference** — <https://tailscale.com/kb/1482/client-metrics>
- Tailscale **KB 1192 — ACL syntax / grants** — <https://tailscale.com/kb/1192/acl-tags>
- Tailscale **KB 1278 — tailnet policy file (grants)** — <https://tailscale.com/kb/1278/tailnet-policy-syntax>
- Tailscale CLI — `tailscale set`, `tailscale metrics` — <https://tailscale.com/kb/1080/cli>

See also the [Metrics & Logs Reference](./metrics.md) for the full catalog of signals
`tailscale2otel` emits and how the forwarded node series appear in Grafana Cloud.
