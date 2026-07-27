"""tab_cardinality() — moved out of build.py in the module split."""

from builder import (barchart_opts, bargauge_opts, organize, panel, prom_t, RI, row,
                     stat_opts, thr, ts_custom, ts_opts, WIN_FAST)


def tab_cardinality():
    OVF = "{otel_metric_overflow=\"true\", __name__=~\"tailscale.*\"}"
    overflow = [
        (panel("Metrics over cardinality cap", "stat",
               [prom_t("count(count by (__name__) (%s)) or vector(0)" % OVF, instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "red")]),
               options=stat_opts(color="background"),
               desc="Metric families that exceeded the per-metric series cap (cardinality.metric_limit, "
                    "default 10000) and are now collapsing excess series into one otel_metric_overflow "
                    "series — SILENT per-series detail loss. >0 means raise metric_limit or lower flow "
                    "cardinality (ephemeral source_port is the biggest driver)."), 6, 5),
        (panel("Busiest metric — % of cap", "stat",
               [prom_t("max(tailscale2otel_series_active) / 10000", instant=True)],
               unit="percentunit", min_=0, max_=1, thresholds=thr([(None, "green"), (0.8, "yellow"), (1, "red")]),
               options=stat_opts(color="background"),
               desc="Highest per-metric active-series count as a fraction of the 10k cap."), 6, 5),
        (panel("Total active series", "stat",
               [prom_t("sum(tailscale2otel_series_active)", instant=True)],
               unit="short", options=stat_opts(graph="area", color="value"),
               desc="Sum of active series across all tailscale2otel metrics (a proxy for ingest cost)."), 6, 5),
        (panel("Metric families tracked", "stat",
               [prom_t("count(tailscale2otel_series_active)", instant=True)],
               unit="short", options=stat_opts()), 6, 5),
        (panel("Overflowing families", "table",
               [prom_t("count by (__name__) (%s)" % OVF, instant=True, fmt="table")],
               novalue="No metrics over cap — all series fully resolved.",
               transformations=[organize(exclude=["Time", "Value", "job", "instance",
                                                   "service_instance_id", "service_name", "service_namespace"],
                                          rename={"__name__": "Metric"})],
               desc="Metric families currently over the per-metric cap (otel_metric_overflow=true)."), 24, 6),
    ]
    budget = [
        (panel("Active series vs 10k cap (top $topn)", "bargauge",
               [prom_t("topk($topn, max by (metric_name) (tailscale2otel_series_active))", legend="{{metric_name}}")],
               unit="short", max_=10000, thresholds=thr([(None, "green"), (8000, "yellow"), (10000, "red")]),
               options=bargauge_opts(),
               desc="Per-metric active series against the cap. Watch the flow families."), 12, 8),
        (panel("Active series over time (top $topn)", "timeseries",
               [prom_t("topk($topn, max by (metric_name) (tailscale2otel_series_active))", legend="{{metric_name}}")],
               unit="short", custom=ts_custom(), options=ts_opts(placement="right")), 12, 8),
    ]
    flow = [
        (panel("Flow series: raw vs bounded rollup", "timeseries",
               [prom_t("max(tailscale2otel_series_active{metric_name=\"tailscale.network.io\"})", legend="io raw"),
                prom_t("max(tailscale2otel_series_active{metric_name=\"tailscale.network.io.rollup\"})", legend="io rollup"),
                prom_t("max(tailscale2otel_series_active{metric_name=\"tailscale.network.packets\"})", legend="packets raw"),
                prom_t("max(tailscale2otel_series_active{metric_name=\"tailscale.network.packets.rollup\"})", legend="packets rollup")],
               unit="short", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Raw flow families saturate the 10k cap; the bounded rollup stays small. When raw is "
                    "at cap, trust the ROLLUP talker panels on the Network tab."), 12, 7),
        (panel("__other__ rollup share", "stat",
               [prom_t("(sum(rate(tailscale_network_io_rollup_bytes_total{tailscale_dst_node=\"__other__\"}[%s])) or vector(0)) / "
                       "clamp_min(sum(rate(tailscale_network_io_rollup_bytes_total[%s])), 1)" % (RI, RI), instant=True)],
               unit="percentunit", thresholds=thr([(None, "green"), (0.5, "yellow"), (0.8, "red")]),
               options=stat_opts(color="background"),
               desc="Fraction of rollup bytes folded into the bounded __other__ bucket. High = many small talkers."), 6, 7),
        (panel("Flow log records dropped/s", "timeseries",
               [prom_t("sum(rate(tailscale_network_flow_logs_dropped_total[%s])) or vector(0)" % RI, legend="dropped/s")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Flow LOG records suppressed by the per-window volume guard "
                    "(collectors.flowlogs.max_log_records_per_window). Metrics are never dropped, only logs."), 6, 7),
    ]
    dedup = [
        (panel("Dedup set size", "timeseries", [prom_t("max by (dedup_set) (tailscale2otel_dedup_size_ratio)", legend="{{dedup_set}}")],
               unit="short", custom=ts_custom(), options=ts_opts(),
               desc="Keys held in each cross-source de-duplication set (a count)."), 12, 6),
        (panel("Dedup evictions/s", "timeseries",
               [prom_t("sum by (dedup_set) (rate(tailscale2otel_dedup_evictions_total[%s]))" % RI, legend="{{dedup_set}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Steady-state evictions are normal: dedup keys are effectively unique, so a full set evicts one key per insert forever even when healthy. Only evictions approaching a set's capacity within one poll interval indicate real overlap loss (boundary double-counting)."), 12, 6),
    ]

    # C5: additional headroom panels added to the overflow row (Task 1.8 Step 1)
    overflow += [
        # No zero-fill: the gauge is emitted only when a positive limit is configured, and
        # 0 is a MEANINGFUL value here (unlimited) — so filling it in asserts the opposite
        # of what an absent series implies (#385).
        (panel("Series limit", "stat",
               [prom_t("max(tailscale2otel_series_limit)", instant=True)],
               unit="short", options=stat_opts(),
               novalue="No per-metric series cap reported — cardinality.metric_limit is unset "
                       "(unlimited), or exporter self-observability is off.",
               desc="Configured per-metric series limit (cardinality.metric_limit). 0 means unlimited."), 6, 5),
        (panel("Overflowing now", "stat",
               [prom_t("sum(tailscale2otel_series_overflowing_ratio) or vector(0)", instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "red")]),
               options=stat_opts(color="background"),
               desc="Number of metric families currently overflowing their series cap. >0 means detail loss."), 6, 5),
        (panel("Per-metric headroom (top-N)", "table",
               [prom_t("topk($topn, max by (metric_name) (tailscale2otel_series_active) / on() group_left() max(tailscale2otel_series_limit))",
                       instant=True, fmt="table")],
               transformations=[organize(
                   exclude=["Time", "job", "instance", "service_instance_id", "service_name", "service_namespace"],
                   rename={"metric_name": "Metric", "Value": "Headroom (frac)"})],
               desc="Per-metric active-series count divided by the series limit — headroom as a fraction. "
                    "1.0 = at cap. Uses / on() group_left() because the limit is a single unlabelled series."), 12, 5),
    ]

    # New row: active series by group + overflowing metrics table (Task 1.8 Step 2 + 1H.3)
    bygroup = [
        (panel("Active series by group", "barchart",
               [prom_t("sum by (metric_group) (last_over_time(tailscale2otel_series_by_group[%s]))" % WIN_FAST,
                       legend="{{metric_group}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Active series aggregated by metric_group — the primary cost-driver view. "
                    "18 groups live; each group maps to a logical collector domain."), 24, 8),
        (panel("Metrics overflowing now", "table",
               [prom_t("max by (metric_name) (last_over_time(tailscale2otel_series_overflowing_ratio[%s])) == 1" % WIN_FAST,
                       instant=True, fmt="table")],
               novalue="No metrics overflowing.",
               transformations=[organize(
                   exclude=["Time", "job", "instance", "service_instance_id", "service_name", "service_namespace", "Value"],
                   rename={"metric_name": "Metric"})],
               desc="Metric families where overflowing_ratio == 1 (capped). 147+ series tracked; "
                    "0 overflowing is the normal live state — that is correct."), 24, 6),
    ]

    # New row: ingest vs export cost (Task 1.8 Step 3 + 1H.3)
    cost = [
        (panel("Ingest vs export cost (per minute)", "timeseries",
               [prom_t("rate(tailscale2otel_export_datapoints_total[%s])*60" % RI, legend="DPM (datapoints/min)"),
                prom_t("rate(tailscale2otel_export_log_records_total[%s])*60" % RI, legend="LPM (log rec/min)"),
                prom_t("sum by (source, signal) (rate(tailscale2otel_ingest_records_total[%s]))" % RI,
                       legend="{{source}}/{{signal}} ingest rec/s")],
               unit="short", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Export datapoints/min and log records/min (ingest cost) alongside per-source ingest rate. "
                    "Rising DPM driven by a single source → check that group in 'Active series by group'."), 24, 8),
    ]

    return [
        row("Cardinality cap & overflow", overflow),
        row("Active series by group", bygroup),
        row("Series budget", budget),
        row("Ingest vs export cost", cost, present="has_selfobs"),
        row("Flow cardinality drivers", flow),
        row("Cross-source dedup", dedup),
    ]
