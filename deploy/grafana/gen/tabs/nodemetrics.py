"""tab_nodemetrics() — moved out of build.py in the module split."""

from builder import (barchart_opts, bargauge_opts, derp_byte_fraction, lot, organize,
                     panel, prom_t, RI, row, stat_opts, thr, ts_custom, ts_opts)
from maps import UP_MAP


def tab_nodemetrics():
    health = [
        (panel("Targets up", "stat", [prom_t("count(%s == 1) or vector(0)" % lot("tailscale_node_up_ratio", "15m"))],
               unit="short", thresholds=thr([(None, "red"), (1, "green")]), options=stat_opts(color="background")), 5, 5),
        (panel("Targets total", "stat", [prom_t("count(%s) or vector(0)" % lot("tailscale_node_up_ratio", "15m"))],
               unit="short", options=stat_opts()), 5, 5),
        (panel("Discovery OK", "stat", [prom_t("max(%s)" % lot("tailscale2otel_nodemetrics_discovery_success_ratio"))],
               mappings=UP_MAP, thresholds=thr([(None, "red"), (1, "green")]), options=stat_opts(color="background")), 5, 5),
        (panel("Discovered targets", "stat", [prom_t("max(%s)" % lot("tailscale2otel_nodemetrics_discovery_targets"))],
               unit="short", options=stat_opts()), 5, 5),
        (panel("Node up", "table", [prom_t(lot("tailscale_node_up_ratio", "15m"), instant=True, fmt="table")],
               transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                  "service_instance_id", "service_name", "service_namespace"],
                                         rename={"tailscale_node": "Node", "Value": "Up"})],
               desc="Per-target scrape health (1=up)."), 4, 5),
    ]
    traffic = [
        (panel("Inbound bytes/s", "timeseries",
               [prom_t("sum by (tailscale_node) (rate(tailscaled_inbound_bytes_total[%s]))" % RI, legend="{{tailscale_node}}")],
               unit="Bps", custom=ts_custom(), options=ts_opts(placement="right")), 12, 7),
        (panel("Outbound bytes/s", "timeseries",
               [prom_t("sum by (tailscale_node) (rate(tailscaled_outbound_bytes_total[%s]))" % RI, legend="{{tailscale_node}}")],
               unit="Bps", custom=ts_custom(), options=ts_opts(placement="right")), 12, 7),
        (panel("Inbound packets/s", "timeseries",
               [prom_t("sum by (tailscale_node) (rate(tailscaled_inbound_packets_total[%s]))" % RI, legend="{{tailscale_node}}")],
               unit="pps", custom=ts_custom(), options=ts_opts()), 12, 7),
        (panel("Outbound packets/s", "timeseries",
               [prom_t("sum by (tailscale_node) (rate(tailscaled_outbound_packets_total[%s]))" % RI, legend="{{tailscale_node}}")],
               unit="pps", custom=ts_custom(), options=ts_opts()), 12, 7),
        (panel("Outbound dropped packets/s by node", "timeseries",
               [prom_t("sum by (tailscale_node, reason) (rate(tailscaled_outbound_dropped_packets_total[%s]))" % RI,
                       legend="{{tailscale_node}} {{reason}}")],
               unit="pps", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Outbound packets dropped by tailscaled per node — a connectivity-degradation signal."), 24, 7),
    ]
    routing = [
        (panel("Advertised routes", "table", [prom_t(lot("tailscaled_advertised_routes", "15m"), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"tailscale_node": "Node", "Value": "Advertised"})]), 8, 7),
        (panel("Approved routes", "table", [prom_t(lot("tailscaled_approved_routes", "15m"), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"tailscale_node": "Node", "Value": "Approved"})]), 8, 7),
        (panel("Health messages", "table", [prom_t(lot("tailscaled_health_messages", "15m"), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"tailscale_node": "Node", "Value": "Messages"})],
               desc="tailscaled self-reported health warnings."), 8, 7),
        (panel("Home DERP region", "table", [prom_t(lot("tailscaled_home_derp_region_id", "15m"), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"tailscale_node": "Node", "Value": "Region ID"})]), 12, 6),
        (panel("Peer relay endpoints", "table", [prom_t(lot("tailscaled_peer_relay_endpoints", "15m"), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"tailscale_node": "Node", "Value": "Endpoints"})]), 12, 6),
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
               unit="Bps", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 16, 6),
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
        (panel("Peer-relay throughput by node", "timeseries",
               [prom_t("sum by (tailscale_node) (rate(tailscale_node_peer_relay_io_bytes_total[%s]))" % RI,
                       legend="{{tailscale_node}}")],
               unit="Bps", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Bytes each node forwarded while acting as a peer relay (curated "
                    "tailscale.node.peer_relay.io)."), 12, 7),
        (panel("Path mix (now)", "barchart",
               [prom_t("sum by (tailscale_path) (rate(tailscale_node_io_bytes_total[%s]))" % RI,
                       legend="{{tailscale_path}}", instant=True, fmt="table")],
               unit="Bps", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Current data-plane byte rate by curated path bucket (direct / DERP / peer-relay)."), 12, 6),
        # The three panels below consume dimensions #429 restored. Before that,
        # curation folded five of the seven documented drop reasons into "other"
        # and read no peer-relay labels at all, so none of this was answerable.
        (panel("Dropped packets by reason", "timeseries",
               [prom_t("sum by (tailscale_drop_reason) (rate(tailscale_node_packets_dropped_total[%s]))" % RI,
                       legend="{{tailscale_drop_reason}}")],
               unit="pps", custom=ts_custom(stack="normal", fill=25), options=ts_opts(),
               desc="Why tailscaled dropped packets, across the seven documented reasons. `acl` is the "
                    "packet filter doing its job and is usually benign; `too_short` / `fragment` / "
                    "`unknown_protocol` point at a malformed or misrouted sender; `error` is the one to "
                    "escalate. `other` means a reason this build does not recognize yet."), 12, 7),
        (panel("Peer-relay throughput by transport", "timeseries",
               [prom_t("sum by (tailscale_peer_relay_transport_in, tailscale_peer_relay_transport_out) "
                       "(rate(tailscale_node_peer_relay_io_bytes_total[%s]))" % RI,
                       legend="{{tailscale_peer_relay_transport_in}} -> {{tailscale_peer_relay_transport_out}}")],
               unit="Bps", custom=ts_custom(), options=ts_opts(),
               desc="Relayed bytes by ingress and egress transport. A heavy udp4 -> udp6 (or reverse) "
                    "share means the relay is bridging address families rather than just forwarding."), 12, 7),
        (panel("Peer-relay endpoints by state", "timeseries",
               [prom_t("sum by (tailscale_peer_relay_state) (%s)" % lot("tailscale_node_peer_relay_endpoints_ratio", "15m"),
                       legend="{{tailscale_peer_relay_state}}")],
               custom=ts_custom(), options=ts_opts(),
               desc="Peer-relay endpoints split by connection state. A persistently high `connecting` "
                    "count against a low `open` count is a relay failing to establish sessions."), 12, 6),
    ]
    return [row("Scraper health", health), row("Traffic (tailscaled)", traffic),
            row("Connection paths (DERP vs direct)", paths, present="has_path"),
            row("Client health (curated)", clienthealth, present="has_node_curated"),
            row("DERP regions (tailnet rollup)", derprollup, present="has_derp_rollup"),
            row("Routing & health", routing)]
