---
title: Flow view
description: Explore your tailnet's traffic from the exporter itself — topology graph, timeline, top talkers and recent connections at /flows, with no metrics backend in the loop.
---

# Flow view

The exporter serves a built-in view of your tailnet's network flow logs at **`/flows`** on the admin
server. It answers the questions you actually open a flow tool for — who talked to whom, over what,
how much, and when — without a Grafana or Prometheus backend in the loop.

It is a **convenience view, not a second telemetry pipeline**. Everything it shows is derived from the
same flow records the exporter is already sending over OTLP, which remains the system of record. The
store is in memory, bounded, and lost on restart.

## What it shows

**Topology.** A force-directed graph of device-to-device conversations. Vertices are sized by total
volume, edges by the bytes on that conversation. Drag a device to reposition it; click one to filter
the whole page to it and dim everything it does not talk to.

**Traffic over time.** Transmit and receive over the selected window. Drag horizontally on the chart
to zoom into a span — that pins the view and pauses polling; double-click, or press **jump to now**,
to go back to live.

**Top devices and conversations.** Ranked by bytes, split by role, so you can tell what a device sent
from what it received rather than inferring it from edge direction.

**Destination services.** What is being talked *to*, resolved to IANA service names where the port is
registered.

**Identity.** Traffic by user, by tag, and by operating system, taken from the node metadata each flow
record carries.

**Recent connections.** The newest individual connections, with their raw endpoints, filterable by
device, address, port, service or protocol. The aggregates cannot answer "what exactly was that", so a
bounded number of raw connections are kept alongside them.

## Enabling it

It is on by default. It needs the admin landing page, which is also on by default:

```yaml
admin:
  enabled: true
  landing_page: true
  auth:
    token: ""        # required on any non-loopback bind — see below
flows:
  enabled: true
  retention: 6h
```

With `admin.enabled` or `admin.landing_page` off, the store is **not built at all** and a startup
advisory says the setting is doing nothing. See [`flows`](configuration.md#flows-built-in-flow-view)
for the full key reference.

## Access control

`/flows` and `/api/flows.json` sit behind the **same gate as the rest of the admin surface**, with no
exceptions:

- With `admin.auth.token` set, both require it as the HTTP Basic password or as
  `Authorization: Bearer <token>`.
- With no token, both are served only on a **loopback** `admin.listen`. On any other bind they are
  refused with HTTP 403 — the same fail-closed behaviour as the status page.

This matters more here than elsewhere: the page shows device names, addresses, and (unless you have
turned it off) the user each device belongs to.

## Privacy

The store sits behind the OTLP redactor rather than in front of it, so it applies your
[`pii_filter`](configuration.md#pii_filter-pii-identifier-redaction) policy **itself**. Switching a
category off removes it from the view as well as from your telemetry:

| `pii_filter` key | Effect on `/flows` |
|---|---|
| `emails: false` | The users breakdown empties; no user is attached to any flow. |
| `hostnames: false` | Device names disappear; the topology graph has nothing left to draw. |
| `tailscale_ips: false` | The recent-connections list loses its raw endpoints; ports and services stay. |
| `external_ips: false` | External addresses are dropped from the connection list. |

## Limits worth knowing

**Memory is bounded, and coverage is honest about it.** Everything is aggregated to one-minute
resolution on the way in, and every dimension has a per-minute cap. Beyond a cap, keys fold into
`__other__` and the page says coverage is partial rather than implying it is complete — the totals stay
exact either way. This matters because the streaming receiver is a potentially unauthenticated ingress,
and a flood of unique flow keys must not be able to grow the process without limit.

**Retention is memory, not storage.** `flows.retention` sizes a ring of one-minute buckets and is
bounded to 24h. In multi-tailnet mode each tailnet keeps its own store, so memory scales with the
number of tailnets too. Nothing survives a restart. For long-range history, query your OTLP backend.

**Some flows carry no destination.** Exit traffic never does — that is how the Tailscale API reports
it, not a gap in decoding. Those connections count toward totals and show a dash where a destination
would be, rather than a fabricated endpoint. Use the `tailscale.exit_node.*` metrics to measure exit
traffic, which attribute by the relaying node.

**Device names need the `devices` collector or the record's own metadata.** Flow records embed their
endpoints' identity, so names resolve even with the devices collector disabled; the collector still
gives better coverage for devices that have not appeared in a flow yet.

## Both ingestion paths feed it

The polling collector and the streaming receiver share one flow processor, so the view is complete
regardless of `collectors.flowlogs.source`. Cross-source de-duplication applies here exactly as it does
to the metrics, so a window delivered twice is counted once.

## The JSON API

The page is a shell that polls `/api/flows.json`; that endpoint is a supported read-only API in its own
right, behind the same auth.

| Parameter | Default | Meaning |
|---|---|---|
| `window` | `1h` | Lookback span. Clamped to `[1m, flows.retention]`. |
| `end` | now | Right-hand edge of the window, RFC3339. Clamped to what is retained. |
| `top` | `20` | Length of each ranked list. Capped at 200. |
| `recent` | `200` | Raw connections returned. Capped at 1000; `0` omits them. |
| `tailnet` | first | Which tailnet to report on. An unknown name is a 404, never another tailnet's data. |

```console
$ curl -sH "Authorization: Bearer $TOKEN" \
    'http://127.0.0.1:9091/api/flows.json?window=15m&top=5&recent=0' | jq '.result.totals'
```

## What it is not

It is not a replacement for dashboards, alerting, or retention. It holds hours, not weeks; it cannot
join across time ranges; and it has no alerting. For any of that, use the OTLP export and the
[dashboards](dashboards.md) and [alert rules](alerts.md) that ship with the project.
