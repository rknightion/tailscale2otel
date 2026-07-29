"""tab_diagnostics() — moved out of build.py in the module split."""

from builder import (bargauge_opts, hq, lot, organize, panel, PII, prom_t, RI, row,
                     sentinel, stat_opts, TBL_NOISE, tempo_t, thr, ts_custom, ts_opts,
                     vmap, WIN_FAST, WIN_SLOW)
from maps import bool_map, BOOL_HEALTHY_ON, UP_MAP
import builder


# Prerequisite-aware empty states for the optional sections (#385/#386).
#
# Absence of a series cannot distinguish "the feature is switched off" from "this
# tailnet does not have it" from "it was never deployed" — all three render as no
# data. So every string below NAMES the configuration key and the prerequisite and
# stops there; none of them claims which prerequisite is the unmet one, because
# that claim would be a guess rendered as fact.
_OBJ_EMPTY = ("No object-store ingestion series. Requires collectors.flowlogs.objectstore "
              "(or collectors.auditlogs.objectstore) with the matching source: objectstore, "
              "and self_observability.enabled.")
_WAL_EMPTY = ("No ingress-WAL series. Requires ingress_wal.enabled together with a receiver "
              "that accepts payloads (streaming or webhook), and self_observability.enabled.")
_SUBREQ_EMPTY = ("No per-entity subrequest series. Requires a collector that fans out per entity "
                 "— devices posture attributes, device invites, or user invites — and "
                 "self_observability.enabled.")
_CAP_EMPTY = ("No capability-preflight series. Requires self_observability.enabled; the scope "
              "preflight additionally requires tailscale.auth.method: oauth, as an API key "
              "carries no scope list to compare against.")
_RL_EMPTY = ("No rate-limiter wait recorded. Requires tailscale.http.rate_limit (or a per-tailnet "
             "http.rate_limit) above 0 and at least one request that actually blocked — a request "
             "that waits zero is never recorded.")
_PROBE_EMPTY = ("No API probe timestamps. Requires self_observability.enabled and at least one "
                "collector that has probed an API operation.")
_STREAM_EMPTY = ("No streaming-receiver series. Requires streaming.enabled with "
                 "collectors.flowlogs.source or collectors.auditlogs.source set to stream.")
_WEBHOOK_EMPTY = ("No webhook-receiver series. Requires webhook.enabled and at least one Tailscale "
                  "webhook endpoint delivering to it.")
_RDNS_EMPTY = ("No reverse-DNS cache series. Requires enrichment.reverse_dns.enabled and "
               "self_observability.enabled.")


def tab_diagnostics():
    # Presence sentinels this tab declares (moved from variables.py, #495).
    sentinel("has_selfobs", "tailscale2otel_series_active")  # also consumed by cardinality.py, overview.py, events.py
    sentinel("has_api_retry", "tailscale2otel_api_retries_total")
    sentinel("has_scrape_err", "tailscale2otel_scrape_errors_total")
    sentinel("has_api_hist", "tailscale2otel_api_duration_seconds_count")
    sentinel("has_export_hist", "tailscale2otel_export_duration_seconds_count")
    sentinel("has_staleness", "tailscale2otel_scrape_staleness_seconds")
    sentinel("has_pii", "tailscale2otel_pii_filter_category_ratio")
    sentinel("has_rdns", "tailscale_rdns_cache_entries_ratio")

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
    # #369: p50/p95/p99 from tailscale2otel.scrape.duration.histogram, with exemplars enabled so a
    # bucket links back to the scrape trace. This SUPERSEDES the old "Scrape duration by collector"
    # gauge panel (last-scrape-duration-only, no distribution) rather than running both side by side
    # showing near-identical per-collector duration-over-time information.
    _scrapedur_p = panel(
        "Scrape duration p50/p95/p99 by collector", "timeseries",
        [prom_t('histogram_quantile(0.5, sum by (le, tailscale_collector) '
                '(rate(tailscale2otel_scrape_duration_histogram_seconds_bucket%s[%s])))' % (cf, RI),
                legend="p50 {{tailscale_collector}}"),
         prom_t('histogram_quantile(0.95, sum by (le, tailscale_collector) '
                '(rate(tailscale2otel_scrape_duration_histogram_seconds_bucket%s[%s])))' % (cf, RI),
                legend="p95 {{tailscale_collector}}", refid="B"),
         prom_t('histogram_quantile(0.99, sum by (le, tailscale_collector) '
                '(rate(tailscale2otel_scrape_duration_histogram_seconds_bucket%s[%s])))' % (cf, RI),
                legend="p99 {{tailscale_collector}}", refid="C")],
        unit="s", custom=ts_custom(), options=ts_opts(placement="right"),
        desc="Per-collector scrape wall-clock duration quantiles (exemplars enabled — link a bucket "
             "back to the scrape trace). Supersedes the plain scrape-duration gauge panel (#369).")
    for _q in builder.ELEMENTS[_scrapedur_p]["spec"]["data"]["spec"]["queries"]:
        _q["spec"]["query"]["spec"]["exemplar"] = True  # Prometheus query-level exemplar fetch
    collectors = [
        (_scrapedur_p, 12, 7),
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
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Tailscale API calls retried (429/5xx/timeout), by endpoint."), 12, 6),
        (panel("Export failures/s by type", "timeseries",
               [prom_t("sum by (error_type) (rate(tailscale2otel_export_failures_total[%s]))" % RI, legend="{{error_type}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="OTLP export attempts that failed, by error type."), 12, 6),
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
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Collector scrape attempts that failed, by collector."), 6, 6),
        (panel("Checkpoint persist errors/s", "timeseries",
               [prom_t("sum by (tailscale_collector) (rate(tailscale2otel_checkpoint_persist_errors_total[%s]))" % RI, legend="{{tailscale_collector}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Checkpoint-store writes that failed, by collector."), 6, 6),
        (panel("Component errors/s", "timeseries",
               [prom_t("sum by (component) (rate(tailscale2otel_component_errors_total[%s]))" % RI, legend="{{component}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Internal component errors, by component."), 6, 6),
        (panel("Admin auth rejected/s", "timeseries",
               [prom_t("sum by (reason) (rate(tailscale2otel_admin_auth_rejected_total[%s]))" % RI, legend="{{reason}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Admin-endpoint requests rejected by auth, by reason."), 6, 6),
    ]
    # --- WU9: app-health (config validity, uptime, CPU, checkpoint) — supersedes C9 stubs.
    apphealth = [
        (panel("Config valid", "stat", [prom_t("max(tailscale2otel_config_valid_ratio)")],
               mappings=BOOL_HEALTHY_ON, thresholds=thr([(None, "red"), (1, "green")]),
               options=stat_opts(color="background"),
               desc="Whether the running config passed validation at startup."), 4, 5),
        (panel("Config warnings", "stat", [prom_t("max(tailscale2otel_config_warnings_ratio) or vector(0)")],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="value"),
               desc="Active configuration advisories from startup (a count, despite the "
                    "_ratio suffix)."), 4, 5),
        (panel("Uptime", "stat", [prom_t("max(process_uptime_seconds)")],
               unit="s", options=stat_opts(),
               desc="Time since the process started."), 4, 5),
        # No zero-fill on either checkpoint gauge: emitCheckpointStats() deliberately emits
        # nothing when the file is absent (in-memory store, or nothing persisted yet), so a
        # zero here would invent a healthy-looking answer out of "not applicable" (#385).
        (panel("Checkpoint disk", "stat", [prom_t("max(tailscale2otel_checkpoint_disk_size_bytes)")],
               unit="bytes", options=stat_opts(),
               novalue="No checkpoint file — the store is not file-backed, or nothing has "
                       "persisted yet.",
               desc="On-disk size of the file-backed checkpoint store."), 4, 5),
        (panel("Process CPU (user/system)", "timeseries",
               [prom_t("sum by (cpu_mode) (rate(process_cpu_time_seconds_total[%s]))" % RI, legend="{{cpu_mode}}")],
               unit="percentunit", custom=ts_custom(), options=ts_opts(),
               desc="CPU seconds/s by mode (~cores)."), 12, 6),
        (panel("Checkpoint persist age", "timeseries",
               [prom_t("max(tailscale2otel_checkpoint_persist_age_seconds)", legend="persist age")],
               unit="s", custom=ts_custom(), options=ts_opts(),
               novalue="No checkpoint file — the store is not file-backed, or nothing has "
                       "persisted yet.",
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
               unit="cps", novalue="0", custom=ts_custom(), options=ts_opts(),
               desc="Rate-limit responses and the retries they trigger, fleet-wide."), 12, 7),
    ]
    # --- WU9 B: export latency histograms (present="has_export_hist").
    exportlat = [
        (panel("Export latency p50/p95/p99 by signal", "timeseries",
               [prom_t(hq("0.5", "tailscale2otel_export_duration_seconds", by="signal"), legend="p50 {{signal}}"),
                prom_t(hq("0.95", "tailscale2otel_export_duration_seconds", by="signal"), legend="p95 {{signal}}", refid="B"),
                prom_t(hq("0.99", "tailscale2otel_export_duration_seconds", by="signal"), legend="p99 {{signal}}", refid="C")],
               unit="s", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="OTLP export latency quantiles, by signal (metrics/logs/traces)."), 12, 7),
        (panel("Export outcome rate", "timeseries",
               [prom_t("sum by (outcome) (rate(tailscale2otel_export_duration_seconds_count[%s]))" % RI, legend="{{outcome}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="OTLP export attempts completed per second, by outcome (success/failure)."), 12, 7),
    ]
    # --- WU9 C: scrape freshness (present="has_staleness").
    freshness = [
        (panel("Scrape staleness", "timeseries",
               [prom_t('max by (tailscale_collector) (tailscale2otel_scrape_staleness_seconds{tailscale_collector=~"$collector"})',
                       legend="{{tailscale_collector}}")],
               unit="s", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Time since each collector's last successful scrape."), 12, 7),
        (panel("Scrape budget headroom", "bargauge",
               [prom_t('max by (tailscale_collector) (tailscale2otel_scrape_budget_ratio{tailscale_collector=~"$collector"})',
                       legend="{{tailscale_collector}}")],
               unit="percentunit", thresholds=thr([(None, "green"), (0.8, "yellow"), (1, "red")]),
               options=bargauge_opts(),
               desc="Last scrape duration as a fraction of the collector's poll interval; near "
                    "or above 1 means the scrape risks overrunning its interval."), 12, 7),
    ]
    # --- WU9 E: rDNS resolver (present="has_rdns").
    rdns = [
        (panel("rDNS cache fill", "stat",
               [prom_t("%s / clamp_min(%s, 1)" % (lot("tailscale_rdns_cache_entries_ratio", WIN_FAST),
                                                  lot("tailscale_rdns_cache_capacity_ratio", WIN_FAST)))],
               unit="percentunit", options=stat_opts(),
               desc="rDNS cache entries as a fraction of configured capacity."), 6, 6),
        # Both hit and stale are served FROM CACHE without the caller waiting on a
        # resolver, so both belong in the numerator (#297). Counting only "hit"
        # would make this rate fall every time stale serving did its job, which
        # reads as the cache getting worse at the moment it saved a lookup. The
        # stale share rides alongside so the two are still tellable apart.
        (panel("rDNS lookup hit-rate", "timeseries",
               [prom_t('sum(rate(tailscale_rdns_cache_lookups_total{result=~"hit|stale"}[%s])) / clamp_min(sum(rate(tailscale_rdns_cache_lookups_total[%s])), 1)' % (RI, RI),
                       legend="cache-served"),
                prom_t('sum(rate(tailscale_rdns_cache_lookups_total{result="stale"}[%s])) / clamp_min(sum(rate(tailscale_rdns_cache_lookups_total[%s])), 1)' % (RI, RI),
                       legend="of which stale", refid="B")],
               unit="percentunit", custom=ts_custom(), options=ts_opts(),
               desc="Share of rDNS lookups served from cache rather than an upstream query. "
                    "'of which stale' is the part served past its TTL while a refresh ran "
                    "(enrichment.reverse_dns.stale_ttl) — those names are still correct, just "
                    "older than cache_ttl."), 9, 6),
        (panel("rDNS upstream queries/s", "timeseries",
               [prom_t("sum by (result) (rate(tailscale_rdns_queries_total[%s]))" % RI, legend="query {{result}}"),
                prom_t("rate(tailscale_rdns_cache_evictions_total[%s])" % RI, legend="evictions/s", refid="B"),
                # Refreshes are a SUBSET of the queries above, not additional load —
                # they are the ones triggered by serving a stale name. Sustained
                # refresh failure is the warning that stale names are heading for
                # expiry rather than renewal.
                prom_t("sum by (result) (rate(tailscale_rdns_refreshes_total[%s]))" % RI,
                       legend="refresh {{result}}", refid="C")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Upstream PTR queries issued and cache evictions, per second. The refresh "
                    "series are the subset of those queries triggered by a stale-serving hit."), 9, 6),
        # An overflow rate on its own is unreadable — "12/s dropped" needs "out of how
        # many", so the accepted hot-path lookup rate shares the panel (#405).
        (panel("rDNS cache overflows vs lookups/s", "timeseries",
               [prom_t("sum(rate(tailscale_rdns_cache_overflows_total[%s]))" % RI, legend="overflows/s"),
                prom_t("sum(rate(tailscale_rdns_cache_lookups_total[%s]))" % RI, legend="lookups/s (accepted)", refid="B")],
               unit="cps", custom=ts_custom(), options=ts_opts(), novalue=_RDNS_EMPTY,
               desc="An overflow is a hot-path miss for a NEW address that could not be scheduled "
                    "because the cache was already at enrichment.reverse_dns.max_entries — the "
                    "address is simply never resolved. Read it against the accepted lookup rate: a "
                    "sustained non-zero share means max_entries is too small."), 24, 6),
    ]
    # --- WU9 F: PII filter status metadata (present="has_pii"; NOT PII-gated — this is metadata about pii).
    pii_status = [
        (panel("PII filter status", "table",
               [prom_t("%s" % lot("tailscale2otel_pii_filter_category_ratio", WIN_FAST), instant=True, fmt="table")],
               mappings=bool_map("REDACTED", "emitted", "red", "green"),
               transformations=[organize(exclude=TBL_NOISE,
                                         rename={"category": "Category", "Value": "State"})],
               desc="Compliance view: every category should read 'emitted' (==1) unless redacted."), 12, 7),
    ]
    # --- WU9 G: per-tailnet API errors (present="has_multitailnet"; empty on single-tailnet).
    pertailnet = [
        (panel("Per-tailnet API errors", "timeseries",
               [prom_t('sum by (tailscale_tailnet) (rate(tailscale2otel_api_requests_total{http_response_status_code=~"4..|5..", tailscale_tailnet!=""}[%s]))' % RI,
                       legend="{{tailscale_tailnet}}")],
               unit="cps", novalue="0", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Tailscale API 4xx/5xx responses per second, split out per tailnet on a "
                    "multi-tailnet deployment."), 24, 7),
    ]
    # --- #399: object-store ingestion (flow/audit export bucket).
    #
    # Every panel here relies on its own empty state rather than a presence sentinel:
    # the whole family is absent on a poll- or stream-fed deployment, and a named
    # prerequisite reads better than a row that silently is not there.
    #
    # Bounded-cardinality/PII contract: the family's only attributes are `reason`,
    # `limit`, `operation` and `outcome`. Nothing here groups by, or names, an object
    # key, bucket, prefix, endpoint or credential.
    objstore_fresh = [
        (panel("Object-store cursor age", "stat",
               [prom_t("max(%s)" % lot("tailscale2otel_objectstore_cursor_age_seconds"))],
               unit="s", options=stat_opts(graph="area"), novalue=_OBJ_EMPTY,
               desc="End-to-end ingestion lag: now minus the timestamp the last cycle left "
                    "persisted. In a healthy feed it settles near the export's write cadence plus "
                    "one collection interval. Never absent, never negative; a cold start reports "
                    "the configured initial lookback rather than zero."), 5, 5),
        (panel("Newest exported object age", "stat",
               [prom_t("max(%s)" % lot("tailscale2otel_objectstore_discovered_newest_age_seconds"))],
               unit="s", options=stat_opts(),
               # -1 is a deliberate sentinel, not an age. Rendering it as "-1 s" is how a
               # silent exporter reads as the freshest possible feed.
               mappings=vmap({"-1": {"text": "no timestamped object listed", "color": "orange", "index": 0}}),
               novalue=_OBJ_EMPTY,
               desc="How fresh the EXPORT's own writes are, independent of what was ingested — "
                    "objects skipped as already ingested still count. -1 means the cycle listed no "
                    "object with a usable timestamp (an empty or misconfigured prefix, or an export "
                    "quieter than the listing window reaches); it is deliberately distinguishable "
                    "from a fresh zero-second age."), 5, 5),
        (panel("Object-store gaps clear", "stat",
               [prom_t("min(%s)" % lot("tailscale2otel_objectstore_gap_healthy_ratio"))],
               mappings=bool_map("GAPS", "CLEAN", "red", "green"),
               thresholds=thr([(None, "red"), (1, "green")]),
               options=stat_opts(color="background"), novalue=_OBJ_EMPTY,
               desc="One when no unresolved gap remains; zero means at least one pending or "
                    "quarantined object is outstanding."), 5, 5),
        (panel("Object listing complete", "stat",
               [prom_t("max(%s)" % lot("tailscale2otel_objectstore_scan_truncated_ratio"))],
               mappings=bool_map("complete", "truncated", "green", "red"),
               thresholds=thr([(None, "green"), (1, "red")]),
               options=stat_opts(color="background"), novalue=_OBJ_EMPTY,
               desc="One means unexamined listing ground remains after the last cycle (a truncated "
                    "S3 page, or a listed object not yet durably handled), which makes the backlog "
                    "and oldest-pending-age panels lower bounds rather than totals."), 5, 5),
        (panel("Backlog & oldest pending object age", "timeseries",
               [prom_t("max(tailscale2otel_objectstore_backlog_ratio)", legend="objects waiting"),
                prom_t("max(tailscale2otel_objectstore_pending_oldest_age_seconds)", legend="oldest pending age", refid="B")],
               unit="short", custom=ts_custom(), options=ts_opts(), novalue=_OBJ_EMPTY,
               overrides=[{"matcher": {"id": "byName", "options": "oldest pending age"},
                           "properties": [{"id": "unit", "value": "s"},
                                          {"id": "custom.axisPlacement", "value": "right"}]}],
               desc="Objects listed but NOT YET ingested, and how stale the next one to be "
                    "processed already is. This is the waiting population only — an object that "
                    "fails leaves it for the gap panel — so zero here pairs with a zero backlog "
                    "and means nothing is queued. Both are lower bounds while the listing is "
                    "truncated."), 12, 7),
        (panel("Unresolved gaps & oldest gap age", "timeseries",
               [prom_t("max(tailscale2otel_objectstore_gaps_ratio)", legend="failed objects"),
                prom_t("max(tailscale2otel_objectstore_gap_oldest_age_seconds)", legend="oldest gap age", refid="B")],
               unit="short", custom=ts_custom(), options=ts_opts(), novalue=_OBJ_EMPTY,
               overrides=[{"matcher": {"id": "byName", "options": "oldest gap age"},
                           "properties": [{"id": "unit", "value": "s"},
                                          {"id": "custom.axisPlacement", "value": "right"}]}],
               desc="FAILED objects awaiting retry or operator acknowledgement, and the age of the "
                    "oldest. A different question from the pending backlog and a different "
                    "population: this one ages objects that already failed, and a gap aging without "
                    "the count falling is an object failing repeatedly rather than recovering."), 12, 7),
    ]
    objstore_flow = [
        (panel("Objects & records ingested/s", "timeseries",
               [prom_t("sum(rate(tailscale2otel_objectstore_objects_total[%s]))" % RI, legend="objects/s"),
                prom_t("sum(rate(tailscale2otel_objectstore_records_total[%s]))" % RI, legend="records/s", refid="B")],
               unit="cps", custom=ts_custom(), options=ts_opts(), novalue=_OBJ_EMPTY,
               desc="Objects fully ingested and flow-log records decoded out of them. Compare the "
                    "record rate against the flow metrics to see what de-duplication removed."), 8, 7),
        (panel("Object bytes read vs decompressed", "timeseries",
               [prom_t("sum(rate(tailscale2otel_objectstore_bytes_total[%s]))" % RI, legend="compressed read"),
                prom_t("sum(rate(tailscale2otel_objectstore_decompressed_bytes_total[%s]))" % RI, legend="decompressed", refid="B")],
               unit="Bps", custom=ts_custom(), options=ts_opts(), novalue=_OBJ_EMPTY,
               desc="Transfer cost (compressed bytes actually read, including unsuccessful "
                    "attempts) against expansion (decompressed bytes consumed, including attempts "
                    "stopped by a limit). Use THESE two for object-store cost and expansion, not "
                    "tailscale2otel.ingest.size — that one now reports decoded payload bytes for "
                    "every source so it can be summed across sources."), 8, 7),
        (panel("Object retries/s", "timeseries",
               [prom_t("sum(rate(tailscale2otel_objectstore_retries_total[%s]))" % RI, legend="retries/s")],
               unit="cps", custom=ts_custom(), options=ts_opts(), novalue=_OBJ_EMPTY,
               desc="OBJECT-level retries: a failed object becomes a durable gap and is attempted "
                    "again on a later cycle under a bounded backoff. There is no transport-level "
                    "retry here, and a first attempt on a newly listed object never counts. "
                    "Emitted every cycle including zero, so a flat line is a healthy feed."), 8, 7),
        (panel("Objects skipped/s by reason", "timeseries",
               [prom_t("sum by (reason) (rate(tailscale2otel_objectstore_skipped_total[%s]))" % RI, legend="{{reason}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"), novalue=_OBJ_EMPTY,
               desc="Objects or individual rows not ingested. `decode_error` and `semantic_invalid` "
                    "are single rows discarded from an object that still completed; a sustained "
                    "`per_cycle_budget` means the per-cycle object cap is holding ingestion behind "
                    "the bucket; `future_timestamp` means objects are named beyond the 5-minute "
                    "clock-skew allowance — check the export's clock."), 12, 7),
        (panel("Undecodable objects (broken feed)", "stat",
               [prom_t('sum(increase(tailscale2otel_objectstore_skipped_total{reason="undecodable_object"}[$__range])) or vector(0)',
                       instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "red")]),
               options=stat_opts(color="background"), novalue=_OBJ_EMPTY,
               desc="Whole objects that decoded ZERO records while at least one row failed — the "
                    "signature of an export whose framing is not newline-delimited records. Treat "
                    "ANY non-zero value as a feed-level fault, not as corrupt data: the entire "
                    "object is discarded and becomes a retried gap. Reading note: for a wholly "
                    "failed object the row-local decode_error/semantic_invalid reasons are "
                    "deliberately not emitted, so it shows as ONE undecodable_object rather than "
                    "N decode_error."), 12, 7),
        (panel("Expansion limit failures/s by limit", "timeseries",
               [prom_t("sum by (limit) (rate(tailscale2otel_objectstore_expansion_limit_failures_total[%s]))" % RI,
                       legend="{{limit}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(), novalue=_OBJ_EMPTY,
               desc="Ingestion attempts stopped by a configured wire-byte, decompressed-byte or "
                    "record-count budget. The bounded `limit` attribute names which object or "
                    "cycle limit was breached."), 8, 7),
        (panel("Object-store provider requests/s", "timeseries",
               [prom_t("sum by (operation, outcome) (rate(tailscale2otel_objectstore_requests_total[%s]))" % RI,
                       legend="{{operation}} {{outcome}}")],
               unit="reqps", custom=ts_custom(), options=ts_opts(placement="right"), novalue=_OBJ_EMPTY,
               desc="TRANSPORT health only, and at most four series. `error` means the LIST or GET "
                    "call itself returned an error — never a decode, validation, framing or limit "
                    "failure, which the skipped and gap panels carry. A body that fails mid-read "
                    "counts as a SUCCESSFUL get here."), 8, 7),
        (panel("Object-store provider latency p50/p95/p99", "timeseries",
               [prom_t(hq("0.5", "tailscale2otel_objectstore_request_duration_seconds", by="operation"), legend="p50 {{operation}}"),
                prom_t(hq("0.95", "tailscale2otel_objectstore_request_duration_seconds", by="operation"), legend="p95 {{operation}}", refid="B"),
                prom_t(hq("0.99", "tailscale2otel_objectstore_request_duration_seconds", by="operation"), legend="p99 {{operation}}", refid="C")],
               unit="s", custom=ts_custom(), options=ts_opts(placement="right"), novalue=_OBJ_EMPTY,
               desc="Times the provider call itself. For `get` that is obtaining the object's "
                    "reader — NOT streaming, decompressing and decoding its body, which the object, "
                    "record and byte counters measure."), 8, 7),
    ]
    # --- #386: durable ingress WAL (receiver acceptance before processing).
    wal = [
        (panel("Ingress WAL pending & retained entries", "timeseries",
               [prom_t("max(tailscale2otel_ingress_wal_pending_entries_ratio)", legend="pending entries"),
                prom_t("max(tailscale2otel_ingress_wal_orphan_stages_ratio)", legend="retained staging files", refid="B"),
                prom_t("max(tailscale2otel_ingress_wal_completion_markers_ratio)", legend="completion markers", refid="C")],
               unit="short", custom=ts_custom(), options=ts_opts(), novalue=_WAL_EMPTY,
               desc="Durable entries awaiting processor application and flush, staging files kept "
                    "for bounded recovery cleanup, and transient completion markers awaiting "
                    "durable cleanup. All three are counts despite the _ratio suffix. Pending "
                    "climbing toward ingress_wal.max_entries fails new requests closed."), 12, 6),
        (panel("Ingress WAL on-disk bytes", "timeseries",
               [prom_t("max(tailscale2otel_ingress_wal_pending_size_bytes)", legend="pending"),
                prom_t("max(tailscale2otel_ingress_wal_orphan_size_bytes)", legend="retained staging", refid="B")],
               unit="bytes", custom=ts_custom(stack="normal"), options=ts_opts(), novalue=_WAL_EMPTY,
               desc="Encoded bytes on the WAL filesystem. There is no TTL or eviction, so this is "
                    "bounded only by ingress_wal.max_bytes — reaching it fails new requests "
                    "closed."), 12, 6),
    ]
    # --- #386: per-entity subrequest fan-out (the N+1 calls inside a scrape).
    subreq = [
        (panel("Subrequest attempts/s", "timeseries",
               [prom_t("sum by (tailscale_collector, tailscale_subrequest) (rate(tailscale2otel_subrequest_attempts_total[%s]))" % RI,
                       legend="{{tailscale_collector}} / {{tailscale_subrequest}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"), novalue=_SUBREQ_EMPTY,
               desc="Per-entity calls made inside a scrape (one posture-attributes call per device, "
                    "and so on) — the N+1 cost that a single scrape-duration figure hides."), 8, 7),
        (panel("Subrequest failures/s by API state", "timeseries",
               [prom_t("sum by (tailscale_subrequest, tailscale_api_state) (rate(tailscale2otel_subrequest_failures_total[%s]))" % RI,
                       legend="{{tailscale_subrequest}} / {{tailscale_api_state}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"), novalue=_SUBREQ_EMPTY,
               desc="A single entity's subrequest failing is non-fatal to the enclosing snapshot, "
                    "so the collector still reports a clean scrape while coverage degrades. A "
                    "scope_denied state here is a missing OAuth scope, not a transient error."), 8, 7),
        (panel("Subrequest coverage (last pass)", "bargauge",
               [prom_t("min by (tailscale_collector, tailscale_subrequest) (%s)" % lot("tailscale2otel_subrequest_coverage_ratio"),
                       legend="{{tailscale_collector}} / {{tailscale_subrequest}}")],
               unit="percentunit", min_=0, max_=1,
               thresholds=thr([(None, "red"), (0.9, "yellow"), (1, "green")]),
               options=bargauge_opts(), novalue=_SUBREQ_EMPTY,
               desc="Fraction of per-entity subrequests that succeeded on the last pass — a genuine "
                    "ratio, unlike most _ratio metrics here. 1 when nothing was attempted: an empty "
                    "tailnet has complete coverage of the entities it has."), 8, 7),
    ]
    # --- #386: capability matrix + advisory OAuth-scope preflight.
    capability = [
        (panel("Capability state by collector", "table",
               [prom_t("%s == 1" % lot("tailscale2otel_capability_status_ratio", WIN_SLOW), instant=True, fmt="table")],
               novalue=_CAP_EMPTY,
               transformations=[organize(exclude=TBL_NOISE,
                                         rename={"tailscale_collector": "Collector",
                                                 "tailscale_api_state": "State"})],
               desc="The current state of each enabled collector, filtered to the state that is "
                    "set. Read it before believing an empty operational panel: `disabled` means "
                    "the tailnet does not have the feature, `scope_denied` means the credential "
                    "cannot see it. The value is the most operator-relevant state across that "
                    "collector's probed operations, so a partial scope_denied is never masked by "
                    "a sibling success."), 12, 7),
        (panel("OAuth scope preflight by capability", "table",
               [prom_t(lot("tailscale2otel_capability_scope_satisfied_ratio", WIN_SLOW), instant=True, fmt="table")],
               mappings=bool_map("NOT SATISFIED", "satisfied", "red", "green"),
               novalue=_CAP_EMPTY,
               transformations=[organize(exclude=TBL_NOISE,
                                         rename={"tailscale_capability": "Capability",
                                                 "Value": "Scopes"})],
               desc="Advisory STARTUP preflight, not a server answer: it compares the configured "
                    "tailscale.auth.oauth.scopes against a static map of upstream's documented "
                    "requirements, never blocks collection, and a `satisfied` is not a guarantee. "
                    "Emitted only for capabilities whose scope is modelled and only under an OAuth "
                    "client. The authoritative signal is the capability state reaching "
                    "scope_denied."), 12, 7),
        (panel("API operation availability by state", "table",
               [prom_t("%s == 1" % lot("tailscale2otel_api_availability_ratio", WIN_SLOW),
                       instant=True, fmt="table")],
               novalue=_CAP_EMPTY,
               transformations=[organize(exclude=TBL_NOISE,
                                         rename={"tailscale_collector": "Collector",
                                                 "tailscale_api_operation": "Operation",
                                                 "tailscale_api_state": "State"})],
               desc="Per-OPERATION availability, the finer-grained sibling of the capability "
                    "table above: that one reports the most operator-relevant state across a "
                    "collector, this one names which individual API operation is in it. Filtered "
                    "to the state that is set — the full state set is zero-seeded and always "
                    "emitted, so a state that stops occurring reads as 0 rather than vanishing, "
                    "and an unfiltered table would be mostly zeroes. This is the metric the "
                    "descriptions of tailscale.acl.validation.ok and "
                    "tailscale.webhook_endpoints.count point at when they say an under-scoped "
                    "credential emits nothing rather than a zero."), 24, 8),
    ]
    # --- #404: client-side rate-limiter wait, kept OUT of the API-latency panel.
    #
    # api.duration deliberately excludes rate-limiter wait, and the two are different
    # faults: wait means the poller is throttling itself, latency means the tailnet or
    # the network is slow. Charting them as one series makes the first look like the
    # second.
    ratelimit = [
        (panel("Rate-limiter wait p50/p95/p99 by endpoint", "timeseries",
               [prom_t(hq("0.5", "tailscale2otel_api_rate_limit_wait_seconds", by="endpoint"), legend="p50 {{endpoint}}"),
                prom_t(hq("0.95", "tailscale2otel_api_rate_limit_wait_seconds", by="endpoint"), legend="p95 {{endpoint}}", refid="B"),
                prom_t(hq("0.99", "tailscale2otel_api_rate_limit_wait_seconds", by="endpoint"), legend="p99 {{endpoint}}", refid="C")],
               unit="s", custom=ts_custom(), options=ts_opts(placement="right"), novalue=_RL_EMPTY,
               desc="Time a request spent blocked on the CLIENT-side limiter "
                    "(tailscale.http.rate_limit) before its first attempt. A rising distribution "
                    "means the configured limit is throttling the poller: raise rate_limit or "
                    "lengthen collector intervals. Compare against the collector interval — wait "
                    "approaching it means scrapes will start overlapping."), 12, 7),
        (panel("Time waiting vs time on the wire", "timeseries",
               [prom_t("sum(rate(tailscale2otel_api_rate_limit_wait_seconds_sum[%s]))" % RI, legend="rate-limiter wait s/s"),
                prom_t("sum(rate(tailscale2otel_api_duration_seconds_sum[%s]))" % RI, legend="API request time s/s", refid="B")],
               unit="s", custom=ts_custom(), options=ts_opts(), novalue=_RL_EMPTY,
               desc="Seconds spent per second in each budget. The two do not overlap — api.duration "
                    "records genuine API/network plus backoff time and excludes the limiter wait — "
                    "so wait exceeding wire time means the exporter is mostly queueing against its "
                    "own limit rather than waiting on Tailscale."), 6, 7),
        (panel("Requests delayed by the rate limiter", "stat",
               [prom_t("sum(rate(tailscale2otel_api_rate_limit_wait_seconds_count[%s])) / "
                       "clamp_min(sum(rate(tailscale2otel_api_requests_total[%s])), 1)" % (RI, RI))],
               unit="percentunit", min_=0, max_=1,
               thresholds=thr([(None, "green"), (0.25, "yellow"), (0.5, "red")]),
               options=stat_opts(color="background", graph="area"), novalue=_RL_EMPTY,
               desc="Share of API requests that actually blocked. Only requests that waited are "
                    "recorded — a zero-wait request is skipped — so the histogram count is the "
                    "delayed population and the request counter is the whole one."), 6, 7),
        (panel("API operation last-probe age", "table",
               [prom_t("time() - %s" % lot("tailscale2otel_api_last_probe_seconds", WIN_SLOW), instant=True, fmt="table")],
               unit="s", novalue=_PROBE_EMPTY,
               transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                  "service_instance_id", "service_name", "service_namespace"],
                                         rename={"tailscale_collector": "Collector",
                                                 "tailscale_api_operation": "Operation",
                                                 "Value": "Age"})],
               desc="Seconds since each API operation was last probed. A row aging past its "
                    "collector's interval means that operation stopped being exercised, which is "
                    "how a capability state goes stale without anything erroring."), 24, 7),
    ]
    # --- #405: receiver loss and rejections, each paired with its accepted volume.
    recvloss = [
        (panel("Stream records accepted vs skipped/s", "timeseries",
               [prom_t("sum(rate(tailscale_stream_records_total[%s]))" % RI, legend="accepted/s"),
                prom_t("sum by (reason) (rate(tailscale_stream_skipped_total[%s]))" % RI, legend="skipped {{reason}}", refid="B")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"), novalue=_STREAM_EMPTY,
               desc="Skipped records were extracted from an otherwise-valid body and then never "
                    "routed to a processor: `unclassified` matched neither the flow nor the audit "
                    "shape, `unwrap_drop` was a non-object HEC event dropped while unwrapping. "
                    "Read against the accepted rate — a bare skip count says nothing about how "
                    "much of the feed is being lost."), 12, 7),
        (panel("Receiver rejections/s by reason", "timeseries",
               [prom_t("sum by (reason) (rate(tailscale_stream_rejected_total[%s]))" % RI, legend="stream {{reason}}"),
                prom_t("sum by (reason) (rate(tailscale_webhook_rejected_total[%s]))" % RI, legend="webhook {{reason}}", refid="B")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"), novalue=_STREAM_EMPTY,
               desc="WHOLE requests refused before any record was extracted, by bounded reason "
                    "(bad token, no token on a reachable bind, body over cap, bad HMAC). Distinct "
                    "from skipped records, which came out of a request that was accepted."), 12, 7),
        (panel("Webhook request duration p50/p95/p99", "timeseries",
               [prom_t(hq("0.5", "tailscale_webhook_request_duration_seconds"), legend="p50"),
                prom_t(hq("0.95", "tailscale_webhook_request_duration_seconds"), legend="p95", refid="B"),
                prom_t(hq("0.99", "tailscale_webhook_request_duration_seconds"), legend="p99", refid="C")],
               unit="s", custom=ts_custom(), options=ts_opts(), novalue=_WEBHOOK_EMPTY,
               desc="Wall-clock handling time for webhook deliveries. Tailscale retries a delivery "
                    "it considers timed out, so a rising tail here turns into duplicate events "
                    "rather than lost ones."), 12, 7),
        (panel("Webhook events accepted vs duplicates/s", "timeseries",
               [prom_t("sum(rate(tailscale_webhook_events_total[%s]))" % RI, legend="accepted/s"),
                prom_t("sum(rate(tailscale_webhook_duplicates_total[%s]))" % RI, legend="duplicates suppressed/s", refid="B")],
               unit="cps", custom=ts_custom(), options=ts_opts(), novalue=_WEBHOOK_EMPTY,
               desc="Accepted volume for the duration and rejection panels above, alongside the "
                    "redeliveries the dedup failsafe suppressed."), 12, 7),
        (panel("Webhook payload schema drift/s by field", "timeseries",
               [prom_t("sum by (field, status) (rate(tailscale_webhook_schema_drift_total[%s]))" % RI,
                       legend="{{field}} {{status}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(), novalue=_WEBHOOK_EMPTY,
               desc="Webhook event schema observations by field and known status. A field moving "
                    "to an unknown status means Tailscale changed the payload shape: the receiver "
                    "keeps accepting the event, so nothing else here goes red, but whatever the "
                    "drifted field feeds is quietly no longer populated. Read it alongside the "
                    "accepted volume above — drift on a field nobody consumes is noise, drift on "
                    "one that is charted is a silent data loss."), 24, 7),
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
            row("API rate-limiter wait & probe freshness", ratelimit),
            row("Export latency", exportlat, present="has_export_hist"),
            row("Capability & scope preflight", capability),
            row("Per-entity subrequests", subreq),
            row("Object-store ingestion freshness", objstore_fresh),
            row("Object-store ingestion throughput & faults", objstore_flow),
            row("Ingress WAL", wal),
            row("Receiver loss & rejections", recvloss),
            row("Scrape freshness", freshness, present="has_staleness"),
            row("Cardinality & dedup", cardinality), row("Enrichment cache", enrich),
            row("rDNS resolver", rdns, present="has_rdns"),
            row("PII filter status", pii_status, present="has_pii"),
            row("Per-tailnet API errors", pertailnet, present="has_multitailnet"),
            row("Go runtime", runtime), row("Reliability", reliability, present="has_scrape_err"),
            row("Traces & spans", traces), row("Traces & spans (metrics)", traces2)]
