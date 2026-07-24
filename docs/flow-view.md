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
registered. Overlay traffic only — see [what the services section counts](#limits-worth-knowing).

**Identity.** Traffic by user, by tag, and by operating system, taken from the node metadata each flow
record carries. Tags break down **individually**, so a device tagged `tag:servers,tag:prod` is counted
under both rather than under a joined label that matches nothing you would search for.

**Who talks to whom.** A tag-to-tag (or user-to-user) matrix: rows are the sending identity, columns
the receiving one, shaded by volume. Click a cell to filter the connection list to exactly that
relationship. See [what the matrix can and cannot show](#what-the-identity-matrix-can-show) below.

**Policy reconciliation.** What the tailnet's own ACL says about the traffic above — see
[reading the policy section](#reading-the-policy-section), which is worth reading before acting on it.

**Path quality.** Whether each peer was reached directly or had to be relayed through DERP, and which
relay carried it — see [reading the path section](#reading-the-path-section).

**Recent connections.** The newest individual connections, with their raw endpoints, the policy's
reading of each one and how each underlay connection was carried, filterable by device, address, port,
service, protocol, verdict or path. The aggregates cannot answer "what exactly was that", so a bounded
number of raw connections are kept alongside them.

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

This matters more here than elsewhere: the page shows device names, addresses, and the user each
device belongs to, unfiltered — see [Privacy](#privacy).

## Privacy

**`/flows` shows what the flow record carried, in full.
[`pii_filter`](configuration.md#pii_filter-pii-identifier-redaction) does not apply to it.**

That filter governs the telemetry this process **exports** — what reaches your OTLP backend, and
whoever can read it. The flow store is a different thing: in memory, never written anywhere, never
sent anywhere, and readable only through the admin-authenticated surface above. Narrowing what you
send onward is not a request to be blinded to your own tailnet, so `emails: false` still leaves the
users breakdown populated here, and `hostnames: false` still leaves the topology graph drawn.

The consequence to be aware of: **the admin token is what protects this data**, on its own. Anyone
you hand it to can see every device name, address and user the control plane reported, whatever your
`pii_filter` says. If that is wider than you want, the answer is the token, not the filter.

> **This changed.** The view previously applied `pii_filter`, so an operator who had switched a
> category off for their backend found it missing here too. See the changelog entry for the release
> that carries it.

## What the identity matrix can show

A matrix cell needs the identity on **both** endpoints, and Tailscale's flow records do not carry all
three fields on both sides. Measured against a live 3-hour capture (18,702 records, 34,680 source →
destination node pairings):

| matrix | both endpoints carry it | usable? |
|---|---|---|
| **Tag → tag** | 24,154 pairings (**70%**) | Yes — this is the one to reach for. |
| **User → user** | 241 pairings (**1%**) | Rarely. A tag-owned device has no user at all, so a machine-to-machine tailnet shows almost nothing. |
| **OS → OS** | 0 | **No.** Not offered. |

**There is no OS matrix, deliberately.** `srcNode` never carries `os` — zero times in 18,702 records —
while `dstNodes` entries do. An OS matrix could therefore only ever be empty, so the page does not
offer one. For the same reason the **Operating system** breakdown is labelled *destination side only*:
it describes what traffic was sent **to**, not what sent it.

The page states the matrix's coverage as a percentage of the window's traffic, so a low number reads as
"most of this traffic has no tags on both ends" rather than as missing data.

## Reading the policy section

Every policy-governed connection is checked against the tailnet's ACL as this evaluator reads it, as
the connection is processed. The ACL comes from the `acl` collector and the tailnet roles from the
`users` collector; with either disabled the section degrades rather than guessing.

**This is a diagnostic, not an audit.** Tailscale carried every connection shown, so it permitted every
connection shown. Anything the section reports is a lead to look into — most often a subnet router or a
VIP service whose semantics this reading does not fully capture — not traffic that got through.

### The four verdicts

| Verdict | Meaning |
|---|---|
| **permitted** | A rule covers the connection in the direction it was observed. |
| **return traffic** | The half of the connection that *established* it is covered. Flow logs report both halves; a policy governs only one. On a live tailnet this was **37% of all connections** — it is normal, not a finding. |
| **not explained** | No rule covers it in either direction, as this evaluator reads the policy. |
| **undecidable** | The policy could not be applied here. Never a finding — see below. |

**"Undecidable" is not "unexplained".** A selector match is yes, no, or *unknown*, and a connection is
reported as unexplained only when **every** rule definitively fails. An undeclared `group:`, a `svc:`
VIP service, or an endpoint carrying no identity all make a rule undecidable, and the section says so
rather than counting it against you. Collapsing unknown into "no" is what would turn this into a
generator of confident false alarms.

### Traffic no rule explains

Unexplained connections are aggregated into **relationships**: source identity, destination identity,
transport and destination port — the shape a grant would be written in. Each endpoint is named by the
most useful thing known about it: its tags, then its owner, then its device name, then its address.

That aggregation is what makes the output usable. On a live tailnet, 9,786 individually unexplained
connections were **three relationships**, all involving one tag talking to two LAN addresses behind a
subnet router.

### `tsmp` — Tailscale reporting its own rejections

A connection whose transport reads `tsmp` is not traffic anybody sent. TSMP is Tailscale's own
ICMP-ish protocol — IP protocol 99, which IANA reserves for "any private encryption scheme" — carried
only between nodes inside the WireGuard tunnel, and it exists to say **why something failed**: an ACL
drop, a refused connection, no route.

Two things make it worth reading rather than dismissing as an odd protocol number:

- **The source is the node that did the rejecting.** A `tsmp` flow travels in the opposite direction to
  the traffic it is about, so it names the end that dropped something *and* the end whose traffic was
  dropped. That is independent corroboration of a **not explained** verdict, arriving from the other
  side of the connection.
- **It never appears in a packet capture.** tailscaled neither accepts these from the host network
  stack nor sends them to it, so `tcpdump` will not show them on any interface — including the
  Tailscale one. On a live tailnet a 60-second capture of the sending host's LAN interface took in
  1,978,983 packets and matched none of them while the API reported the flow continuously. Chasing the
  packets is a dead end; the flow log is the only place they are visible.

TSMP has no ports, so these connections carry `:0` — Tailscale's placeholder for a protocol that has
none, not a connection to port zero.

### Rules that permitted nothing

The other half: which rules never covered anything in the window. **The window is stated on the list,
and it is the whole point** — a rule can be entirely healthy and idle. NTP, ICMP and break-glass access
routinely go hours without firing; on a live tailnet 9 of 19 rules were idle over three hours and
almost all of them legitimately so. Widen the window before concluding a rule is dead.

### What the evaluator covers

Selector families: `*`, `tag:`, named users, `group:`, `ipset:`,
`autogroup:{owner,admin,member,self,internet}`, host aliases, IP literals and CIDRs. Port specs: `*`,
bare ports (tcp+udp), `proto:port`, `proto:*` and ranges. Both `grants` (the current syntax) and the
legacy `acls` accept entries.

Known limits, which the section reports honestly rather than guessing around:

- **`svc:` (VIP services) is undecidable.** Resolving one needs a mapping this evaluator does not have.
- **`autogroup:admin` includes the Owner.** A modelling choice, not something the API states.
- **Only `accept` rules are evaluated.** `ssh`, `nodeAttrs`, `postures` and `autoApprovers` govern
  other things entirely.
- **`physicalTraffic` is not evaluated.** It is the WireGuard underlay, not a connection a policy
  describes; those connections show no verdict at all.
- **Exit traffic** carries no destination, so it is evaluated only against `autogroup:internet`.
- **`ip: ["*"]` covers every protocol**, including ICMP — it is a wildcard over protocol as well as
  port. A *bare* port number is what implies tcp/udp only.

Reconciliation reads the identity the flow record carried — the same identity the rest of the page
shows, and for the same reason (see [Privacy](#privacy)). An endpoint the record carried nothing
identifying for appears as `unidentified`.

## Reading the path section

Tailscale connects two nodes directly when it can and falls back to relaying through a **DERP** server
when it cannot. Both are end-to-end encrypted; a relayed path is simply slower and lower throughput,
because the traffic goes via Tailscale's infrastructure instead of straight between the two machines.

This is read from `physicalTraffic` — the WireGuard underlay, which reports the endpoint each peer was
actually reached at. Three values:

| Path | Meaning |
|---|---|
| `direct_ipv4` | The two nodes reached each other directly over IPv4. |
| `direct_ipv6` | The two nodes reached each other directly over IPv6. |
| `derp` | The connection was relayed. Tailscale writes the loopback marker `127.3.3.40` in place of an endpoint address, and the DERP **region ID** in place of the port. |

**The marker is never shown as a device.** A relayed connection's destination is that loopback
marker, so the connection list shows a dash where the destination device name would be and keeps the
raw `127.3.3.40:<region>` beside it. The peer is on the *source* side of a physical record — that is
how the API reports it — so it is still named there, and the per-peer table below is keyed on it.

**The counts are connections, not bytes**, and the two are usually far apart. On a live tailnet 11.6%
of underlay connections were relayed but only 0.4% of the bytes: handshakes and keepalives relay while
bulk transfer finds a direct path. The per-peer table shows both so neither can be read as the other.

**Regions are shown as IDs.** Tailscale's API does not serve its DERP map, so there is no supported
source for the ID-to-name mapping. A built-in name table would go stale silently and mislabel a region
you were about to act on, so the raw ID is what is shown. Tailscale's own published DERP map is where
to look one up.

**A peer being relayed is a lead, not a fault.** The usual causes are a hard NAT in front of one of the
two nodes, UDP blocked on the path, or genuinely no route between the two networks. Start with
`tailscale netcheck` on the relayed peer.

Two things this deliberately does not claim:

- **Peer-relay paths are not distinguished from direct ones.** A flow record reports the endpoint a
  peer was reached at, and a Tailscale peer relay looks like an ordinary endpoint in that field.
- **A physical connection with no endpoint gets no path at all**, rather than being counted as direct.
  "We cannot tell" must not read as good news.

Note also that the peer here is named by its **device name** only — never by its tags, unlike the
unexplained-relationship list. The question is which machine to go and look at, and a row keyed on
`tag:servers` would merge every tagged server into one.

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

**The destination-services section counts overlay traffic only.** A physical connection describes the
WireGuard underlay — *how* two nodes reached each other — so its destination port is the ephemeral one
the peer happened to be listening on, and on a relayed path it is not a port at all but the DERP region
ID written where a port would go. Neither names a service, so neither is counted here; on a live
tailnet the underlay was half the section's bytes and both of its top two rows. Nothing is hidden by
this: the underlay endpoint is on the connection list verbatim, its direct-versus-relayed split is the
[path section](#reading-the-path-section), and its bytes count toward the totals and every other
breakdown.

**Device names need the `devices` collector or the record's own metadata.** Flow records embed their
endpoints' identity, so names resolve even with the devices collector disabled; the collector still
gives better coverage for devices that have not appeared in a flow yet.

## Every ingestion path feeds it

The polling collector, the streaming receiver and the object-store reader share one flow processor, so
the view is complete regardless of `collectors.flowlogs.source`. Cross-source de-duplication applies
here exactly as it does to the metrics, so a window delivered twice is counted once.

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

Alongside the ranked lists, `result` carries `tag_matrix`, `user_matrix` and `os_matrix` — each an
array of `{src, dst, counts}` cells ranked by bytes and capped at 400. Entries in `recent` carry the
endpoint identity (`src_user`, `src_tags`, `src_os` and their `dst_` counterparts) as well as the raw
addresses, plus that connection's own `verdict`, `reversed` and `rule`, and — on underlay connections
only — its `path` and `derp_region`.

`result.ports` is `{port, transport, service, counts}` per destination endpoint, from the **overlay**
traffic types only — a physical entry's port is a WireGuard underlay port, or a DERP region ID on a
relayed path, and neither is a service. Its counts therefore sum to less than `result.totals`.

Path quality is three more fields on `result`:

| Field | Contents |
|---|---|
| `result.paths` | Connection counts per path: `direct_ipv4`, `direct_ipv6`, `derp`. Empty when the window held no underlay traffic. |
| `result.derp_regions` | `{label, counts}` per DERP **region ID**, for the relayed share only. |
| `result.peer_paths` | `{peer, direct, relayed}` per peer, ranked with the relayed ones first. `direct` folds both IP families. |

```console
$ # Which peers are being relayed, and how much of their traffic.
$ curl -sH "Authorization: Bearer $TOKEN" \
    'http://127.0.0.1:9091/api/flows.json?window=6h&recent=0' \
  | jq -r '.result.peer_paths[] | select(.relayed.flows > 0)
           | "\(.peer)  \(.relayed.flows)/\(.relayed.flows + .direct.flows) conns relayed"'
```

Policy reconciliation spans two places. `result` carries what the policy *said* about the window:

| Field | Contents |
|---|---|
| `result.verdicts` | Connection counts per verdict: `permitted`, `permitted_reverse`, `no_rule`, `undetermined`. Empty when no policy was in force — which is **not** the same as everything being permitted. |
| `result.unexplained` | `{src, dst, transport, port, counts}` relationships nothing explained, ranked by bytes. |
| `result.rules` | `{rule, counts}` per rule index that permitted something. Complete and unranked, so subtracting it from `policy.rules` gives the rules that permitted nothing. |

and `policy` carries what the policy *is*: `available`, an optional compile `error`, and `rules` as
`{index, kind, source}` in document order. `index` is the key `result.rules` joins on.

```console
$ # Relationships the policy does not explain, busiest first.
$ curl -sH "Authorization: Bearer $TOKEN" \
    'http://127.0.0.1:9091/api/flows.json?window=6h&recent=0' \
  | jq -r '.result.unexplained[] | "\(.src) -> \(.dst)  \(.transport)/\(.port // "-")  \(.counts.flows) conns"'

$ # Rules that permitted nothing in the window.
$ curl -sH "Authorization: Bearer $TOKEN" \
    'http://127.0.0.1:9091/api/flows.json?window=6h&recent=0' \
  | jq -r '[.result.rules[].rule] as $used | .policy.rules[] | select(.index | IN($used[]) | not) | .source'
```

```console
$ curl -sH "Authorization: Bearer $TOKEN" \
    'http://127.0.0.1:9091/api/flows.json?window=15m&top=5&recent=0' | jq '.result.totals'

$ # Which tags talk to which, busiest first.
$ curl -sH "Authorization: Bearer $TOKEN" \
    'http://127.0.0.1:9091/api/flows.json?window=6h&recent=0' \
  | jq -r '.result.tag_matrix[] | "\(.src) -> \(.dst)  \(.counts.tx_bytes + .counts.rx_bytes)"'
```

## What it is not

It is not a replacement for dashboards, alerting, or retention. It holds hours, not weeks; it cannot
join across time ranges; and it has no alerting. For any of that, use the OTLP export and the
[dashboards](dashboards.md) and [alert rules](alerts.md) that ship with the project.
