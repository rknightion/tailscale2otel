"""tab_network() — moved out of build.py in the module split.

#391 re-scoped: the four hand-maintained classic-schema dashboards (the legacy
`tailscale-network.json` among them) were deleted in #394, so "modernize the legacy
network dashboard" is delivered here, on the flagship's Network & Flows tab.
"""

from builder import (BAR_NOISE, bargauge_opts, category_bar_opts, barchart_opts, logs_opts, loki_t, lot, organize,
                     panel, PII, pii_sentinel, prom_t, RI, row, sentinel, stat_opts, thr, ts_custom,
                     ts_opts)


# --- rollup vs raw: pick ONE path, never add them (#391) ---------------------
#
# The rollup family is the bounded top-N *fold of the same traffic* the raw family
# reports — under `cardinality.flow.metrics_mode: both` a tailnet emits both, and any
# expression that adds them double-counts every byte. rollup_first() is the only place
# in this module where a raw and a rollup metric name may appear in one expression,
# and it cannot emit anything but the fallback: one `shape` template is rendered twice,
# once per metric name, and the two renderings are joined with PromQL `or`, which
# returns the left operand whole and evaluates the right only when the left has no
# series. There is no code path through it that produces a `+`, so the double count is
# structurally unreachable rather than merely avoided by review. Every other panel
# names exactly one of the two families and lives in a row gated on that family's
# presence sentinel. test_network_diagnostics.py re-checks both properties against the
# built document.

ROLLUP_BYTES = "tailscale_network_io_rollup_bytes_total"
RAW_BYTES = "tailscale_network_io_bytes_total"
ROLLUP_PACKETS = "tailscale_network_packets_rollup_total"
RAW_PACKETS = "tailscale_network_packets_total"

# Prerequisite sentences for the two families. Presence cannot tell "disabled" from
# "unsupported" from "never deployed", so these name the config key and stop — they
# never assert why a row is empty (#385).
ROLLUP_PREREQ = ("Bounded top-N rollup series, emitted when `cardinality.flow.metrics_mode` "
                 "is `rollup` (the default) or `both`.")
RAW_PREREQ = ("Unbounded per-flow series, emitted only when `cardinality.flow.metrics_mode` "
              "is `all` or `both`; the row is hidden when the family has no series. Read it "
              "instead of the ROLLUP row, never added to it — the rollup is a fold of this "
              "same traffic.")


def rollup_first(shape, rollup, raw):
    """Rollup-first query with a raw fallback: `<shape/rollup> or <shape/raw>`.

    `shape` is a query template carrying exactly one `{m}` placeholder for the metric
    name (pre-format any other `%s` before calling). Both operands are rendered from
    that single template, so they cannot diverge, and `or` is the only operator this
    function can put between them.
    """
    if shape.count("{m}") != 1:
        raise ValueError("rollup_first shape needs exactly one {m} placeholder: %r" % shape)
    return "%s or %s" % (shape.replace("{m}", rollup), shape.replace("{m}", raw))


def talker_bar(title, metric, label, display, filt, desc):
    """Top-N barchart over an instant query, with a named device category axis.

    `display` is both the renamed label column and the pinned xField — field config
    and viz options are applied AFTER transformations, so xField must be the renamed
    name, not the raw Prometheus label.
    """
    return panel(title, "barchart",
                 [prom_t("topk($topn, sum by (%s) (rate(%s%s[%s])))" % (label, metric, filt, RI),
                         instant=True, fmt="table")],
                 unit="Bps", options=category_bar_opts(display),
                 transformations=[organize(exclude=BAR_NOISE,
                                           rename={label: display, "Value": "Bytes/s"})],
                 desc=desc)


def tab_network(scope):
    # Presence sentinels this tab declares (moved from variables.py, #495).
    # #526 wave 3: every sentinel here gates a ROW inside this one tab, so each is
    # declared at TAB scope rather than on the dashboard. The Events & Logs tab, which
    # used to be the other consumer of has_flows, no longer exists — its flow-log stream
    # moved onto this tab (decision 9), so the name is genuinely single-tab now.
    sentinel("has_flows", "tailscale_network_flows_total", scope)
    sentinel("has_raw_flow", RAW_BYTES, scope)
    sentinel("has_rollup_flow", ROLLUP_BYTES, scope)
    # has_unique was dead until #391: the peer/port topology row queries exactly the two
    # unique_* gauges, which are gated by cardinality.flow.node_dims on top of the rollup
    # mode, so has_rollup_flow was the looser of the two gates and left the row rendering
    # empty whenever node_dims was off.
    sentinel("has_unique", "tailscale_network_unique_dst_peers", scope)
    sentinel("has_exit_io", "tailscale_exit_node_io_bytes_total", scope)
    pii_sentinel("pii_node", PII + '{category="node_ids"} == 0', scope)
    pii_sentinel("pii_topology", PII + '{category="network_topology"} == 0', scope)
    # DELETED in #526: pii_int_ips / pii_ext_ips / pii_ts_ips. They were declared and
    # shipped in the artifact while gating nothing, costing a Prometheus query on every
    # dashboard load and reading, to anyone scanning the variable list, like a working
    # feature gate. The reason they gated nothing still holds — the three IP categories
    # redact source.address/destination.address, which appear only on the
    # tailscale.network.flow LOG record, and the flow-log panels here are Loki *metric*
    # queries over node labels — so re-adding one is only correct alongside a panel that
    # actually renders an address. Declare it in the same commit as that panel, never
    # ahead of it.

    # Scope selectors (#391). tailnet/provider are real per-series metric labels stamped
    # on every signal (telemetry.constLabelAttrs), so they filter with a plain matcher and
    # no target_info join. Under "All" both expand to `.*`, which also matches a series
    # carrying no such label at all — so a single-tailnet deployment is unaffected.
    scope_sel = 'tailscale_tailnet=~"$tailnet", tailscale2otel_provider=~"$provider"'
    sf = "{%s}" % scope_sel
    loki_flow = ('{service_name="tailscale2otel"} | tailscale_tailnet=~"$tailnet" '
                 '| tailscale2otel_provider=~"$provider" '
                 '| event_name=`tailscale.network.flow`')
    tf = ('{%s, network_transport=~"$net_transport", tailscale_traffic_type=~"$traffic_type"}'
          % scope_sel)
    # tf, but also exclude unclassified (empty-label) services so the top-services
    # barcharts name every bar instead of falling back to "Value" for the empty group.
    tsf = tf[:-1] + ", tailscale_dst_service!=\"\"}"
    summary = [
        (panel("Throughput (now)", "stat",
               [prom_t(rollup_first("sum(rate({m}%s[%s]))" % (tf, RI), ROLLUP_BYTES, RAW_BYTES),
                       instant=True)],
               unit="Bps", options=stat_opts(graph="area", color="value"),
               desc="Rollup-first: the bounded rollup family is read when present and the raw "
                    "family only when it is not, so a tailnet running "
                    "`cardinality.flow.metrics_mode: both` reports each byte once."), 4, 5),
        (panel("Packets/s (now)", "stat",
               [prom_t(rollup_first("sum(rate({m}%s[%s]))" % (tf, RI), ROLLUP_PACKETS, RAW_PACKETS),
                       instant=True)],
               unit="pps", options=stat_opts(graph="area"),
               desc="Rollup-first, same fallback as Throughput (now): rollup when present, raw "
                    "otherwise, never the sum of the two."), 4, 5),
        (panel("Flows/s (now)", "stat", [prom_t("sum(rate(tailscale_network_flows_total%s[%s]))" % (sf, RI), instant=True)],
               unit="cps", options=stat_opts(graph="area"),
               desc="Distinct flow records observed per second, fleet-wide."), 4, 5),
        (panel("Flows/s by transport", "timeseries",
               [prom_t("sum by (network_transport) (rate(tailscale_network_flows_total%s[%s]))" % (tf, RI), legend="{{network_transport}}")],
               unit="cps", custom=ts_custom(stack="normal"), options=ts_opts(),
               desc="Flow rate split by transport protocol (tcp/udp/other)."), 6, 5),
        (panel("Flows/s by traffic type", "timeseries",
               [prom_t("sum by (tailscale_traffic_type) (rate(tailscale_network_flows_total%s[%s]))" % (tf, RI), legend="{{tailscale_traffic_type}}")],
               unit="cps", custom=ts_custom(stack="normal"), options=ts_opts(),
               desc="Flow rate split by traffic type (virtual/subnet/exit/physical)."), 6, 5),
    ]
    integrity = [
        (panel("Reporter trust & consistency", "timeseries",
               [prom_t("sum by (trust, consistency) "
                       "(rate(tailscale_network_reporter_observations_total%s[%s]))" % (sf, RI),
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
    # Ingestion hygiene (#391) — the three counters that say a flow record never reached
    # the metrics above. All three are bounded-label aggregates carrying no identity, so
    # no PII gate; they belong next to the integrity row rather than in exporter
    # diagnostics because a non-zero rate changes how the throughput panels read.
    hygiene = [
        (panel("Rejected flow records/s", "timeseries",
               [prom_t("sum by (source, reason) (rate(tailscale_network_data_quality_total%s[%s])) "
                       "or (0 * sum(rate(tailscale_network_flows_total%s[%s])))"
                       % (sf, RI, sf, RI), legend="{{source}} / {{reason}}")],
               unit="cps", custom=ts_custom(stack="normal"), options=ts_opts(),
               thresholds=thr([(None, "green"), (0.01, "yellow")]),
               desc="Semantically invalid flow records rejected before any processor side "
                    "effect, by ingestion source and validation reason. These records are "
                    "absent from every other panel on this tab, so a sustained rate means the "
                    "throughput and talker numbers are reading a subset of the feed."), 8, 7),
        (panel("Dedup counter conflicts/s", "timeseries",
               [prom_t("sum by (scope, tailscale_traffic_type) "
                       "(rate(tailscale_network_dedup_conflicts_total%s[%s])) "
                       "or (0 * sum(rate(tailscale_network_flows_total%s[%s])))"
                       % (sf, RI, sf, RI),
                       legend="{{scope}} / {{tailscale_traffic_type}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Duplicate flow connections whose byte/packet counters disagree with the "
                    "first observation of the same connection. The first observation stays "
                    "authoritative, so the conflicting counters are discarded rather than "
                    "added. Expected while both a poller and a receiver feed the same log "
                    "type (`collectors.flowlogs.source`); otherwise it is upstream "
                    "re-delivery."), 8, 7),
        (panel("Flow-view store drops/s", "timeseries",
               [prom_t("sum by (reason) (rate(tailscale_network_store_dropped_total%s[%s])) "
                       "or (0 * sum(rate(tailscale_network_flows_total%s[%s])))"
                       % (sf, RI, sf, RI), legend="{{reason}}")],
               unit="cps", custom=ts_custom(stack="normal"), options=ts_opts(),
               desc="Flow observations rejected from the local in-memory flow view because "
                    "their timestamps fall outside its retention or future-skew bounds "
                    "(`flow_view.*`). OTLP emission is unaffected — these records still reach "
                    "the metrics and logs on this tab; only the admin flow-view page omits "
                    "them."), 8, 7),
    ]
    # Exit-node IO — uses tailscale_exit_node label (node identity); gate with pii_node.
    exitio = [
        (panel("Exit-node throughput", "timeseries",
               [prom_t("sum by (tailscale_exit_node, network_io_direction) (rate(tailscale_exit_node_io_bytes_total%s[%s]))" % (sf, RI),
                       legend="{{tailscale_exit_node}} {{network_io_direction}}")],
               unit="Bps", custom=ts_custom(stack="normal"), options=ts_opts(),
               desc="Bytes relayed through each exit node. Attributed by the reporting node of "
                    "`traffic_type=exit` flow records and gated by "
                    "`cardinality.flow.exit_node_attribution` (default on) — independent of the "
                    "rollup/raw metric mode."), 12, 8),
        (panel("Exit-node packets/s", "timeseries",
               [prom_t("sum by (tailscale_exit_node, network_io_direction) (rate(tailscale_exit_node_packets_total%s[%s]))" % (sf, RI),
                       legend="{{tailscale_exit_node}} {{network_io_direction}}")],
               unit="pps", options=ts_opts(),
               desc="Packets relayed through each exit node, same attribution and gating as "
                    "Exit-node throughput."), 12, 8),
    ]
    # Flow-log cross-signal byte totals (Loki metric queries) — aggregate, gate
    # pii_topology for safety. Use the selected range rather than $__rate_interval:
    # flow records arrive in batches wider than a graph step, so a short moving rate can
    # be empty while the raw stream underneath is visibly populated.
    fl_bw = [
        (panel("Observed bytes (flow logs)", "bargauge",
               [loki_t("sum(sum_over_time(%s | unwrap tailscale_tx_bytes [$__range]))"
                       % loki_flow, refid="A", legend="tx", instant=True),
                loki_t("sum(sum_over_time(%s | unwrap tailscale_rx_bytes [$__range]))"
                       % loki_flow, refid="B", legend="rx", instant=True)],
               unit="bytes", options=bargauge_opts(),
               novalue="No matching flow-log records in the selected range.",
               desc="Bytes summed directly from raw flow-log records over the selected range, "
                    "independent of the Prometheus rollup/raw metric mode. Empty means no "
                    "matching log records; a populated zero is shown only when records carry "
                    "zero bytes."), 24, 8),
    ]
    # Top node-pair talkers from flow logs — node identity; gate pii_node.
    fl_pairs = [
        (panel("Top node-pair talkers (flow logs)", "table",
               [loki_t("topk($topn, sum by (tailscale_src_node, tailscale_dst_node) "
                       "(sum_over_time(%s | unwrap tailscale_tx_bytes [$__range])))" % loki_flow,
                        refid="A", instant=True)],
               unit="bytes",
               transformations=[organize(exclude=["Time"],
                                         rename={"tailscale_src_node": "Source",
                                                 "tailscale_dst_node": "Destination",
                                                 "Value": "TX bytes"})],
               novalue="No flow-log records with both source and destination node identity in "
                       "the selected range.",
               desc="Top-N source/destination node pairs by total outbound bytes in the selected "
                    "range, from raw flow-log records."), 24, 8),
    ]
    # Rollup aggregate panels — no identity labels; no PII gate.
    rollup_agg = [
        (panel("Throughput by direction", "timeseries",
               [prom_t("sum by (network_io_direction) (rate(%s%s[%s]))" % (ROLLUP_BYTES, tf, RI), legend="{{network_io_direction}}")],
               unit="Bps", custom=ts_custom(stack="normal", fill=25), options=ts_opts(),
               desc=ROLLUP_PREREQ), 8, 7),
        (panel("Throughput by transport", "timeseries",
               [prom_t("sum by (network_transport) (rate(%s%s[%s]))" % (ROLLUP_BYTES, tf, RI), legend="{{network_transport}}")],
               unit="Bps", custom=ts_custom(stack="normal", fill=25), options=ts_opts(),
               desc=ROLLUP_PREREQ), 8, 7),
        (panel("Throughput by traffic type", "timeseries",
               [prom_t("sum by (tailscale_traffic_type) (rate(%s%s[%s]))" % (ROLLUP_BYTES, tf, RI), legend="{{tailscale_traffic_type}}")],
               unit="Bps", custom=ts_custom(stack="normal", fill=25), options=ts_opts(),
               desc=ROLLUP_PREREQ), 8, 7),
        (panel("__other__ rollup share", "stat",
               [prom_t("(sum(rate(%s{%s, tailscale_dst_node=\"__other__\"}[%s])) or vector(0)) / "
                       "clamp_min(sum(rate(%s%s[%s])), 1)" % (ROLLUP_BYTES, scope_sel, RI, ROLLUP_BYTES, sf, RI), instant=True)],
               unit="percentunit", thresholds=thr([(None, "green"), (0.5, "yellow"), (0.8, "red")]),
               options=stat_opts(color="background"),
               desc="Fraction of rollup bytes folded into the bounded __other__ bucket. Raise "
                    "`cardinality.flow.rollup_top_n` to name more of the traffic."), 8, 6),
    ]
    # Route/path/service context (#391) — the rollup family carries tailscale_path and the
    # numeric tailscale_derp_region_id on PHYSICAL traffic only, so this is real routing
    # context without duplicating the Node Metrics tab (which owns the tailscaled-side path
    # and peer-relay detail and is gated on has_nodemetrics). Neither label is identity.
    rollup_path = [
        (panel("Throughput by path — ROLLUP", "timeseries",
               [prom_t("sum by (tailscale_path) (rate(%s{%s, tailscale_path!=\"\"}[%s]))"
                       % (ROLLUP_BYTES, scope_sel, RI), legend="{{tailscale_path}}")],
               unit="Bps", custom=ts_custom(stack="normal", fill=25), options=ts_opts(),
               desc="Rollup bytes split by how the tailnet carried them: `direct` or `derp`. "
                    "Carried on physical traffic only — the overlay traffic types describe what "
                    "was carried, not how, so they hold no path at all and are excluded here "
                    "rather than counted as direct. " + ROLLUP_PREREQ), 12, 7),
        (panel("Throughput by DERP region ID — ROLLUP", "timeseries",
               [prom_t("sum by (tailscale_derp_region_id) (rate(%s{%s, tailscale_derp_region_id!=\"\"}[%s]))"
                       % (ROLLUP_BYTES, scope_sel, RI), legend="region {{tailscale_derp_region_id}}")],
               unit="Bps", custom=ts_custom(stack="normal", fill=25), options=ts_opts(),
               desc="Relayed rollup bytes by the numeric DERP region ID the flow record "
                    "supplied. NOT joinable with the region NAMES on the device-latency "
                    "metrics (`tailscale_derp_region`): the API exposes no DERP map to "
                    "translate between an ID and a name."), 12, 7),
        (panel("DERP-relayed share of rollup bytes", "stat",
               [prom_t("sum(rate(%s{%s, tailscale_path=\"derp\"}[%s])) / "
                       "clamp_min(sum(rate(%s{%s, tailscale_path!=\"\"}[%s])), 1)"
                       % (ROLLUP_BYTES, scope_sel, RI, ROLLUP_BYTES, scope_sel, RI), instant=True)],
               unit="percentunit", thresholds=thr([(None, "green"), (0.3, "yellow"), (0.6, "red")]),
               options=stat_opts(color="background"),
               desc="Share of physical rollup bytes that took a DERP relay rather than a direct "
                    "path. The flow-side counterpart of Fleet DERP share (now) on the Node "
                    "Metrics tab, which measures the same thing from tailscaled."), 8, 6),
    ]
    # Rollup top-talker barcharts — tailscale_src_node/dst_node/dst_service = node identity; gate pii_node.
    rollup_talkers = [
        (talker_bar("Top $topn source nodes", ROLLUP_BYTES, "tailscale_src_node", "Source node", tf,
                    "Busiest source nodes by rollup byte rate. `__other__` is the bounded "
                    "remainder bucket, not a node. " + ROLLUP_PREREQ), 8, 8),
        (talker_bar("Top $topn destination nodes", ROLLUP_BYTES, "tailscale_dst_node", "Destination node", tf,
                    "Busiest destination nodes by rollup byte rate. `__other__` is the bounded "
                    "remainder bucket, not a node. " + ROLLUP_PREREQ), 8, 8),
        (talker_bar("Top destination services", ROLLUP_BYTES, "tailscale_dst_service", "Destination service", tsf,
                    "Busiest destination services by rollup byte rate; unclassified "
                    "(empty-service) traffic is excluded so every bar is named. "
                    + ROLLUP_PREREQ), 8, 8),
    ]
    # Rollup topology tables — tailscale_src_node + port/peer topology; gate pii_topology.
    rollup_topo = [
        (panel("Unique dst peers per src", "table",
               [prom_t(lot("tailscale_network_unique_dst_peers%s" % sf), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"tailscale_src_node": "Source node", "Value": "Unique peers"})],
               desc="Distinct destination peers per source node (last flush). Needs "
                    "`cardinality.flow.metrics_mode` `rollup` or `both` AND "
                    "`cardinality.flow.node_dims`."), 12, 6),
        (panel("Unique dst ports per src", "table",
               [prom_t(lot("tailscale_network_unique_dst_ports%s" % sf), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"tailscale_src_node": "Source node", "Value": "Unique ports"})],
               desc="Distinct destination ports per source node (last flush) — port-level "
                    "visibility without per-port series. Same prerequisites as the peers "
                    "table."), 12, 6),
    ]
    # Raw aggregate panels — no identity labels; no PII gate.
    raw_agg = [
        (panel("Throughput by direction (raw)", "timeseries",
               [prom_t("sum by (network_io_direction) (rate(%s%s[%s]))" % (RAW_BYTES, tf, RI), legend="{{network_io_direction}}")],
               unit="Bps", custom=ts_custom(stack="normal", fill=25), options=ts_opts(),
               desc=RAW_PREREQ), 8, 7),
        (panel("Packets by direction (raw)", "timeseries",
               [prom_t("sum by (network_io_direction) (rate(%s%s[%s]))" % (RAW_PACKETS, tf, RI), legend="{{network_io_direction}}")],
               unit="pps", custom=ts_custom(stack="normal"), options=ts_opts(),
               desc=RAW_PREREQ), 8, 7),
        (panel("Throughput by transport (raw)", "timeseries",
               [prom_t("sum by (network_transport) (rate(%s%s[%s]))" % (RAW_BYTES, tf, RI), legend="{{network_transport}}")],
               unit="Bps", custom=ts_custom(stack="normal", fill=25), options=ts_opts(),
               desc=RAW_PREREQ), 8, 7),
    ]
    # Raw top-talker barcharts — node identity; gate pii_node.
    raw_talkers = [
        (talker_bar("Top $topn source nodes (raw)", RAW_BYTES, "tailscale_src_node", "Source node", tf,
                    "Busiest source nodes by raw per-flow byte rate — unbounded, so it names "
                    "every node rather than folding a remainder. " + RAW_PREREQ), 8, 8),
        (talker_bar("Top $topn destination nodes (raw)", RAW_BYTES, "tailscale_dst_node", "Destination node", tf,
                    "Busiest destination nodes by raw per-flow byte rate. " + RAW_PREREQ), 8, 8),
        (talker_bar("Top $topn destination services (raw)", RAW_BYTES, "tailscale_dst_service", "Destination service", tsf,
                    "Busiest destination services by raw per-flow byte rate. " + RAW_PREREQ), 8, 8),
    ]
    # #526 decision 9: the Events & Logs tab is dissolved and its per-signal log streams
    # go to the tab that owns the signal. This one is the raw flow-log line, which is the
    # detail behind every rollup panel above — a reader who wants to know WHICH connection
    # drove a spike ends up here, so it sits last, after the aggregates that sent them.
    flowlogs = [
        (panel("Flow log stream", "logs",
               [loki_t("{service_name=\"tailscale2otel\"} | event_name=`tailscale.network.flow` "
                       "|~ `$log_filter`", maxlines=300)],
               options=logs_opts(),
               desc="Raw flow-log lines as received, newest first; narrow them with the Log "
                    "filter control. This is the unaggregated record behind the rollup and raw "
                    "panels above."), 24, 10),
    ]
    return [
        row("Flow summary", summary, present="has_flows"),
        row("Flow integrity", integrity, present="has_flows"),
        row("Flow ingestion hygiene", hygiene, present="has_flows"),
        row("Exit-node I/O", exitio, present="has_exit_io", hide_when=["pii_node"]),
        row("Observed bytes (flow logs)", fl_bw, present="has_flows", hide_when=["pii_topology"]),
        row("Throughput & talkers — ROLLUP (bounded top-N)", rollup_agg, present="has_rollup_flow"),
        row("Path & DERP context — ROLLUP", rollup_path, present="has_rollup_flow"),
        row("Top talkers — ROLLUP", rollup_talkers, present="has_rollup_flow", hide_when=["pii_node"]),
        # has_unique, not has_rollup_flow: both panels here need cardinality.flow.node_dims
        # on top of the rollup mode, and the unique_* gauges are the signal for that.
        # Collapsed: topology is a second-stage drill-down after rollup traffic.
        row("Peer & port topology — ROLLUP", rollup_topo, present="has_unique", hide_when=["pii_topology"], collapse=True),
        # Collapsed: raw views are intentionally expensive, full-detail investigations.
        row("Throughput & talkers — RAW (full detail)", raw_agg, present="has_raw_flow", collapse=True),
        row("Top talkers — RAW", raw_talkers, present="has_raw_flow", hide_when=["pii_node"], collapse=True),
        row("Top node-pair talkers (flow logs)", fl_pairs, present="has_flows", hide_when=["pii_node"], collapse=True),
        row("Flow log stream", flowlogs, present="has_flows", collapse=True),
    ]
