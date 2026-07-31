"""tab_health_internals() — the residue of the old Exporter Diagnostics tab (#526).

That tab carried 83 panels covering every pipeline stage at once, which is exactly
why a reader could not tell from it WHERE a failure had occurred. Overview,
Collection, Ingestion, Delivery and Runtime superseded 69 of those panels, each
organised around one stage.

What is left here fits none of those stages cleanly: caches that sit beside the
pipeline rather than in it (enrichment, reverse DNS), a per-tailnet view that cuts
across collection rather than belonging to it, and tracing, which is a third
signal type rather than a stage. Placing each of these properly is a judgement
about which stage owns it, so they keep a tab of their own rather than being
scattered on the way past — see the consolidation pass in #526.
"""

from builder import (bargauge_opts, lot, organize, panel, prom_t, raw_sentinel, RI, row, sentinel,
                     stat_opts, TBL_NOISE, tempo_t, thr, ts_custom, ts_opts, vmap,
                     WIN_FAST, WIN_SLOW)
from maps import bool_map, BOOL_HEALTHY_ON

_RDNS_EMPTY = ("No reverse-DNS cache series. Requires enrichment.reverse_dns.enabled and "
               "self_observability.enabled.")


def tab_health_internals(scope):
    """Rows the stage tabs did not claim. See the module docstring.

    Both sentinels are TAB-scoped: each is consumed only by a row on this tab, so
    neither belongs on the dashboard. has_multitailnet is the one that would
    otherwise have to stay dashboard-level, and it does not — it gates a ROW here,
    not a tab, and a row can gate on its own tab's variable.
    """
    sentinel("has_rdns", "tailscale_rdns_cache_entries_ratio", scope)
    # Not a plain metric-presence check, so raw_sentinel rather than sentinel: this
    # gates on ">1 distinct tailnet observed", which no single metric's existence
    # answers. Same query tabs/tailnets.py uses on the other dashboard.
    raw_sentinel(
        "has_multitailnet",
        'query_result(count(count by (tailscale_tailnet) '
        '({__name__=~"tailscale_.+", tailscale_tailnet!="", tailscale_tailnet!="-"})) > 1)',
        scope)

    _trace_desc = "Trace panels are empty if tracing.enabled=false."

    cardinality = [
        (panel("Active series by metric (top $topn)", "timeseries",
               [prom_t("topk($topn, max by (metric_name) (tailscale2otel_series_active))", legend="{{metric_name}}")],
               unit="short", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Per-metric active series (cap 10k). Watch the flow families."), 12, 8),
        (panel("Dedup set size", "timeseries", [prom_t("max by (dedup_set) (tailscale2otel_dedup_size_ratio)", legend="{{dedup_set}}")],
               unit="short", custom=ts_custom(), options=ts_opts(),
               desc='Entries currently held in each cross-source dedup set. The set is a bounded FIFO failsafe against double-counting when a log type is ingested by both a poller and a receiver; a set sitting at its cap is evicting, so read the eviction rate beside it.'), 6, 8),
        (panel("Dedup evictions/s", "timeseries",
               [prom_t("sum by (dedup_set) (rate(tailscale2otel_dedup_evictions_total[%s]))" % RI, legend="{{dedup_set}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc='Evictions from each dedup set per second. Sustained evictions mean the set is too small for the ingest rate, so a duplicate arriving later than the window is no longer recognised and gets counted twice.'), 6, 8),
    ]

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
    # --- WU9 F: PII filter status metadata (present="has_pii"; NOT PII-gated — this is metadata about pii).

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
    # #526: this tab was 83 panels covering every pipeline stage at once. Overview,
    # Collection, Ingestion, Delivery and Runtime superseded 69 of them, organised by
    # WHERE a failure actually occurs. What is left here is the residue that fits none
    # of those stages cleanly — enrichment caches, reverse DNS, per-tailnet API errors
    # and tracing. Distributing these into the stage tabs is a consolidation-pass
    # decision (each needs a judgement about which stage it belongs to), not a
    # mechanical move, so they keep a tab of their own for now.
    return [row("Cardinality & dedup", cardinality),
            row("Enrichment cache", enrich),
            row("rDNS resolver", rdns, present="has_rdns"),
            row("Per-tailnet API errors", pertailnet, present="has_multitailnet"),
            row("Traces & spans", traces),
            row("Traces & spans (metrics)", traces2)]

    return [row("Cardinality & dedup", cardinality),
            row("Enrichment cache", enrich),
            row("rDNS resolver", rdns, present="has_rdns"),
            row("Per-tailnet API errors", pertailnet, present="has_multitailnet"),
            row("Traces & spans", traces),
            row("Traces & spans (metrics)", traces2)]
