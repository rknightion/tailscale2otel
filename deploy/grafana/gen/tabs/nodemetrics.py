"""tab_nodemetrics() — moved out of build.py in the module split."""

from builder import (BAR_NOISE, category_bar_opts, bargauge_opts, derp_byte_fraction, lot, organize,
                     panel, prom_t, RI, row, sentinel, stat_opts, thr, ts_custom, ts_opts)
from maps import UP_MAP
from builder import DASHBOARD  # #526: wave 1 leaves every sentinel dashboard-level

# Curated node-metric families (#402). Named once so a panel cannot typo one into a
# silently empty chart — internal/catalog/dashboardrefs_test.go only catches a name
# the catalog does not have at all, not a name used in the wrong place.
NODE_PACKETS = "tailscale_node_packets_total"
NODE_DROPPED = "tailscale_node_packets_dropped_total"
NODE_RELAY_BYTES = "tailscale_node_peer_relay_io_bytes_total"
NODE_RELAY_PACKETS = "tailscale_node_peer_relay_packets_total"
NODE_RELAY_ENDPOINTS = "tailscale_node_peer_relay_endpoints_ratio"

# An ACL drop is the packet filter enforcing policy, i.e. the tailnet working as
# configured. Charting it in the same panel as `error`/`too_short`/`fragment` — worse,
# under a shared red threshold — trains operators to ignore drop panels entirely, so
# #402 splits the two and only the error class carries escalation colouring.
DROP_POLICY = 'tailscale_drop_reason="acl"'
DROP_NOT_POLICY = 'tailscale_drop_reason!="acl"'


def tab_nodemetrics(scope):
    # Presence sentinels this tab declares (moved from variables.py, #495).
    # has_nodemetrics gates the whole "Node Metrics" tab (see build.py's tab_defs) — declared
    # here because this is the tab it names/gates, even though build.py (not this function's
    # own rows) is the actual consumer.
    sentinel("has_nodemetrics", "tailscale_node_up_ratio", DASHBOARD)
    sentinel("has_path", "tailscaled_inbound_bytes_total", DASHBOARD)
    sentinel("has_derp_rollup", "tailscale_derp_region_devices_ratio", DASHBOARD)
    # has_dropped was dead until #402: the raw dropped-packets panel it names sat in the
    # ungated "Traffic (tailscaled)" row, so on a target whose tailscaled exposes no
    # dropped-packet counters it rendered permanently empty instead of hiding. It now
    # gates its own row.
    sentinel("has_dropped", "tailscaled_outbound_dropped_packets_total", DASHBOARD)
    sentinel("has_node_curated", "tailscale_node_io_bytes_total", DASHBOARD)

    health = [
        (panel("Targets up", "stat", [prom_t("count(%s == 1) or vector(0)" % lot("tailscale_node_up_ratio", "15m"))],
               unit="short", thresholds=thr([(None, "red"), (1, "green")]), options=stat_opts(color="background"),
               desc="Node-metrics scrape targets currently reachable."), 5, 5),
        (panel("Targets total", "stat", [prom_t("count(%s) or vector(0)" % lot("tailscale_node_up_ratio", "15m"))],
               unit="short", options=stat_opts(),
               desc="Node-metrics scrape targets configured/discovered, up or down."), 5, 5),
        (panel("Discovery OK", "stat", [prom_t("max(%s)" % lot("tailscale2otel_nodemetrics_discovery_success_ratio"))],
               mappings=UP_MAP, thresholds=thr([(None, "red"), (1, "green")]), options=stat_opts(color="background"),
               desc="Whether the last node-metrics target-discovery pass succeeded."), 5, 5),
        (panel("Discovered targets", "stat", [prom_t("max(%s)" % lot("tailscale2otel_nodemetrics_discovery_targets"))],
               unit="short", options=stat_opts(),
               desc="Scrape targets found by the last node-metrics discovery pass."), 5, 5),
        (panel("Node up", "table", [prom_t(lot("tailscale_node_up_ratio", "15m"), instant=True, fmt="table")],
               transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                  "service_instance_id", "service_name", "service_namespace"],
                                         rename={"tailscale_node": "Node", "Value": "Up"})],
               desc="Per-target scrape health (1=up)."), 4, 5),
    ]
    traffic = [
        (panel("Inbound bytes/s", "timeseries",
               [prom_t("sum by (tailscale_node) (rate(tailscaled_inbound_bytes_total[%s]))" % RI, legend="{{tailscale_node}}")],
               unit="Bps", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Raw tailscaled inbound byte rate, per node."), 12, 7),
        (panel("Outbound bytes/s", "timeseries",
               [prom_t("sum by (tailscale_node) (rate(tailscaled_outbound_bytes_total[%s]))" % RI, legend="{{tailscale_node}}")],
               unit="Bps", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Raw tailscaled outbound byte rate, per node."), 12, 7),
        (panel("Inbound packets/s", "timeseries",
               [prom_t("sum by (tailscale_node) (rate(tailscaled_inbound_packets_total[%s]))" % RI, legend="{{tailscale_node}}")],
               unit="pps", custom=ts_custom(), options=ts_opts(),
               desc="Raw tailscaled inbound packet rate, per node."), 12, 7),
        (panel("Outbound packets/s", "timeseries",
               [prom_t("sum by (tailscale_node) (rate(tailscaled_outbound_packets_total[%s]))" % RI, legend="{{tailscale_node}}")],
               unit="pps", custom=ts_custom(), options=ts_opts(),
               desc="Raw tailscaled outbound packet rate, per node."), 12, 7),
    ]
    # Raw tailscaled drop counters, gated on their own presence (#402). Kept apart from
    # the curated drop row below: this one carries tailscaled's own unfolded `reason`
    # label, the curated one carries the bounded tailscale_drop_reason admit-set.
    rawdrops = [
        (panel("Outbound dropped packets/s by node", "timeseries",
               [prom_t("sum by (tailscale_node, reason) (rate(tailscaled_outbound_dropped_packets_total[%s]))" % RI,
                       legend="{{tailscale_node}} {{reason}}")],
               unit="pps", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Outbound packets dropped by tailscaled per node, with the raw upstream "
                    "`reason` label. `acl` here is the packet filter enforcing policy, not a "
                    "fault — the curated row below separates the two. Needs the node-metrics "
                    "scraper (`node_metrics.enabled`) against a tailscaled exposing "
                    "`tailscaled_outbound_dropped_packets_total`."), 24, 7),
    ]
    routing = [
        (panel("Advertised routes", "table", [prom_t(lot("tailscaled_advertised_routes", "15m"), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"tailscale_node": "Node", "Value": "Advertised"})],
               desc="Per-node count of subnet routes advertised, from raw tailscaled state."), 8, 7),
        (panel("Approved routes", "table", [prom_t(lot("tailscaled_approved_routes", "15m"), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"tailscale_node": "Node", "Value": "Approved"})],
               desc="Per-node count of subnet routes approved, from raw tailscaled state."), 8, 7),
        (panel("Health messages", "table", [prom_t(lot("tailscaled_health_messages", "15m"), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"tailscale_node": "Node", "Value": "Messages"})],
               desc="tailscaled self-reported health warnings."), 8, 7),
        (panel("Home DERP region", "table", [prom_t(lot("tailscaled_home_derp_region_id", "15m"), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"tailscale_node": "Node", "Value": "Region ID"})],
               desc="Each node's current home DERP region ID, from raw tailscaled state."), 12, 6),
        (panel("Peer relay endpoints", "table", [prom_t(lot("tailscaled_peer_relay_endpoints", "15m"), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"tailscale_node": "Node", "Value": "Endpoints"})],
               desc="Per-node count of peer-relay endpoints, from raw tailscaled state."), 12, 6),
    ]
    paths = [
        (panel("% traffic via DERP relay by node", "timeseries",
               [prom_t(derp_byte_fraction("tailscale_node"), legend="{{tailscale_node}}")],
               unit="percentunit", min_=0, max_=1, custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Fraction of each node's traffic relayed via DERP rather than sent direct. Sustained "
                    "high values indicate NAT-traversal problems (added latency)."), 12, 7),
        (panel("Throughput by path", "timeseries",
               [prom_t("sum by (path) (rate(tailscaled_inbound_bytes_total[%s]) + rate(tailscaled_outbound_bytes_total[%s]))"
                       % (RI, RI), legend="{{path}}")],
               unit="Bps", custom=ts_custom(stack="normal", fill=25), options=ts_opts(),
               desc="Total tailnet throughput split by path: DERP relay vs direct IPv4 vs direct IPv6."), 12, 7),
        (panel("Fleet DERP share (now)", "stat",
               [prom_t(derp_byte_fraction(), instant=True)],
               unit="percentunit", thresholds=thr([(None, "green"), (0.3, "yellow"), (0.6, "red")]),
               options=stat_opts(color="background"),
               desc="Fleet-wide fraction of bytes relayed via DERP."), 8, 6),
        (panel("Path mix (DERP / IPv4 / IPv6)", "barchart",
               [prom_t("sum by (path) (rate(tailscaled_inbound_bytes_total[%s]) + rate(tailscaled_outbound_bytes_total[%s]))"
                       % (RI, RI), legend="{{path}}", instant=True, fmt="table")],
               unit="Bps", options=category_bar_opts("Path"),
               transformations=[organize(exclude=BAR_NOISE, rename={"path": "Path", "Value": "Bytes/s"})],
               desc="Current total throughput split by raw tailscaled path (DERP/IPv4/IPv6)."), 16, 6),
    ]
    derprollup = [
        (panel("Best latency per DERP region", "bargauge",
               [prom_t("max by (tailscale_derp_region) (%s)" % lot("tailscale_derp_region_latency_min_seconds"), legend="{{tailscale_derp_region}}")],
               unit="s", options=bargauge_opts(),
               desc="Best (minimum) device→DERP-region latency across the tailnet, per region."), 8, 7),
        (panel("Devices per DERP region", "bargauge",
               [prom_t("max by (tailscale_derp_region) (%s)" % lot("tailscale_derp_region_devices_ratio"), legend="{{tailscale_derp_region}}")],
               unit="short", options=bargauge_opts(),
               desc="Number of devices reporting latency to each DERP region."), 8, 7),
        (panel("Preferred DERP region distribution", "bargauge",
               [prom_t("max by (tailscale_derp_region) (%s)" % lot("tailscale_derp_region_preferred_ratio"), legend="{{tailscale_derp_region}}")],
               unit="short", options=bargauge_opts(),
               desc="Number of devices that prefer each DERP region."), 8, 7),
    ]
    # #172: curated client-health panels over the tailscale_node_* family (#171). Unlike the raw
    # tailscaled_* paths row above, the curated tailscale_path label folds into direct / derp /
    # peer_relay, so the traffic-mix panel separates the peer-relay bucket; health messages are
    # broken out by curated tailscale_health_type rather than a per-node count.
    clienthealth = [
        (panel("Active health warnings by type", "timeseries",
               [prom_t("sum by (tailscale_health_type) (%s)" % lot("tailscale_node_health_messages_ratio", "15m"),
                       legend="{{tailscale_health_type}}")],
               unit="short", custom=ts_custom(style="line", fill=10, points="always"),
               options=ts_opts(placement="right"),
               desc="Active tailscaled client health-warning messages across the fleet, by health type "
                    "(curated tailscale.node.health_messages; alert ts2o-node-health-warnings)."), 12, 7),
        (panel("Traffic mix by path (direct / DERP / peer-relay)", "timeseries",
               [prom_t("sum by (tailscale_path) (rate(tailscale_node_io_bytes_total[%s]))" % RI, legend="{{tailscale_path}}")],
               unit="Bps", custom=ts_custom(stack="normal", fill=25), options=ts_opts(),
               desc="Tailnet data-plane throughput split by curated path bucket: direct, DERP relay, or "
                    "peer relay (curated tailscale.node.io — includes the peer_relay bucket the raw "
                    "tailscaled path label does not separate)."), 12, 7),
        (panel("Packets/s by path (curated)", "timeseries",
               [prom_t("sum by (tailscale_path) (rate(%s[%s]))" % (NODE_PACKETS, RI),
                       legend="{{tailscale_path}}")],
               unit="pps", custom=ts_custom(stack="normal", fill=25), options=ts_opts(),
               desc="Data-plane PACKET rate by curated path bucket — the packet counterpart of "
                    "Traffic mix by path. Read alongside it: a path whose packet share far "
                    "exceeds its byte share is carrying small frames (keepalives, handshake "
                    "churn) rather than useful payload, which the byte panel alone hides."), 12, 7),
        (panel("Path mix (now)", "barchart",
               [prom_t("sum by (tailscale_path) (rate(tailscale_node_io_bytes_total[%s]))" % RI,
                       instant=True, fmt="table")],
               unit="Bps", options=category_bar_opts("Path"),
               transformations=[organize(exclude=BAR_NOISE,
                                         rename={"tailscale_path": "Path", "Value": "Bytes/s"})],
               desc="Current data-plane byte rate by curated path bucket (direct / DERP / peer-relay)."), 12, 6),
        (panel("Home DERP region (curated)", "table",
               [prom_t(lot("tailscale_node_derp_home_region_ratio", "15m"), instant=True, fmt="table")],
               unit="short",
               transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                  "service_instance_id", "service_name", "service_namespace"],
                                         rename={"tailscale_node": "Node", "Value": "Home region ID"})],
               desc="Each node's current home DERP region, carried as the region ID in the "
                    "value column. The `_ratio` suffix is the OTLP unit-`1` gauge naming rule, "
                    "not a fraction — read the number as an ID. It is NOT joinable with the "
                    "region NAMES on the DERP-region rollup panels below; the API exposes no "
                    "DERP map to translate between an ID and a name."), 12, 6),
    ]
    # Packet drops and peer relay (#402), split out of the client-health row because the
    # dimensions #429 restored are only useful when the reasons are charted apart. Before
    # #429 curation folded five of the seven documented drop reasons into "other" and read
    # no peer-relay labels at all, so none of this was answerable.
    #
    # Policy vs error is the load-bearing split: an `acl` drop is the packet filter
    # enforcing the tailnet policy and is the expected steady state on any tailnet with a
    # real ACL, so it gets its own panel with no threshold colouring. Only the non-`acl`
    # reasons carry an escalation threshold. A single panel summing both, red, is how a
    # drop chart becomes background noise nobody reads.
    dropsrelay = [
        (panel("Policy drops (ACL)", "timeseries",
               [prom_t("sum by (network_io_direction) (rate(%s{%s}[%s]))" % (NODE_DROPPED, DROP_POLICY, RI),
                       legend="acl {{network_io_direction}}")],
               unit="pps", custom=ts_custom(stack="normal", fill=25), options=ts_opts(),
               desc="Packets the tailscaled packet filter dropped because the tailnet policy "
                    "does not permit them. This is the ACL working, NOT a fault, so the panel "
                    "carries no error colouring and nothing here should be escalated on its "
                    "own. A step change is worth correlating with an ACL edit (Security & "
                    "Audit tab); a sustained level is normal. Error-class drops are the panel "
                    "beside this one."), 12, 7),
        (panel("Error & malformed drops by reason", "timeseries",
               [prom_t("sum by (tailscale_drop_reason) (rate(%s{%s}[%s]))" % (NODE_DROPPED, DROP_NOT_POLICY, RI),
                       legend="{{tailscale_drop_reason}}")],
               unit="pps", custom=ts_custom(stack="normal", fill=25), options=ts_opts(),
               thresholds=thr([(None, "green"), (1, "yellow"), (10, "red")]),
               desc="Every documented drop reason EXCEPT `acl`. `error` is the one to escalate; "
                    "`too_short` / `fragment` / `unknown_protocol` point at a malformed or "
                    "misrouted sender; `multicast` / `link_local_unicast` are traffic the "
                    "tailnet does not carry and are usually a chatty LAN rather than a fault; "
                    "`other` is a reason this build does not recognize yet, so a rising `other` "
                    "means the upstream vocabulary moved. Sustained `error`/`unknown_protocol`/"
                    "`other` is the escalation signal, not a single spike."), 12, 7),
        (panel("Drop share by reason", "timeseries",
               [prom_t("sum by (tailscale_drop_reason) (rate(%s[%s])) / "
                       "scalar(clamp_min(sum(rate(%s[%s])) + sum(rate(%s[%s])), 1))"
                       % (NODE_DROPPED, RI, NODE_PACKETS, RI, NODE_DROPPED, RI),
                       legend="{{tailscale_drop_reason}}")],
               unit="percentunit", min_=0, max_=1, custom=ts_custom(), options=ts_opts(),
               desc="Each reason's share of all offered packets (delivered + dropped), so a "
                    "drop rate can be read against traffic volume instead of in isolation. "
                    "Deliberately unthresholded and including `acl`: the ratio is context, and "
                    "a high but steady ACL share is a strict policy, not an incident."), 12, 7),
        (panel("Peer-relay throughput by node", "timeseries",
               [prom_t("sum by (tailscale_node) (rate(%s[%s]))" % (NODE_RELAY_BYTES, RI),
                       legend="{{tailscale_node}}")],
               unit="Bps", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Bytes each node forwarded while acting as a peer relay (curated "
                    "tailscale.node.peer_relay.io)."), 12, 7),
        (panel("Peer-relay throughput by transport", "timeseries",
               [prom_t("sum by (tailscale_peer_relay_transport_in, tailscale_peer_relay_transport_out) "
                       "(rate(%s[%s]))" % (NODE_RELAY_BYTES, RI),
                       legend="{{tailscale_peer_relay_transport_in}} -> {{tailscale_peer_relay_transport_out}}")],
               unit="Bps", custom=ts_custom(), options=ts_opts(),
               desc="Relayed bytes by ingress and egress transport. A heavy udp4 -> udp6 (or reverse) "
                    "share means the relay is bridging address families rather than just forwarding."), 12, 7),
        (panel("Peer-relay packets/s by transport", "timeseries",
               [prom_t("sum by (tailscale_peer_relay_transport_in, tailscale_peer_relay_transport_out) "
                       "(rate(%s[%s]))" % (NODE_RELAY_PACKETS, RI),
                       legend="{{tailscale_peer_relay_transport_in}} -> {{tailscale_peer_relay_transport_out}}")],
               unit="pps", custom=ts_custom(), options=ts_opts(),
               desc="Relayed PACKET rate by the same transport transition. Held against the byte "
                    "panel it gives the mean relayed frame size: packets climbing while bytes "
                    "hold flat is a relay carrying overhead rather than payload."), 12, 7),
        (panel("Peer-relay endpoints by state", "timeseries",
               [prom_t("sum by (tailscale_peer_relay_state) (%s)" % lot(NODE_RELAY_ENDPOINTS, "15m"),
                       legend="{{tailscale_peer_relay_state}}")],
               custom=ts_custom(), options=ts_opts(),
               desc="Peer-relay endpoints split by connection state. A persistently high `connecting` "
                    "count against a low `open` count is a relay failing to establish sessions."), 12, 6),
        (panel("Peer-relay endpoint share by state", "timeseries",
               [prom_t("sum by (tailscale_peer_relay_state) (%s) / "
                       "scalar(clamp_min(sum(%s), 1))"
                       % (lot(NODE_RELAY_ENDPOINTS, "15m"), lot(NODE_RELAY_ENDPOINTS, "15m")),
                       legend="{{tailscale_peer_relay_state}}")],
               unit="percentunit", min_=0, max_=1, custom=ts_custom(stack="normal", fill=25),
               options=ts_opts(),
               desc="The same endpoint states as a share of all peer-relay endpoints, so a fleet "
                    "that grows does not look like a relay regression. Sustained non-`open` "
                    "share is the shape to alert on; a transient `connecting` spike after a "
                    "restart is not."), 12, 6),
    ]
    return [row("Scraper health", health), row("Traffic (tailscaled)", traffic),
            row("Packet drops (raw tailscaled)", rawdrops, present="has_dropped"),
            row("Connection paths (DERP vs direct)", paths, present="has_path"),
            row("Client health (curated)", clienthealth, present="has_node_curated"),
            row("Packet drops & peer relay (curated)", dropsrelay, present="has_node_curated"),
            row("DERP regions (tailnet rollup)", derprollup, present="has_derp_rollup"),
            row("Routing & health", routing)]
