"""tab_diagnostics() — moved out of build.py in the module split."""

from builder import (bargauge_opts, hq, lot, organize, panel, PII, prom_t, RI, row,
                     stat_opts, TBL_NOISE, tempo_t, thr, ts_custom, ts_opts, vmap,
                     WIN_FAST, WIN_SLOW)
from maps import BOOL_MAP, UP_MAP
import builder


def tab_diagnostics():
    cf = "{tailscale_collector=~\"$collector\"}"
    live = [
        (panel("Exporter up", "stat", [prom_t("max(%s)" % lot("tailscale2otel_up_ratio"))],
               mappings=UP_MAP, thresholds=thr([(None, "red"), (1, "green")]), options=stat_opts(color="background")), 4, 5),
        (panel("Collectors OK", "stat", [prom_t("count(%s == 1) or vector(0)" % lot("tailscale2otel_scrape_success_ratio"))],
               unit="short", thresholds=thr([(None, "green")]), options=stat_opts(color="value")), 4, 5),
        (panel("Goroutines", "stat", [prom_t("max(%s)" % lot("tailscale2otel_runtime_goroutines_ratio"))],
               unit="short", options=stat_opts(graph="area")), 4, 5),
        (panel("GOMAXPROCS", "stat", [prom_t("max(%s)" % lot("tailscale2otel_runtime_gomaxprocs_ratio"))],
               unit="short", options=stat_opts()), 4, 5),
        (panel("Build info", "table", [prom_t(lot("tailscale2otel_build_info_ratio", WIN_SLOW), instant=True, fmt="table")],
               transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                  "service_instance_id", "service_name", "service_namespace", "Value"],
                                         rename={"version": "Version", "go_version": "Go version"})],
               desc="Version / Go version (labels)."), 8, 5),
    ]
    collectors = [
        (panel("Scrape duration by collector", "timeseries",
               [prom_t("max by (tailscale_collector) (tailscale2otel_scrape_duration_seconds%s)" % cf, legend="{{tailscale_collector}}")],
               unit="s", custom=ts_custom(), options=ts_opts(placement="right")), 12, 7),
        (panel("Scrape success by collector", "timeseries",
               [prom_t("max by (tailscale_collector) (tailscale2otel_scrape_success_ratio%s)" % cf, legend="{{tailscale_collector}}")],
               unit="short", min_=0, max_=1, custom=ts_custom(style="line", fill=10), options=ts_opts(placement="right")), 12, 7),
        (panel("Last scrape age", "table",
               [prom_t("time() - %s" % lot("tailscale2otel_scrape_last_timestamp_seconds" + cf), instant=True, fmt="table")],
               unit="s", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                            "service_instance_id", "service_name", "service_namespace"],
                                                   rename={"tailscale_collector": "Collector", "Value": "Age"})],
               desc="Seconds since each collector's last scrape."), 12, 7),
        (panel("Scrape errors/s by collector / type", "timeseries",
               [prom_t("sum by (tailscale_collector, error_type) (rate(tailscale2otel_scrape_errors_total%s[%s]))" % (cf, RI), legend="{{tailscale_collector}} / {{error_type}}")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 12, 7),
    ]
    api = [
        (panel("API requests/s by status", "timeseries",
               [prom_t("sum by (http_response_status_code) (rate(tailscale2otel_api_requests_total[%s]))" % RI, legend="{{http_response_status_code}}")],
               unit="reqps", custom=ts_custom(stack="normal"), options=ts_opts(placement="right")), 12, 7),
        (panel("API requests/s by endpoint", "timeseries",
               [prom_t("sum by (endpoint) (rate(tailscale2otel_api_requests_total[%s]))" % RI, legend="{{endpoint}}")],
               unit="reqps", custom=ts_custom(stack="normal"), options=ts_opts(placement="right")), 12, 7),
    ]
    api_cond = [
        (panel("API retries/s by endpoint", "timeseries",
               [prom_t("sum by (endpoint) (rate(tailscale2otel_api_retries_total[%s]))" % RI, legend="{{endpoint}}")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 12, 6),
        (panel("Export failures/s by type", "timeseries",
               [prom_t("sum by (error_type) (rate(tailscale2otel_export_failures_total[%s]))" % RI, legend="{{error_type}}")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 12, 6),
    ]
    cardinality = [
        (panel("Active series by metric (top $topn)", "timeseries",
               [prom_t("topk($topn, max by (metric_name) (tailscale2otel_series_active))", legend="{{metric_name}}")],
               unit="short", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Per-metric active series (cap 10k). Watch the flow families."), 12, 8),
        (panel("Dedup set size", "timeseries", [prom_t("max by (dedup_set) (tailscale2otel_dedup_size_ratio)", legend="{{dedup_set}}")],
               unit="short", custom=ts_custom(), options=ts_opts()), 6, 8),
        (panel("Dedup evictions/s", "timeseries",
               [prom_t("sum by (dedup_set) (rate(tailscale2otel_dedup_evictions_total[%s]))" % RI, legend="{{dedup_set}}")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 6, 8),
    ]
    enrich = [
        (panel("Enrich cache age", "timeseries", [prom_t("max(tailscale2otel_enrich_cache_age_seconds)", legend="age")],
               unit="s", custom=ts_custom(), options=ts_opts()), 12, 6),
        (panel("Enrich cache size", "timeseries", [prom_t("max(tailscale2otel_enrich_cache_size_ratio)", legend="devices")],
               unit="short", custom=ts_custom(), options=ts_opts()), 12, 6),
    ]
    runtime = [
        (panel("Memory breakdown", "timeseries",
               [prom_t("max(tailscale2otel_runtime_memory_heap_inuse_bytes)", legend="heap in-use"),
                prom_t("max(tailscale2otel_runtime_memory_heap_sys_bytes - tailscale2otel_runtime_memory_heap_inuse_bytes)", legend="heap idle"),
                prom_t("max(tailscale2otel_runtime_memory_stack_inuse_bytes)", legend="stack in-use"),
                prom_t("max(tailscale2otel_runtime_memory_sys_bytes - tailscale2otel_runtime_memory_heap_sys_bytes - tailscale2otel_runtime_memory_stack_inuse_bytes)", legend="other (non-heap)")],
               unit="bytes", custom=ts_custom(stack="normal", fill=25), options=ts_opts(placement="right"),
               desc="Go memory obtained from the OS (runtime.memory.sys), stacked into in-use heap, idle/reserved heap, stacks, and other non-heap runtime (GC, mspan/mcache). Total height = total sys."), 12, 7),
        (panel("Goroutines & stack", "timeseries",
               [prom_t("max(tailscale2otel_runtime_goroutines_ratio)", legend="goroutines"),
                prom_t("max(tailscale2otel_runtime_memory_stack_inuse_bytes)", legend="stack inuse")],
               unit="short", custom=ts_custom(), options=ts_opts(),
               overrides=[{"matcher": {"id": "byName", "options": "stack inuse"},
                           "properties": [{"id": "unit", "value": "bytes"}, {"id": "custom.axisPlacement", "value": "right"}]}]), 12, 7),
        (panel("GC cycles/s", "timeseries", [prom_t("sum(rate(tailscale2otel_runtime_gc_count_total[%s]))" % RI, legend="gc/s")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 8, 6),
        (panel("GC pause/s", "timeseries", [prom_t("sum(rate(tailscale2otel_runtime_gc_pause_time_seconds_total[%s]))" % RI, legend="pause s/s")],
               unit="s", custom=ts_custom(), options=ts_opts()), 8, 6),
        (panel("GC CPU fraction", "timeseries", [prom_t("max(tailscale2otel_runtime_gc_cpu_fraction_ratio)", legend="gc cpu")],
               unit="percentunit", custom=ts_custom(), options=ts_opts()), 8, 6),
        (panel("GC next-target vs live heap", "timeseries",
               [prom_t("max(tailscale2otel_runtime_gc_next_target_bytes)", legend="next GC target"),
                prom_t("max(tailscale2otel_runtime_memory_heap_alloc_bytes)", legend="live heap")],
               unit="bytes", custom=ts_custom(), options=ts_opts(),
               desc="Live heap vs the heap size that triggers the next GC; the gap is GC headroom."), 8, 6),
        (panel("Heap alloc churn", "timeseries",
               [prom_t("sum(rate(tailscale2otel_runtime_memory_alloc_bytes_total[%s]))" % RI, legend="alloc/s")],
               unit="Bps", custom=ts_custom(), options=ts_opts(),
               desc="Cumulative heap-allocation rate (includes freed); allocation churn / GC pressure."), 8, 6),
        (panel("Live heap objects", "timeseries",
               [prom_t("max(tailscale2otel_runtime_memory_heap_objects_ratio)", legend="objects")],
               unit="short", custom=ts_custom(), options=ts_opts(),
               desc="Number of live heap objects (a count, despite the _ratio suffix)."), 8, 6),
    ]
    reliability = [
        (panel("Scrape errors/s", "timeseries",
               [prom_t("sum by (tailscale_collector) (rate(tailscale2otel_scrape_errors_total[%s]))" % RI, legend="{{tailscale_collector}}")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 6, 6),
        (panel("Checkpoint persist errors/s", "timeseries",
               [prom_t("sum by (tailscale_collector) (rate(tailscale2otel_checkpoint_persist_errors_total[%s]))" % RI, legend="{{tailscale_collector}}")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 6, 6),
        (panel("Component errors/s", "timeseries",
               [prom_t("sum by (component) (rate(tailscale2otel_component_errors_total[%s]))" % RI, legend="{{component}}")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 6, 6),
        (panel("Admin auth rejected/s", "timeseries",
               [prom_t("sum by (reason) (rate(tailscale2otel_admin_auth_rejected_total[%s]))" % RI, legend="{{reason}}")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 6, 6),
    ]
    # --- WU9: app-health (config validity, uptime, CPU, checkpoint) — supersedes C9 stubs.
    apphealth = [
        (panel("Config valid", "stat", [prom_t("max(tailscale2otel_config_valid_ratio)")],
               mappings=BOOL_MAP, thresholds=thr([(None, "red"), (1, "green")]),
               options=stat_opts(color="background")), 4, 5),
        (panel("Config warnings", "stat", [prom_t("max(tailscale2otel_config_warnings_ratio) or vector(0)")],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="value")), 4, 5),
        (panel("Uptime", "stat", [prom_t("max(process_uptime_seconds)")],
               unit="s", options=stat_opts()), 4, 5),
        (panel("Checkpoint disk", "stat", [prom_t("max(tailscale2otel_checkpoint_disk_size_bytes) or vector(0)")],
               unit="bytes", novalue="0", options=stat_opts()), 4, 5),
        (panel("Process CPU (user/system)", "timeseries",
               [prom_t("sum by (cpu_mode) (rate(process_cpu_time_seconds_total[%s]))" % RI, legend="{{cpu_mode}}")],
               unit="percentunit", custom=ts_custom(), options=ts_opts(),
               desc="CPU seconds/s by mode (~cores)."), 12, 6),
        (panel("Checkpoint persist age", "timeseries",
               [prom_t("max(tailscale2otel_checkpoint_persist_age_seconds) or vector(0)", legend="persist age")],
               unit="s", novalue="0", custom=ts_custom(), options=ts_opts(),
               desc="Absent when the checkpoint store is not file-backed (in-memory)."), 12, 6),
    ]
    # --- WU9 A: API latency histograms (present="has_api_hist").
    _apilat_p = panel("API latency p50/p95/p99 by endpoint", "timeseries",
                      [prom_t(hq("0.5", "tailscale2otel_api_duration_seconds", by="endpoint"), legend="p50 {{endpoint}}"),
                       prom_t(hq("0.95", "tailscale2otel_api_duration_seconds", by="endpoint"), legend="p95 {{endpoint}}", refid="B"),
                       prom_t(hq("0.99", "tailscale2otel_api_duration_seconds", by="endpoint"), legend="p99 {{endpoint}}", refid="C")],
                      unit="s", custom=ts_custom(), options=ts_opts(placement="right"),
                      desc="Per-endpoint API latency quantiles (exemplars enabled).")
    for _q in builder.ELEMENTS[_apilat_p]["spec"]["data"]["spec"]["queries"]:
        _q["spec"]["query"]["spec"]["exemplar"] = True  # Prometheus query-level exemplar fetch
    apilat = [
        (_apilat_p, 12, 7),
        (panel("API 429 / retries", "timeseries",
               [prom_t('sum(rate(tailscale2otel_api_requests_total{http_response_status_code="429"}[%s]))' % RI, legend="429/s"),
                prom_t("sum(rate(tailscale2otel_api_retries_total[%s]))" % RI, legend="retries/s", refid="B")],
               unit="cps", novalue="0", custom=ts_custom(), options=ts_opts()), 12, 7),
    ]
    # --- WU9 B: export latency histograms (present="has_export_hist").
    exportlat = [
        (panel("Export latency p50/p95/p99 by signal", "timeseries",
               [prom_t(hq("0.5", "tailscale2otel_export_duration_seconds", by="signal"), legend="p50 {{signal}}"),
                prom_t(hq("0.95", "tailscale2otel_export_duration_seconds", by="signal"), legend="p95 {{signal}}", refid="B"),
                prom_t(hq("0.99", "tailscale2otel_export_duration_seconds", by="signal"), legend="p99 {{signal}}", refid="C")],
               unit="s", custom=ts_custom(), options=ts_opts(placement="right")), 12, 7),
        (panel("Export outcome rate", "timeseries",
               [prom_t("sum by (outcome) (rate(tailscale2otel_export_duration_seconds_count[%s]))" % RI, legend="{{outcome}}")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 12, 7),
    ]
    # --- WU9 C: scrape freshness (present="has_staleness").
    freshness = [
        (panel("Scrape staleness", "timeseries",
               [prom_t('max by (tailscale_collector) (tailscale2otel_scrape_staleness_seconds{tailscale_collector=~"$collector"})',
                       legend="{{tailscale_collector}}")],
               unit="s", custom=ts_custom(), options=ts_opts(placement="right")), 12, 7),
        (panel("Scrape budget headroom", "bargauge",
               [prom_t('max by (tailscale_collector) (tailscale2otel_scrape_budget_ratio{tailscale_collector=~"$collector"})',
                       legend="{{tailscale_collector}}")],
               unit="percentunit", thresholds=thr([(None, "green"), (0.8, "yellow"), (1, "red")]),
               options=bargauge_opts()), 12, 7),
    ]
    # --- WU9 E: rDNS resolver (present="has_rdns").
    rdns = [
        (panel("rDNS cache fill", "stat",
               [prom_t("%s / clamp_min(%s, 1)" % (lot("tailscale_rdns_cache_entries_ratio", WIN_FAST),
                                                  lot("tailscale_rdns_cache_capacity_ratio", WIN_FAST)))],
               unit="percentunit", options=stat_opts()), 6, 6),
        (panel("rDNS lookup hit-rate", "timeseries",
               [prom_t('sum(rate(tailscale_rdns_cache_lookups_total{result="hit"}[%s])) / clamp_min(sum(rate(tailscale_rdns_cache_lookups_total[%s])), 1)' % (RI, RI),
                       legend="hit-rate")],
               unit="percentunit", custom=ts_custom(), options=ts_opts()), 9, 6),
        (panel("rDNS upstream queries/s", "timeseries",
               [prom_t("sum by (result) (rate(tailscale_rdns_queries_total[%s]))" % RI, legend="query {{result}}"),
                prom_t("rate(tailscale_rdns_cache_evictions_total[%s])" % RI, legend="evictions/s", refid="B")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 9, 6),
    ]
    # --- WU9 F: PII filter status metadata (present="has_pii"; NOT PII-gated — this is metadata about pii).
    pii_status = [
        (panel("PII filter status", "table",
               [prom_t("%s" % lot("tailscale2otel_pii_filter_category_ratio", WIN_FAST), instant=True, fmt="table")],
               mappings=vmap({"0": {"text": "REDACTED", "color": "red", "index": 0},
                              "1": {"text": "emitted", "color": "green", "index": 1}}),
               transformations=[organize(exclude=TBL_NOISE,
                                         rename={"category": "Category", "Value": "State"})],
               desc="Compliance view: every category should read 'emitted' (==1) unless redacted."), 12, 7),
    ]
    # --- WU9 G: per-tailnet API errors (present="has_multitailnet"; empty on single-tailnet).
    pertailnet = [
        (panel("Per-tailnet API errors", "timeseries",
               [prom_t('sum by (tailscale_tailnet) (rate(tailscale2otel_api_requests_total{http_response_status_code=~"4..|5..", tailscale_tailnet!=""}[%s]))' % RI,
                       legend="{{tailscale_tailnet}}")],
               unit="cps", novalue="0", custom=ts_custom(), options=ts_opts(placement="right")), 24, 7),
    ]
    # --- WU9 I: traces & spans (tracing opt-in; rely on panel empty-state, no present gate).
    _trace_desc = "Trace panels are empty if tracing.enabled=false."
    traces = [
        (panel("Scrape → API trace waterfall", "traces",
               [tempo_t('{ resource.service.name = "tailscale2otel" && name =~ "scrape.+" }')],
               desc=_trace_desc), 24, 9),
    ]
    traces2 = [
        (panel("API p95 by endpoint (traces)", "timeseries",
               [tempo_t('{span.tailscale.endpoint != "" && resource.service.name = "tailscale2otel"} '
                   '| quantile_over_time(duration, 0.95) by (span.tailscale.endpoint)')],
               unit="s", custom=ts_custom(), options=ts_opts(placement="right"), desc=_trace_desc), 12, 7),
        (panel("Scrape cadence by collector (traces)", "timeseries",
               [tempo_t('{name =~ "scrape.+" && resource.service.name = "tailscale2otel"} | rate() by (name)')],
               custom=ts_custom(), options=ts_opts(placement="right"), desc=_trace_desc), 12, 7),
        (panel("stream.receive batch size (traces)", "timeseries",
               [tempo_t('{name = "stream.receive" && resource.service.name = "tailscale2otel"} '
                   '| avg_over_time(span.tailscale.stream.flows) by (resource.service.instance.id)'),
                tempo_t('{name = "stream.receive" && resource.service.name = "tailscale2otel"} '
                   '| avg_over_time(span.http.request.body.size) by (resource.service.instance.id)', refid="B")],
               custom=ts_custom(), options=ts_opts(placement="right"), desc=_trace_desc), 24, 7),
    ]
    return [row("Liveness & build", live), row("App health", apphealth, present="has_selfobs"),
            row("Collectors", collectors), row("API & export", api),
            row("API retries & export failures", api_cond, present="has_api_retry"),
            row("API latency", apilat, present="has_api_hist"),
            row("Export latency", exportlat, present="has_export_hist"),
            row("Scrape freshness", freshness, present="has_staleness"),
            row("Cardinality & dedup", cardinality), row("Enrichment cache", enrich),
            row("rDNS resolver", rdns, present="has_rdns"),
            row("PII filter status", pii_status, present="has_pii"),
            row("Per-tailnet API errors", pertailnet, present="has_multitailnet"),
            row("Go runtime", runtime), row("Reliability", reliability, present="has_scrape_err"),
            row("Traces & spans", traces), row("Traces & spans (metrics)", traces2)]
