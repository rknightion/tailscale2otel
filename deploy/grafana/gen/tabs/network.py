"""tab_network() — moved out of build.py in the module split."""

from builder import (barchart_opts, loki_t, lot, organize, panel, PII, prom_t, RI, row,
                     stat_opts, thr, ts_custom, ts_opts)


def tab_network():
    tf = "{network_transport=~\"$net_transport\", tailscale_traffic_type=~\"$traffic_type\"}"
    # tf, but also exclude unclassified (empty-label) services so the top-services
    # barcharts name every bar instead of falling back to "Value" for the empty group.
    tsf = tf[:-1] + ", tailscale_dst_service!=\"\"}"
    summary = [
        (panel("Throughput (now)", "stat",
               [prom_t("sum(rate(tailscale_network_io_rollup_bytes_total[%s])) or "
                       "sum(rate(tailscale_network_io_bytes_total[%s]))" % (RI, RI), instant=True)],
               unit="Bps", options=stat_opts(graph="area", color="value")), 4, 5),
        (panel("Packets/s (now)", "stat",
               [prom_t("sum(rate(tailscale_network_packets_rollup_total[%s])) or "
                       "sum(rate(tailscale_network_packets_total[%s]))" % (RI, RI), instant=True)],
               unit="pps", options=stat_opts(graph="area")), 4, 5),
        (panel("Flows/s (now)", "stat", [prom_t("sum(rate(tailscale_network_flows_total[%s]))" % RI, instant=True)],
               unit="cps", options=stat_opts(graph="area")), 4, 5),
        (panel("Flows/s by transport", "timeseries",
               [prom_t("sum by (network_transport) (rate(tailscale_network_flows_total%s[%s]))" % (tf, RI), legend="{{network_transport}}")],
               unit="cps", custom=ts_custom(stack="normal"), options=ts_opts()), 6, 5),
        (panel("Flows/s by traffic type", "timeseries",
               [prom_t("sum by (tailscale_traffic_type) (rate(tailscale_network_flows_total%s[%s]))" % (tf, RI), legend="{{tailscale_traffic_type}}")],
               unit="cps", custom=ts_custom(stack="normal"), options=ts_opts()), 6, 5),
    ]
    integrity = [
        (panel("Reporter trust & consistency", "timeseries",
               [prom_t("sum by (trust, consistency) "
                       "(rate(tailscale_network_reporter_observations_total[%s]))" % RI,
                       legend="{{trust}} / {{consistency}}")],
               unit="cps", custom=ts_custom(stack="normal"), options=ts_opts(),
               desc="One bounded observation per flow record. Trust is configured (node-ID "
                    "allowlist), tagged (authoritative device tag), untrusted, or unconfigured; "
                    "consistency compares the verified reporter with the unverified embedded "
                    "source reference. No raw node ID is present."), 12, 7),
        (panel("Observed field omissions", "timeseries",
               [prom_t("sum by (tailscale_traffic_type, field_class) "
                       "(rate(tailscale_network_field_observations_total{state=\"missing\"}[%s])) / "
                       "clamp_min(sum by (tailscale_traffic_type, field_class) "
                       "(rate(tailscale_network_field_observations_total[%s])), 0.000000001)"
                       % (RI, RI),
                       legend="{{tailscale_traffic_type}} / {{field_class}}")],
               unit="percentunit", min_=0, max_=1, custom=ts_custom(), options=ts_opts(),
               desc="Observed missing-field fraction, not inferred Destination Logging state. "
                    "Privacy-driven destination/port omissions can be expected."), 12, 7),
    ]
    # Exit-node IO — uses tailscale_exit_node label (node identity); gate with pii_node.
    exitio = [
        (panel("Exit-node throughput", "timeseries",
               [prom_t("sum by (tailscale_exit_node, network_io_direction) (rate(tailscale_exit_node_io_bytes_total[%s]))" % RI,
                       legend="{{tailscale_exit_node}} {{network_io_direction}}")],
               unit="Bps", custom=ts_custom(stack="normal"), options=ts_opts()), 12, 8),
        (panel("Exit-node packets/s", "timeseries",
               [prom_t("sum by (tailscale_exit_node, network_io_direction) (rate(tailscale_exit_node_packets_total[%s]))" % RI,
                       legend="{{tailscale_exit_node}} {{network_io_direction}}")],
               unit="pps", options=ts_opts()), 12, 8),
    ]
    # Flow-log cross-signal bandwidth (Loki metric queries) — aggregate, gate pii_topology for safety.
    fl_bw = [
        (panel("Observed tailnet bandwidth (flow logs)", "timeseries",
               [loki_t("sum(rate({service_name=\"tailscale2otel\"} | event_name=`tailscale.network.flow` | unwrap tailscale_tx_bytes [%s]))" % RI,
                        refid="A", legend="tx"),
                loki_t("sum(rate({service_name=\"tailscale2otel\"} | event_name=`tailscale.network.flow` | unwrap tailscale_rx_bytes [%s]))" % RI,
                        refid="B", legend="rx")],
               unit="Bps", novalue="0", options=ts_opts()), 24, 8),
    ]
    # Top node-pair talkers from flow logs — node identity; gate pii_node.
    fl_pairs = [
        (panel("Top node-pair talkers (flow logs)", "table",
               [loki_t("topk($topn, sum by (tailscale_src_node, tailscale_dst_node) (rate({service_name=\"tailscale2otel\"} | event_name=`tailscale.network.flow` | unwrap tailscale_tx_bytes [%s])))" % RI,
                        refid="A", instant=True)],
               unit="Bps",
               transformations=[organize(exclude=["Time"],
                                         rename={"tailscale_src_node": "Source",
                                                 "tailscale_dst_node": "Destination",
                                                 "Value": "tx bytes/s"})]), 24, 8),
    ]
    # Rollup aggregate panels — no identity labels; no PII gate.
    rollup_agg = [
        (panel("Throughput by direction", "timeseries",
               [prom_t("sum by (network_io_direction) (rate(tailscale_network_io_rollup_bytes_total%s[%s]))" % (tf, RI), legend="{{network_io_direction}}")],
               unit="Bps", custom=ts_custom(stack="normal", fill=25), options=ts_opts()), 8, 7),
        (panel("Throughput by transport", "timeseries",
               [prom_t("sum by (network_transport) (rate(tailscale_network_io_rollup_bytes_total%s[%s]))" % (tf, RI), legend="{{network_transport}}")],
               unit="Bps", custom=ts_custom(stack="normal", fill=25), options=ts_opts()), 8, 7),
        (panel("Throughput by traffic type", "timeseries",
               [prom_t("sum by (tailscale_traffic_type) (rate(tailscale_network_io_rollup_bytes_total%s[%s]))" % (tf, RI), legend="{{tailscale_traffic_type}}")],
               unit="Bps", custom=ts_custom(stack="normal", fill=25), options=ts_opts()), 8, 7),
        (panel("__other__ rollup share", "stat",
               [prom_t("(sum(rate(tailscale_network_io_rollup_bytes_total{tailscale_dst_node=\"__other__\"}[%s])) or vector(0)) / "
                       "clamp_min(sum(rate(tailscale_network_io_rollup_bytes_total[%s])), 1)" % (RI, RI), instant=True)],
               unit="percentunit", thresholds=thr([(None, "green"), (0.5, "yellow"), (0.8, "red")]),
               options=stat_opts(color="background"), desc="Fraction of rollup bytes folded into the bounded __other__ bucket."), 8, 6),
    ]
    # Rollup top-talker barcharts — tailscale_src_node/dst_node/dst_service = node identity; gate pii_node.
    rollup_talkers = [
        (panel("Top $topn source nodes", "barchart",
               [prom_t("topk($topn, sum by (tailscale_src_node) (rate(tailscale_network_io_rollup_bytes_total%s[%s])))" % (tf, RI), legend="{{tailscale_src_node}}", instant=True, fmt="table")],
               unit="Bps", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 8, 8),
        (panel("Top $topn destination nodes", "barchart",
               [prom_t("topk($topn, sum by (tailscale_dst_node) (rate(tailscale_network_io_rollup_bytes_total%s[%s])))" % (tf, RI), legend="{{tailscale_dst_node}}", instant=True, fmt="table")],
               unit="Bps", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 8, 8),
        (panel("Top destination services", "barchart",
               [prom_t("topk($topn, sum by (tailscale_dst_service) (rate(tailscale_network_io_rollup_bytes_total%s[%s])))" % (tsf, RI), legend="{{tailscale_dst_service}}", instant=True, fmt="table")],
               unit="Bps", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 8, 8),
    ]
    # Rollup topology tables — tailscale_src_node + port/peer topology; gate pii_topology.
    rollup_topo = [
        (panel("Unique dst peers per src", "table",
               [prom_t(lot("tailscale_network_unique_dst_peers"), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"tailscale_src_node": "Source node", "Value": "Unique peers"})],
               desc="Distinct destination peers per source node (last flush)."), 12, 6),
        (panel("Unique dst ports per src", "table",
               [prom_t(lot("tailscale_network_unique_dst_ports"), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"tailscale_src_node": "Source node", "Value": "Unique ports"})],
               desc="Distinct destination ports per source node (last flush)."), 12, 6),
    ]
    # Raw aggregate panels — no identity labels; no PII gate.
    raw_agg = [
        (panel("Throughput by direction (raw)", "timeseries",
               [prom_t("sum by (network_io_direction) (rate(tailscale_network_io_bytes_total%s[%s]))" % (tf, RI), legend="{{network_io_direction}}")],
               unit="Bps", custom=ts_custom(stack="normal", fill=25), options=ts_opts()), 8, 7),
        (panel("Packets by direction (raw)", "timeseries",
               [prom_t("sum by (network_io_direction) (rate(tailscale_network_packets_total%s[%s]))" % (tf, RI), legend="{{network_io_direction}}")],
               unit="pps", custom=ts_custom(stack="normal"), options=ts_opts()), 8, 7),
        (panel("Throughput by transport (raw)", "timeseries",
               [prom_t("sum by (network_transport) (rate(tailscale_network_io_bytes_total%s[%s]))" % (tf, RI), legend="{{network_transport}}")],
               unit="Bps", custom=ts_custom(stack="normal", fill=25), options=ts_opts()), 8, 7),
    ]
    # Raw top-talker barcharts — node identity; gate pii_node.
    raw_talkers = [
        (panel("Top $topn source nodes (raw)", "barchart",
               [prom_t("topk($topn, sum by (tailscale_src_node) (rate(tailscale_network_io_bytes_total%s[%s])))" % (tf, RI), legend="{{tailscale_src_node}}", instant=True, fmt="table")],
               unit="Bps", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 8, 8),
        (panel("Top $topn destination nodes (raw)", "barchart",
               [prom_t("topk($topn, sum by (tailscale_dst_node) (rate(tailscale_network_io_bytes_total%s[%s])))" % (tf, RI), legend="{{tailscale_dst_node}}", instant=True, fmt="table")],
               unit="Bps", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 8, 8),
        (panel("Top $topn destination services (raw)", "barchart",
               [prom_t("topk($topn, sum by (tailscale_dst_service) (rate(tailscale_network_io_bytes_total%s[%s])))" % (tsf, RI), legend="{{tailscale_dst_service}}", instant=True, fmt="table")],
               unit="Bps", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 8, 8),
    ]
    return [
        row("Flow summary", summary, present="has_flows"),
        row("Flow integrity", integrity, present="has_flows"),
        row("Exit-node I/O", exitio, present="has_exit_io", hide_when=["pii_node"]),
        row("Observed tailnet bandwidth (flow logs)", fl_bw, present="has_flows", hide_when=["pii_topology"]),
        row("Throughput & talkers — ROLLUP (bounded top-N)", rollup_agg, present="has_rollup_flow"),
        row("Top talkers — ROLLUP", rollup_talkers, present="has_rollup_flow", hide_when=["pii_node"]),
        row("Peer & port topology — ROLLUP", rollup_topo, present="has_rollup_flow", hide_when=["pii_topology"]),
        row("Throughput & talkers — RAW (full detail)", raw_agg, present="has_raw_flow"),
        row("Top talkers — RAW", raw_talkers, present="has_raw_flow", hide_when=["pii_node"]),
        row("Top node-pair talkers (flow logs)", fl_pairs, present="has_flows", hide_when=["pii_node"]),
    ]
