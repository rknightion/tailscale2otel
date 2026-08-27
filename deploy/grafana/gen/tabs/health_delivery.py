"""tab_health_delivery(scope) — the last pipeline stage: getting data OUT of the
exporter to the backend (#526).

Content moved here from tabs/diagnostics.py (OTLP export health, PII filter
status) and tabs/events.py (SIEM log-shipping delivery health). Both source
modules are unchanged by this move — they still declare their own copies of
the panels below until a separate wiring pass prunes the duplication; see the
dispatcher report for that call. This module is self-contained: it declares
every sentinel its own rows consume, at the tab-scoped `scope` it is handed.

Also the scheduled home for five signals that had NO panel anywhere before
#526 (per the coverage ledger): tailscale2otel.annotation.published/dropped/
degraded, tailscale2otel.export.spans, tailscale2otel.export.diagnostics.suppressed.

#526 second pass (dissolving the 7th "Exporter internals" leaf,
tabs/health_internals.py): "Traces & spans" and "Traces & spans (metrics)" move here in
full, moved verbatim (Tempo queries, unchanged) — tracing is telemetry the exporter SHIPS,
alongside OTLP export and SIEM log shipping, the same delivery stage as the rest of this
tab. Neither row needs a new sentinel: both were already ungated on health_internals.py
(each panel's own desc states the tracing.enabled prerequisite), so they stay ungated here.
"""

from builder import (hq, logs_opts, loki_t, lot, organize, panel, prom_t, RI, row,
                     sentinel, stat_opts, TBL_NOISE, tempo_t, thr, ts_custom, ts_opts,
                     WIN_FAST, WIN_SLOW)
from maps import bool_map

_TRACE_DESC = "Trace panels are empty if tracing.enabled=false."


# Grafana annotations are entirely opt-in (grafana_annotations.url), and unlike
# most optional families here there is no existing sentinel name in
# builder._SENTINEL_ORDER to gate the whole row on — declaring a new name is
# out of scope for this module (builder.py is read-only). Each panel instead
# carries its own empty-state text, the same idiom tabs/diagnostics.py already
# uses for families with no single presence metric to gate on (_OBJ_EMPTY etc).
_ANNOTATION_EMPTY = ("No Grafana annotation activity. Requires grafana_annotations.url "
                     "to be configured.")

_DEGRADED_MAP = bool_map("healthy", "degraded", "green", "red")


def tab_health_delivery(scope):
    # Presence sentinels this tab declares. has_export_hist and has_pii and
    # has_logstream already exist in builder._SENTINEL_ORDER (previously
    # declared dashboard-level by tabs/diagnostics.py / tabs/events.py for the
    # pre-split single dashboard); re-declared here at THIS tab's scope since
    # the health dashboard's Delivery tab is now their only consumer.
    sentinel("has_export_hist", "tailscale2otel_export_duration_seconds_count", scope)
    sentinel("has_pii", "tailscale2otel_pii_filter_category_ratio", scope)
    sentinel("has_logstream", "tailscale_logstream_configured_ratio", scope)

    # --- Annotations: NEW coverage for annotation.published/dropped/degraded.
    annotations = [
        (panel("Annotations published/s by category", "timeseries",
               [prom_t("sum by (category) (rate(tailscale2otel_annotation_published_total[%s]))" % RI,
                       legend="{{category}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"),
               novalue=_ANNOTATION_EMPTY,
               desc="Grafana annotations accepted by the annotations API since process start, by "
                    "category. Only emitted when grafana_annotations.url is set."), 12, 7),
        (panel("Annotations dropped/s by reason", "timeseries",
               [prom_t("sum by (reason) (rate(tailscale2otel_annotation_dropped_total[%s]))" % RI,
                       legend="{{reason}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"), novalue="0",
               desc="Annotations that did not reach Grafana, by bounded reason. `duplicate` is the "
                    "steady state on a snapshot-shaped source (a key re-observed every poll while "
                    "still inside its expiry window) and is not a fault; `queue_full` and "
                    "`local_rate_limited` are this process's own guards; `unauthorized`, "
                    "`rate_limited`, `rejected`, `server_error` and `transport` are Grafana's "
                    "verdict — a climbing `unauthorized` is an expired or under-scoped token."), 12, 7),
        (panel("Annotation delivery degraded", "stat",
               [prom_t("max(%s)" % lot("tailscale2otel_annotation_degraded_ratio", WIN_FAST))],
               mappings=_DEGRADED_MAP, thresholds=thr([(None, "green"), (1, "red")]),
               options=stat_opts(color="background"), novalue=_ANNOTATION_EMPTY,
               desc="Whether the most recent Grafana annotation write failed and has not yet been "
                    "recovered by a later success. The counterpart to the dropped counter: that one "
                    "says how much was lost, this says whether it is still happening."), 24, 5),
    ]

    # --- OTLP export health: latency, outcome, failures, plus the two new
    # coverage signals (export.spans, export.diagnostics.suppressed).
    export = [
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
        (panel("Export failures/s by type", "timeseries",
               [prom_t("sum by (error_type) (rate(tailscale2otel_export_failures_total[%s]))" % RI, legend="{{error_type}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(), novalue="0",
               desc="OTLP export attempts that failed, by error type."), 12, 6),
        (panel("Export spans/s", "timeseries",
               [prom_t("sum(rate(tailscale2otel_export_spans_total[%s]))" % RI, legend="spans/s")],
               unit="cps", custom=ts_custom(), options=ts_opts(), novalue="0",
               desc="Spans handed to the OTLP trace exporter — the trace cost proxy. Counts every "
                    "span per export batch; empty when tracing.enabled=false."), 12, 6),
        (panel("Export diagnostics suppressed/s", "timeseries",
               [prom_t("sum by (signal, error_type) (rate(tailscale2otel_export_diagnostics_suppressed_total[%s]))" % RI,
                       legend="{{signal}} / {{error_type}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(), novalue="0",
               desc="Export-failure diagnostic log lines suppressed during a sustained OTLP outage, "
                    "by signal and error class. Exact — never itself rate-limited, so this is the "
                    "true count of failures happening beneath the log-rate-limit floor."), 24, 6),
    ]

    # --- PII filter status: compliance view, whether each category is emitted
    # or redacted. NOT itself PII-gated — this is metadata ABOUT the filter.
    pii_status = [
        (panel("PII filter status", "table",
               [prom_t("%s" % lot("tailscale2otel_pii_filter_category_ratio", WIN_FAST), instant=True, fmt="table")],
               mappings=bool_map("REDACTED", "emitted", "red", "green"),
               transformations=[organize(exclude=TBL_NOISE,
                                         rename={"category": "Category", "Value": "State"})],
               desc="Compliance view: every category should read 'emitted' (==1) unless "
                    "redacted."), 12, 7),
    ]

    # --- SIEM log shipping (delivery side of tabs/events.py's log streaming
    # row — the ingestion-side receiver/dedup/checkpoint/freshness panels stay
    # with whichever lane owns Ingestion).
    siem = [
        (panel("Streams configured", "stat",
               [prom_t("sum(max by (tailscale_logstream_type) (%s)) or vector(0)" % lot("tailscale_logstream_configured_ratio", WIN_SLOW), instant=True)],
               unit="short", options=stat_opts(color="value"),
               desc="Configuration/network log streams delivering to a SIEM sink."), 4, 6),
        (panel("Last delivery error", "stat",
               [prom_t("max(%s)" % lot("tailscale_logstream_error_ratio", WIN_FAST), instant=True)],
               mappings=bool_map("OK", "ERROR", "green", "red"),
               thresholds=thr([(None, "green"), (1, "red")]), options=stat_opts(color="background"),
               novalue="No delivery status — no configured log stream has reported one.",
               desc="1 if any stream's last delivery reported an error (see the Delivery errors "
                    "log)."), 4, 6),
        (panel("Delivery throughput by type", "timeseries",
               [prom_t("sum by (tailscale_logstream_type) (rate(tailscale_logstream_bytes_sent_total[%s]))" % RI, legend="{{tailscale_logstream_type}}")],
               unit="Bps", custom=ts_custom(), options=ts_opts(),
               desc="Bytes sent to the configured SIEM sink, by log type."), 8, 6),
        (panel("Entries delivered/s by type", "timeseries",
               [prom_t("sum by (tailscale_logstream_type) (rate(tailscale_logstream_entries_sent_total[%s]))" % RI, legend="{{tailscale_logstream_type}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Log entries successfully delivered to the SIEM sink, by log type."), 8, 6),
        (panel("Delivery requests/s by type", "timeseries",
               [prom_t("sum by (tailscale_logstream_type) (rate(tailscale_logstream_requests_total[%s]))" % RI, legend="{{tailscale_logstream_type}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Delivery requests attempted to the sink, per log type. Zero here with a "
                    "configured stream means nothing is being sent at all, which the failure rate "
                    "alone cannot show."), 8, 6),
        (panel("Delivery failure ratio by type", "timeseries",
               [prom_t("sum by (tailscale_logstream_type) (rate(tailscale_logstream_requests_failed_total[%s])) / clamp_min("
                       "sum by (tailscale_logstream_type) (rate(tailscale_logstream_requests_total[%s])), 0.001)" % (RI, RI),
                       legend="{{tailscale_logstream_type}}")],
               unit="percentunit", min_=0, custom=ts_custom(), options=ts_opts(),
               desc="Failed share of attempted delivery requests. The denominator is clamped, so a "
                    "type with no attempts reads 0 rather than dividing by zero."), 8, 6),
        (panel("Failed requests/s by type", "timeseries",
               [prom_t("sum by (tailscale_logstream_type) (rate(tailscale_logstream_requests_failed_total[%s]))" % RI, legend="{{tailscale_logstream_type}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Failed delivery requests to the sink — alert on a sustained rate."), 8, 6),
        (panel("Backpressure: spoofed & max-body/s", "timeseries",
               [prom_t("sum by (tailscale_logstream_type) (rate(tailscale_logstream_spoofed_entries_total[%s]))" % RI, legend="spoofed {{tailscale_logstream_type}}", refid="A"),
                prom_t("sum by (tailscale_logstream_type) (rate(tailscale_logstream_max_body_requests_total[%s]))" % RI, legend="max-body {{tailscale_logstream_type}}", refid="B")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Entries rejected as spoofed and requests that hit the max body size (SIEM "
                    "backpressure)."), 8, 6),
        (panel("Last activity age by type", "table",
               [prom_t("time() - %s" % lot("tailscale_logstream_last_activity_seconds", WIN_SLOW), instant=True, fmt="table")],
               unit="s", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                            "service_instance_id", "service_name", "service_namespace"],
                                                   rename={"tailscale_logstream_type": "Log type", "Value": "Last activity age"})],
               desc="Time since the most recent delivery activity per log type (alert on "
                    "staleness)."), 8, 6),
        (panel("Delivery errors", "logs",
               [loki_t("{service_name=\"tailscale2otel\"} | event_name=`tailscale.logstream.error` |~ `$log_filter`", maxlines=100)],
               options=logs_opts(), desc="Per-stream delivery errors; the error text is the log "
                    "body."), 16, 7),
    ]

    # --- Moved verbatim from tabs/health_internals.py (#526 dissolution).
    traces = [
        (panel("Scrape → API trace waterfall", "traces",
               [tempo_t('{ resource.service.name = "tailscale2otel" && name =~ "scrape.+" }')],
               desc=_TRACE_DESC), 24, 9),
    ]
    traces2 = [
        (panel("Span class activity", "timeseries",
               [tempo_t('{resource.service.name = "tailscale2otel" && '
                        'name =~ "scrape .+|tailscale.api .+|headscale.api .+|nodemetrics.scrape|'
                        'stream.receive|webhook.receive|release.check .+"} | rate() by (name)')],
               custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Bounded discovery rate for every span class emitted by the exporter: "
                    "scheduler scrapes, Tailscale and Headscale API calls, node-metrics HTTP "
                    "scrapes, stream and webhook receivers, and release checks. An absent class "
                    "means that path was idle, disabled, unsupported, or unsampled; it is not "
                    "fabricated as zero."), 24, 7),
        (panel("API p95 by endpoint (traces)", "timeseries",
               [tempo_t('{span.tailscale.endpoint != "" && resource.service.name = "tailscale2otel"} '
                   '| quantile_over_time(duration, 0.95) by (span.tailscale.endpoint)')],
               unit="s", custom=ts_custom(), options=ts_opts(placement="right"), desc=_TRACE_DESC), 12, 7),
        (panel("Scrape cadence by collector (traces)", "timeseries",
               [tempo_t('{name =~ "scrape.+" && resource.service.name = "tailscale2otel"} | rate() by (name)')],
               custom=ts_custom(), options=ts_opts(placement="right"), desc=_TRACE_DESC), 12, 7),
        (panel("stream.receive batch size (traces)", "timeseries",
               [tempo_t('{name = "stream.receive" && resource.service.name = "tailscale2otel"} '
                   '| avg_over_time(span.tailscale.stream.flows) by (resource.service.instance.id)'),
                tempo_t('{name = "stream.receive" && resource.service.name = "tailscale2otel"} '
                   '| avg_over_time(span.http.request.body.size) by (resource.service.instance.id)', refid="B")],
               custom=ts_custom(), options=ts_opts(placement="right"), desc=_TRACE_DESC), 24, 7),
    ]

    return [row("Annotations", annotations),
            row("OTLP export health", export, present="has_export_hist"),
            row("PII filter status", pii_status, present="has_pii"),
            row("SIEM log shipping", siem, present="has_logstream"),
            row("Traces & spans", traces),
            row("Traces & spans (metrics)", traces2)]
