"""tab_health_collection() — the "Collection" leaf on tailscale2otel-health (#526).

First pipeline stage: getting data OUT of the Tailscale API and the nodes, before it is
processed/ingested (that is the Ingestion tab) or exported over OTLP (Delivery). Content here
is COPIED from tabs/diagnostics.py (the scrape/poll rows, API request + retry + rate-limit-wait
rows, and the capability/scope preflight rows) and from tabs/nodemetrics.py (the full 5-panel
"Scraper health" row — moved here in full per #526 because it measures the *node-metrics
scraper's own health*, not the tailnet's; two summary tiles stay behind on the product Node
Metrics tab, added independently of this module).

Also carries the six catalog signals that had NO panel anywhere before #526's pending-panel
ledger: the Prometheus pull-endpoint scrape family (tailscale2otel.metrics.scrape.{requests,
duration,in_flight,gather_errors}, tailscale2otel.metrics.auth.rejected) and the per-collector
last-scrape-duration gauge (tailscale2otel.scrape.duration, distinct from the
.scrape.duration.histogram already charted in the "Scrape performance" row below).

Sentinel scoping (#526): has_api_retry / has_api_hist / has_export_hist / has_staleness are
declared at DASHBOARD scope by tabs/diagnostics.py (still true for the HEALTH dashboard build —
diagnostics.py is untouched by this module) — referenced here by name only, NOT re-declared,
since a name cannot be registered twice in one build. has_nodemetrics is declared HERE (tab
scope): tabs/nodemetrics.py, which owns that name at DASHBOARD scope, is only imported into the
TAILNET dashboard's build and never runs during the HEALTH build, so nothing else in this
dashboard registers it. The Prometheus pull-endpoint row is deliberately left UNGATED with its
own noValue prose (matching the existing object-store-ingestion idiom in diagnostics.py) rather
than gated on a new sentinel, since the feature has no single always-present prerequisite metric
distinct from the four series it OWNS — gating a row on one of its own row's metrics doesn't
buy anything beyond a good noValue string.

#526 second pass (dissolving the 7th "Exporter internals" leaf, tabs/health_internals.py):
"Enrichment cache", "rDNS resolver" and "Per-tailnet API errors" move here in full. Enrichment
and reverse-DNS both happen while a scrape is pulling and resolving data — the same collection
stage as the rows above — and a per-tailnet API error split is a collection-side signal (which
tailnet's API calls are failing), not an ingestion or delivery concern. `has_rdns` and
`has_multitailnet` move with their rows: both were TAB-scoped on health_internals.py (consumed
only there), so they are declared fresh here at THIS tab's scope rather than referenced —
exactly the same reasoning as has_nodemetrics above.
"""

from builder import (bargauge_opts, hq, lot, organize, panel, prom_t, raw_sentinel, RI, row,
                     sentinel, stat_opts, TBL_NOISE, thr, ts_custom, ts_opts, WIN_FAST, WIN_SLOW)
from maps import bool_map, UP_MAP

_RDNS_EMPTY = ("No reverse-DNS cache series. Requires enrichment.reverse_dns.enabled and "
               "self_observability.enabled.")


_PULL_EMPTY = ("No Prometheus pull-endpoint series. Requires prometheus.enabled and at least "
               "one scrape against the exporter's /metrics endpoint.")


def tab_health_collection(scope):
    # has_nodemetrics: consumed only by the "Scraper health" row below, within this one tab
    # of this one dashboard (tabs/nodemetrics.py's own declaration never runs during the
    # HEALTH build) — so it is tab-scoped here rather than DASHBOARD-scoped.
    sentinel("has_nodemetrics", "tailscale_node_up_ratio", scope)
    # Declared here rather than merely referenced. Before #526 tabs/diagnostics.py
    # declared these at DASHBOARD scope and every health tab rode on that; that module
    # is gone, and the copies on the Overview tab are TAB-scoped, so they are invisible
    # from here. A row gating on a sentinel nothing declares renders permanently hidden
    # — identical, on screen, to a correctly-gated row that simply has no data.
    sentinel("has_staleness", "tailscale2otel_scrape_staleness_seconds", scope)
    sentinel("has_api_retry", "tailscale2otel_api_retries_total", scope)
    sentinel("has_api_hist", "tailscale2otel_api_duration_seconds_count", scope)
    # Moved from tabs/health_internals.py (#526 dissolution) — both were tab-scoped there,
    # consumed only by the row that moves with them, so declared fresh here rather than
    # referenced (see module docstring).
    sentinel("has_rdns", "tailscale_rdns_cache_entries_ratio", scope)
    raw_sentinel(
        "has_multitailnet",
        'query_result(count(count by (tailscale_tailnet) '
        '({__name__=~"tailscale_.+", tailscale_tailnet!="", tailscale_tailnet!="-"})) > 1)',
        scope)

    cf = "{tailscale_collector=~\"$collector\"}"

    # --- Scraper health (moved in full from tabs/nodemetrics.py's "Scraper health" row).
    # This is the node-metrics SCRAPER's own health (can it reach targets, did discovery
    # succeed) — exporter/collection health, not tailnet/node health, hence the move (#526).
    scraper_health = [
        (panel("Node-metrics targets up", "stat",
               [prom_t("count(%s == 1) or vector(0)" % lot("tailscale_node_up_ratio", "15m"))],
               unit="short", thresholds=thr([(None, "red"), (1, "green")]),
               options=stat_opts(color="background"),
               desc="Node-metrics scrape targets currently reachable."), 5, 5),
        (panel("Node-metrics targets total", "stat",
               [prom_t("count(%s) or vector(0)" % lot("tailscale_node_up_ratio", "15m"))],
               unit="short", options=stat_opts(),
               desc="Node-metrics scrape targets configured/discovered, up or down."), 5, 5),
        (panel("Node-metrics discovery OK", "stat",
               [prom_t("max(%s)" % lot("tailscale2otel_nodemetrics_discovery_success_ratio"))],
               mappings=UP_MAP, thresholds=thr([(None, "red"), (1, "green")]),
               options=stat_opts(color="background"),
               desc="Whether the last node-metrics target-discovery pass succeeded."), 5, 5),
        (panel("Node-metrics discovered targets", "stat",
               [prom_t("max(%s)" % lot("tailscale2otel_nodemetrics_discovery_targets"))],
               unit="short", options=stat_opts(),
               desc="Scrape targets found by the last node-metrics discovery pass."), 5, 5),
        (panel("Node-metrics scrape health by target", "table",
               [prom_t(lot("tailscale_node_up_ratio", "15m"), instant=True, fmt="table")],
               transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                  "service_instance_id", "service_name", "service_namespace"],
                                         rename={"tailscale_node": "Node", "Value": "Up"})],
               desc="Per-target node-metrics scrape health (1=up)."), 4, 5),
        (panel("Node-metrics failed scrapes/s by reason", "timeseries",
               [prom_t("sum by (reason) (rate(tailscale2otel_nodemetrics_scrape_failures_total[%s]))" % RI,
                       legend="{{reason}}")],
               unit="cps", novalue="0", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Failed node-metrics scrapes by a closed diagnostic class. `connection_refused` "
                    "means the target accepted no metrics connection; `timeout` means it did not "
                    "answer in time; `missing_endpoint` is HTTP 404; `http_error` is another "
                    "non-2xx response; and `other` covers TLS, auth, parse, and unknown transport "
                    "failures."), 24, 7),
    ]

    # --- Scrape performance by collector (from diagnostics.py's "Collectors" row).
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
        desc="Per-collector scrape wall-clock duration quantiles from the histogram series.")
    scrape_perf = [
        (_scrapedur_p, 12, 7),
        (panel("Last scrape duration by collector", "timeseries",
               [prom_t("max by (tailscale_collector) (%s)" % lot("tailscale2otel_scrape_duration_seconds" + cf),
                       legend="{{tailscale_collector}}")],
               unit="s", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Wall-clock duration of each collector's most recent scrape (gauge, "
                    "distinct from the p50/p95/p99 quantiles derived from the histogram "
                    "series to its left)."), 12, 7),
        (panel("Scrape success by collector", "timeseries",
               [prom_t("max by (tailscale_collector) (tailscale2otel_scrape_success_ratio%s)" % cf, legend="{{tailscale_collector}}")],
               unit="short", min_=0, max_=1, custom=ts_custom(style="line", fill=10), options=ts_opts(placement="right"),
               desc="1 when a collector's last scrape succeeded, 0 when it failed."), 12, 7),
        (panel("Last scrape age", "table",
               [prom_t("time() - %s" % lot("tailscale2otel_scrape_last_timestamp_seconds" + cf), instant=True, fmt="table")],
               unit="s", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                            "service_instance_id", "service_name", "service_namespace"],
                                                   rename={"tailscale_collector": "Collector", "Value": "Age"})],
               desc="Seconds since each collector's last scrape."), 12, 7),
        (panel("Scrape errors/s by collector / type", "timeseries",
               [prom_t("sum by (tailscale_collector, error_type) (rate(tailscale2otel_scrape_errors_total%s[%s]))" % (cf, RI), legend="{{tailscale_collector}} / {{error_type}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Collector scrape attempts that failed, by collector and error type."), 12, 7),
    ]

    # --- Scrape freshness (from diagnostics.py; has_staleness declared there, DASHBOARD scope).
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

    # --- API requests & retries (from diagnostics.py's "API & export" / "API retries..." rows;
    # export-failures panel deliberately left out — that is Delivery-tab content).
    # Consolidation (#526 decision 7): the by-status and by-endpoint breakdowns were two
    # single-purpose rows over the exact same underlying counter and unit; merged into one
    # two-series panel rather than two rows (neither metric loses coverage — both group-bys
    # are still charted, now on one axis instead of two).
    api = [
        (panel("API requests/s by status & endpoint", "timeseries",
               [prom_t("sum by (http_response_status_code) (rate(tailscale2otel_api_requests_total[%s]))" % RI,
                       legend="status {{http_response_status_code}}"),
                prom_t("sum by (endpoint) (rate(tailscale2otel_api_requests_total[%s]))" % RI,
                       legend="endpoint {{endpoint}}", refid="B")],
               unit="reqps", custom=ts_custom(stack="normal"), options=ts_opts(placement="right"),
               desc="Tailscale API requests issued per second, by HTTP status code and by "
                    "endpoint (two independent group-bys over the same counter, on one "
                    "axis)."), 24, 7),
    ]
    api_retry = [
        (panel("API retries/s by endpoint", "timeseries",
               [prom_t("sum by (endpoint) (rate(tailscale2otel_api_retries_total[%s]))" % RI, legend="{{endpoint}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Tailscale API calls retried (429/5xx/timeout), by endpoint."), 24, 6),
    ]

    # --- API latency (from diagnostics.py; has_api_hist declared there, DASHBOARD scope).
    _apilat_p = panel("API latency p50/p95/p99 by endpoint", "timeseries",
                      [prom_t(hq("0.5", "tailscale2otel_api_duration_seconds", by="endpoint"), legend="p50 {{endpoint}}"),
                       prom_t(hq("0.95", "tailscale2otel_api_duration_seconds", by="endpoint"), legend="p95 {{endpoint}}", refid="B"),
                       prom_t(hq("0.99", "tailscale2otel_api_duration_seconds", by="endpoint"), legend="p99 {{endpoint}}", refid="C")],
                      unit="s", custom=ts_custom(), options=ts_opts(placement="right"),
                      desc="Per-endpoint Tailscale API latency quantiles.")
    apilat = [
        (_apilat_p, 12, 7),
        (panel("API 429 / retries", "timeseries",
               [prom_t('sum(rate(tailscale2otel_api_requests_total{http_response_status_code="429"}[%s]))' % RI, legend="429/s"),
                prom_t("sum(rate(tailscale2otel_api_retries_total[%s]))" % RI, legend="retries/s", refid="B")],
               unit="cps", novalue="0", custom=ts_custom(), options=ts_opts(),
               desc="Rate-limit responses and the retries they trigger, fleet-wide."), 12, 7),
    ]

    # --- Rate-limiter wait (from diagnostics.py's "API rate-limiter wait..." row, ungated there).
    ratelimit = [
        (panel("Provider rate-limit utilization by tailnet", "timeseries",
               [prom_t("max by (tailscale_tailnet) (tailscale2otel_api_rate_limit_utilization_ratio)",
                       legend="{{tailscale_tailnet}}")],
               unit="percentunit", min_=0, max_=1,
               thresholds=thr([(None, "green"), (1, "red")]), custom=ts_custom(), options=ts_opts(),
               desc="Provider quota-pressure samples by tailnet: 1 when a logical request observed "
                    "an HTTP 429, including one that later retried successfully; 0 otherwise. This "
                    "is response-derived provider saturation, distinct from client-side limiter wait."), 12, 7),
        (panel("Rate-limiter wait p50/p95/p99 by endpoint", "timeseries",
               [prom_t(hq("0.5", "tailscale2otel_api_rate_limit_wait_seconds", by="endpoint"), legend="p50 {{endpoint}}"),
                prom_t(hq("0.95", "tailscale2otel_api_rate_limit_wait_seconds", by="endpoint"), legend="p95 {{endpoint}}", refid="B"),
                prom_t(hq("0.99", "tailscale2otel_api_rate_limit_wait_seconds", by="endpoint"), legend="p99 {{endpoint}}", refid="C")],
               unit="s", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Time a request spent blocked on the client-side rate limiter "
                    "(tailscale.http.rate_limit) before its first attempt."), 12, 7),
        (panel("Time waiting vs time on the wire", "timeseries",
               [prom_t("sum(rate(tailscale2otel_api_rate_limit_wait_seconds_sum[%s]))" % RI, legend="rate-limiter wait s/s"),
                prom_t("sum(rate(tailscale2otel_api_duration_seconds_sum[%s]))" % RI, legend="API request time s/s", refid="B")],
               unit="s", custom=ts_custom(), options=ts_opts(),
               desc="Seconds spent per second waiting on the client limiter versus actually "
                    "on the wire to the Tailscale API."), 6, 7),
        (panel("Requests delayed by the rate limiter", "stat",
               [prom_t("sum(rate(tailscale2otel_api_rate_limit_wait_seconds_count[%s])) / "
                       "clamp_min(sum(rate(tailscale2otel_api_requests_total[%s])), 1)" % (RI, RI))],
               unit="percentunit", min_=0, max_=1,
               thresholds=thr([(None, "green"), (0.25, "yellow"), (0.5, "red")]),
               options=stat_opts(color="background", graph="area"),
               desc="Share of API requests that actually blocked on the client-side "
                    "rate limiter."), 6, 7),
        (panel("API operation last-probe age", "table",
               [prom_t("time() - %s" % lot("tailscale2otel_api_last_probe_seconds", WIN_SLOW), instant=True, fmt="table")],
               unit="s",
               transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                  "service_instance_id", "service_name", "service_namespace"],
                                         rename={"tailscale_collector": "Collector",
                                                 "tailscale_api_operation": "Operation",
                                                 "Value": "Age"})],
               desc="Seconds since each API operation was last probed; a row aging past its "
                    "collector's interval means that operation stopped being exercised."), 24, 7),
    ]

    # --- Capability & scope preflight (from diagnostics.py, ungated there — relies on noValue).
    _CAP_EMPTY = ("No capability-preflight series. Requires self_observability.enabled; the scope "
                  "preflight additionally requires tailscale.auth.method: oauth, as an API key "
                  "carries no scope list to compare against.")
    capability = [
        (panel("Capability state by collector", "table",
               [prom_t("%s == 1" % lot("tailscale2otel_capability_status_ratio", WIN_SLOW), instant=True, fmt="table")],
               novalue=_CAP_EMPTY,
               transformations=[organize(exclude=TBL_NOISE,
                                         rename={"tailscale_collector": "Collector",
                                                 "tailscale_api_state": "State"})],
               desc="The current state of each enabled collector, filtered to the state that "
                    "is set (disabled/scope_denied/ok)."), 12, 7),
        (panel("OAuth scope preflight by capability", "table",
               [prom_t(lot("tailscale2otel_capability_scope_satisfied_ratio", WIN_SLOW), instant=True, fmt="table")],
               mappings=bool_map("NOT SATISFIED", "satisfied", "red", "green"),
               novalue=_CAP_EMPTY,
               transformations=[organize(exclude=TBL_NOISE,
                                         rename={"tailscale_capability": "Capability",
                                                 "Value": "Scopes"})],
               desc="Advisory startup preflight comparing configured OAuth scopes against "
                    "upstream's documented per-capability requirements."), 12, 7),
        (panel("API operation availability by state", "table",
               [prom_t("%s == 1" % lot("tailscale2otel_api_availability_ratio", WIN_SLOW),
                       instant=True, fmt="table")],
               novalue=_CAP_EMPTY,
               transformations=[organize(exclude=TBL_NOISE,
                                         rename={"tailscale_collector": "Collector",
                                                 "tailscale_api_operation": "Operation",
                                                 "tailscale_api_state": "State"})],
               desc="Per-operation availability, the finer-grained sibling of the capability "
                    "table above — which individual API operation is in a non-ok state."), 24, 8),
    ]

    # --- #526: Prometheus pull endpoint (opt-in scrape target, default :2112). Six new
    # catalog signals with no panel anywhere before this tab: scrape.requests, .duration,
    # .in_flight, .gather_errors, auth.rejected — plus the histogram feeds the duration
    # quantile panel via hq(). Ungated (own noValue prose) rather than a new sentinel: see
    # module docstring.
    pull_dur_p = panel(
        "Pull-endpoint scrape duration p50/p95/p99 by outcome", "timeseries",
        [prom_t(hq("0.5", "tailscale2otel_metrics_scrape_duration_seconds", by="outcome"), legend="p50 {{outcome}}"),
         prom_t(hq("0.95", "tailscale2otel_metrics_scrape_duration_seconds", by="outcome"), legend="p95 {{outcome}}", refid="B"),
         prom_t(hq("0.99", "tailscale2otel_metrics_scrape_duration_seconds", by="outcome"), legend="p99 {{outcome}}", refid="C")],
        unit="s", custom=ts_custom(), options=ts_opts(placement="right"), novalue=_PULL_EMPTY,
        desc="Wall-clock time serving one Prometheus /metrics request (gather + encode + "
             "write), by classified outcome — what the scraper's own timeout races against.")
    pull_endpoint = [
        (panel("Pull-endpoint scrapes/s by outcome", "timeseries",
               [prom_t("sum by (outcome) (rate(tailscale2otel_metrics_scrape_requests_total[%s]))" % RI, legend="{{outcome}}")],
               unit="cps", custom=ts_custom(stack="normal"), options=ts_opts(placement="right"), novalue=_PULL_EMPTY,
               desc="Prometheus /metrics scrapes served after the auth gate, by classified "
                    "outcome (success/unavailable/error). A flat success rate is how a "
                    "scraper that stopped asking is told apart from an endpoint that stopped "
                    "answering."), 12, 7),
        (pull_dur_p, 12, 7),
        (panel("Pull-endpoint requests in flight", "timeseries",
               [prom_t("max(tailscale2otel_metrics_scrape_in_flight)", legend="in-flight")],
               unit="short", custom=ts_custom(), options=ts_opts(), novalue=_PULL_EMPTY,
               desc="Prometheus /metrics requests currently being served. Pinned at "
                    "prometheus.max_requests_in_flight means scrapes are being shed; a value "
                    "that never returns to zero means collection is hanging rather than "
                    "slow."), 8, 6),
        (panel("Pull-endpoint gather errors/s", "timeseries",
               [prom_t("sum(rate(tailscale2otel_metrics_scrape_gather_errors_total[%s]))" % RI, legend="gather errors/s")],
               unit="cps", novalue="0", custom=ts_custom(), options=ts_opts(),
               desc="Prometheus registry Gather errors hit while serving /metrics; the handler "
                    "continues on error and returns 200 with the remaining series, so a "
                    "non-zero rate means series are silently being dropped from every "
                    "scrape."), 8, 6),
        (panel("Pull-endpoint auth rejections/s by reason", "timeseries",
               [prom_t("sum by (reason) (rate(tailscale2otel_metrics_auth_rejected_total[%s]))" % RI, legend="{{reason}}")],
               unit="cps", novalue=_PULL_EMPTY, custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Prometheus /metrics requests refused by the auth gate before being "
                    "counted as a scrape, by reason (missing/bad credentials, or "
                    "auth_required misconfiguration on a network-reachable bind)."), 8, 6),
    ]

    # --- Moved verbatim from tabs/health_internals.py (#526 dissolution).
    enrich = [
        (panel("Enrich cache age", "timeseries", [prom_t("max(tailscale2otel_enrich_cache_age_seconds)", legend="age")],
               unit="s", custom=ts_custom(), options=ts_opts(),
               desc='Age of the device-enrichment cache, which maps IPs and node IDs to device names for flow and audit records. It is populated by the devices collector: if that collector is disabled or failing this climbs, and enrichment silently degrades to unknown/external.'), 12, 6),
        (panel("Enrich cache size", "timeseries", [prom_t("max(tailscale2otel_enrich_cache_size_ratio)", legend="devices")],
               unit="short", custom=ts_custom(), options=ts_opts(),
               desc="Devices held in the enrichment cache. A count well below the tailnet's device count means flow and audit records are resolving to unknown for the remainder."), 12, 6),
    ]

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

    pertailnet = [
        (panel("Per-tailnet API errors", "timeseries",
               [prom_t('sum by (tailscale_tailnet) (rate(tailscale2otel_api_requests_total{http_response_status_code=~"4..|5..", tailscale_tailnet!=""}[%s]))' % RI,
                       legend="{{tailscale_tailnet}}")],
               unit="cps", novalue="0", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Tailscale API 4xx/5xx responses per second, split out per tailnet on a "
                    "multi-tailnet deployment."), 24, 7),
    ]

    return [
        row("Scraper health", scraper_health, present="has_nodemetrics"),
        row("Scrape performance by collector", scrape_perf),
        row("Scrape freshness", freshness, present="has_staleness"),
        row("API requests", api),
        row("API retries", api_retry, present="has_api_retry"),
        row("API latency", apilat, present="has_api_hist"),
        row("API rate-limiter wait & probe freshness", ratelimit),
        row("Capability & scope preflight", capability),
        row("Prometheus pull endpoint", pull_endpoint),
        row("Enrichment cache", enrich),
        row("rDNS resolver", rdns, present="has_rdns"),
        row("Per-tailnet API errors", pertailnet, present="has_multitailnet"),
    ]
