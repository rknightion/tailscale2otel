"""tab_events() — moved out of build.py in the module split."""

from builder import (hq, logs_opts, loki_t, lot, organize, panel, prom_t, RI, row,
                     sentinel, stat_opts, thr, ts_custom, ts_opts, WIN_FAST, WIN_SLOW)
from maps import bool_map, BOOL_HEALTHY_ON

# Cross-link target for the empty states below. builder.panel() hardcodes
# `links: []`, so the panel description is the only markdown surface available.
CFG_DOC = "https://m7kni.io/tailscale2otel/configuration/"

# #393 — tailnet / provider filters on the audit-pipeline panels. Both are real
# per-series metric labels (roadmap item L): a plain selector, no target_info
# join. On Loki they arrive as structured metadata from the same const attrs;
# `=~".*"` (the All value) also matches a record that carries neither, so the
# filter is safe on a single-tailnet deployment.
TNP = 'tailscale_tailnet=~"$tailnet", tailscale2otel_provider=~"$provider"'
LOKI_TN = '{service_name="tailscale2otel"} | tailscale_tailnet=~"$tailnet"'


def sel(metric, extra=""):
    """`<metric>{<tailnet/provider filter>[, <extra>]}` — the filtered selector."""
    return "%s{%s%s}" % (metric, TNP, (", " + extra) if extra else "")


def q_hist(quantile, metric):
    """histogram_quantile over a tailnet-filtered `<metric>_bucket`.

    builder.hq() cannot be reused here: it appends `_bucket` to whatever string
    it is given, so a selector would come out as `metric{...}_bucket`.
    """
    return ("histogram_quantile(%s, sum by (le) (rate(%s[%s])))"
            % (quantile, sel(metric + "_bucket"), RI))


# The state key #393 asks for, written once and appended to every panel in the
# "Audit pipeline state" row so a reader lands on it wherever they look first.
# It states its own limit on purpose: presence cannot separate "switched off"
# from "unsupported" from "never deployed" (#385), so the row names the
# prerequisite and stops, rather than asserting a cause it cannot know.
STATE_KEY = (
    "\n\nReading this row — four states that otherwise look identical:\n\n"
    "- **idle tailnet**: the collector scrape reads 1, the accepted counter has series, and "
    "both range counters read 0. Nothing happened.\n"
    "- **absent Loki data**: the metric counter moved over this range but the Loki counter "
    "is 0. Events were accepted; the log records are not in the queried Loki datasource.\n"
    "- **ingestion failure**: the scrape gauge reads 0, or the failures panel is non-zero. "
    "Records are being attempted and lost.\n"
    "- **audit collection not enabled**: no scrape series and no accepted counter at all. "
    "This one is a floor, not a verdict — presence **cannot** separate a disabled collector "
    "from a provider that does not support the API from a process that was never deployed, "
    "so the empty states name the prerequisite instead of guessing why."
)


def tab_events():
    # Presence sentinels this tab declares (moved from variables.py, #495).
    sentinel("has_stream", "tailscale_stream_records_total")
    sentinel("has_webhook", "tailscale_webhook_events_total")
    sentinel("has_logstream", "tailscale_logstream_configured_ratio")
    sentinel("has_recv_dur", "tailscale_stream_request_duration_seconds_count")
    sentinel("has_ingest", "tailscale2otel_ingest_records_total")

    rates = [
        (panel("Audit events/s by action", "timeseries",
               [prom_t("sum by (tailscale_audit_action) (rate(tailscale_config_audit_events_total[%s]))" % RI, legend="{{tailscale_audit_action}}")],
               unit="cps", custom=ts_custom(stack="normal"), options=ts_opts(placement="right")), 9, 7),
        (panel("Audit events/s by origin", "timeseries",
               [prom_t("sum by (tailscale_audit_origin) (rate(tailscale_config_audit_events_total[%s]))" % RI, legend="{{tailscale_audit_origin}}")],
               unit="cps", custom=ts_custom(stack="normal"), options=ts_opts()), 9, 7),
        (panel("Audit events (range)", "stat",
               [prom_t("sum(increase(tailscale_config_audit_events_total[$__range]))", instant=True)],
               unit="short", options=stat_opts(color="value", graph="none")), 6, 7),
    ]
    # #393 — the four-state distinction. Each panel is one discriminator; the
    # combination is what separates the states, which is why they share a row
    # and why every description carries the same key.
    auditstate = [
        (panel("Audit poll collector", "stat",
               [prom_t("min(%s)" % lot(sel("tailscale2otel_scrape_success_ratio",
                                           'tailscale_collector="auditlogs"'), WIN_FAST))],
               mappings=BOOL_HEALTHY_ON, thresholds=thr([(None, "red"), (1, "green")]),
               options=stat_opts(color="background"),
               # No zero-fill. A manufactured 0 would render "the audit poller is
               # broken" on a tailnet that streams its audit log instead (#385).
               novalue="No auditlogs scrape reported. Prerequisites: collectors.auditlogs.enabled "
                       "with collectors.auditlogs.source of poll or both. A stream, webhook or "
                       "objectstore path reports no scrape here — read the ingestion-path panel. "
                       "See " + CFG_DOC,
               desc="Last poll-path scrape result for the auditlogs collector." + STATE_KEY), 5, 6),
        (panel("Audit events accepted (this range)", "stat",
               [prom_t("sum(increase(%s[$__range]))" % sel("tailscale_config_audit_events_total"),
                       instant=True)],
               unit="short", options=stat_opts(color="value", graph="none"),
               desc="Audit events the exporter accepted over the dashboard time range, from every "
                    "ingestion path. Reads exactly $__range, the same window as its Loki "
                    "counterpart — the two disagreeing on window is the classic false "
                    "\"the data is missing\" report." + STATE_KEY), 5, 6),
        (panel("Audit log lines in Loki (this range)", "stat",
               [loki_t("sum(count_over_time(%s | event_name=`tailscale.config.audit` [$__range]))"
                       % LOKI_TN, instant=True)],
               unit="short", options=stat_opts(color="value", graph="none"), novalue="0",
               desc="Audit log records present in the queried Loki datasource over the same "
                    "$__range window. Materially below the accepted counter means the records "
                    "were accepted but did not land where this dashboard reads them." + STATE_KEY), 5, 6),
        (panel("Audit events by ingestion path", "timeseries",
               [prom_t("sum by (source) (rate(%s[%s]))"
                       % (sel("tailscale2otel_ingest_records_total", 'signal="audit"'), RI),
                       legend="{{source}}")],
               unit="cps", custom=ts_custom(stack="normal"), options=ts_opts(),
               desc="Which path audit records arrived by: poll, stream (the HEC receiver), "
                    "webhook, or objectstore (the S3-compatible configuration-log export). The "
                    "export destination and object names are never emitted — only the path "
                    "label." + STATE_KEY), 9, 6),
        (panel("Audit ingestion failures/s", "timeseries",
               [prom_t("sum by (error_type) (rate(%s[%s]))"
                       % (sel("tailscale2otel_scrape_errors_total",
                              'tailscale_collector="auditlogs"'), RI),
                       legend="poll {{error_type}}", refid="A"),
                prom_t("sum by (reason) (rate(%s[%s]))"
                       % (sel("tailscale_stream_rejected_total"), RI),
                       legend="stream {{reason}}", refid="B"),
                prom_t("sum by (reason) (rate(%s[%s]))"
                       % (sel("tailscale_webhook_rejected_total"), RI),
                       legend="webhook {{reason}}", refid="C")],
               unit="cps", custom=ts_custom(), options=ts_opts(), novalue="0",
               desc="Records the pipeline attempted and lost, by path. Any non-zero series here "
                    "rules out the idle reading: something arrived and did not make it "
                    "through." + STATE_KEY), 24, 6),
    ]
    # #393 — audit pipeline latency. Two distinct delays: how long Tailscale
    # itself deferred the record before logging it, and how long it then took to
    # reach local acceptance. A backlog in the first is upstream and nothing
    # here can shorten it.
    auditlatency = [
        (panel("Tailscale deferred delay (p50/p95/p99)", "timeseries",
               [prom_t(q_hist("0.5", "tailscale_config_audit_deferred_delay_seconds"), legend="p50"),
                prom_t(q_hist("0.95", "tailscale_config_audit_deferred_delay_seconds"), legend="p95"),
                prom_t(q_hist("0.99", "tailscale_config_audit_deferred_delay_seconds"), legend="p99")],
               unit="s", custom=ts_custom(), options=ts_opts(),
               desc="Time Tailscale deferred a configuration-audit record before logging it. "
                    "Emitted only when the record carries both eventTime and deferredAt in "
                    "order, so a quiet panel is not the same as a zero delay."), 12, 7),
        (panel("Local processing delay (p50/p95/p99)", "timeseries",
               [prom_t(q_hist("0.5", "tailscale_config_audit_processing_delay_seconds"), legend="p50"),
                prom_t(q_hist("0.95", "tailscale_config_audit_processing_delay_seconds"), legend="p95"),
                prom_t(q_hist("0.99", "tailscale_config_audit_processing_delay_seconds"), legend="p99")],
               unit="s", custom=ts_custom(), options=ts_opts(),
               desc="Time from the record's deferredAt (or eventTime when it was not deferred) to "
                    "local processor acceptance. Rising here with a flat deferred delay points at "
                    "the poll window or the exporter, not at upstream."), 12, 7),
    ]
    auditdrift = [
        (panel("Audit schema drift", "timeseries",
               [prom_t("sum by (field, status) "
                       "(rate(tailscale_config_audit_schema_drift_total[%s]))" % RI,
                       legend="{{field}} / {{status}}")],
               unit="cps", custom=ts_custom(stack="normal"), options=ts_opts(),
               desc="Bounded known/unknown observations for audit action, origin, actor type, "
                    "and target property. Raw unknown values never enter metric labels."), 24, 7),
    ]
    ingest = [
        (panel("Stream records/s by type", "timeseries",
               [prom_t("sum by (type) (rate(tailscale_stream_records_total[%s]))" % RI, legend="records {{type}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Records accepted from the configured stream (HEC) receiver, by record type."), 8, 7),
        (panel("Stream rejected/s by reason", "timeseries",
               [prom_t("sum by (reason) (rate(tailscale_stream_rejected_total[%s]))" % RI, legend="rejected {{reason}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Stream records rejected before decoding, by rejection reason."), 8, 7),
        (panel("Stream decode errors/s", "timeseries",
               [prom_t("sum by (type) (rate(tailscale_stream_decode_errors_total[%s]))" % RI, legend="{{type}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Stream records that failed to decode into a known record type."), 8, 7),
    ]
    webhook = [
        (panel("Webhook events/s by type", "timeseries",
               [prom_t("sum by (tailscale_webhook_type) (rate(tailscale_webhook_events_total[%s]))" % RI, legend="{{tailscale_webhook_type}}")],
               unit="cps", custom=ts_custom(stack="normal"), options=ts_opts(placement="right"),
               desc="Webhook events accepted, by event type."), 12, 7),
        (panel("Webhook rejected/s by reason", "timeseries",
               [prom_t("sum by (reason) (rate(tailscale_webhook_rejected_total[%s]))" % RI, legend="{{reason}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Webhook deliveries rejected (bad signature, unknown event, etc.), by reason."), 12, 7),
    ]
    logstream = [
        (panel("Log stream — $log_event", "logs",
               [loki_t("{service_name=\"tailscale2otel\"} | event_name=~`$log_event` |~ `$log_filter`", maxlines=300)],
               options=logs_opts(), desc="Pick an event type with the Log event variable; filter with Log filter."), 16, 11),
        (panel("Log volume by event", "timeseries",
               [loki_t("sum by (event_name) (count_over_time({service_name=\"tailscale2otel\"} | event_name != `` [$__auto]))", legend="{{event_name}}")],
               unit="cps", custom=ts_custom(stack="normal", fill=30), options=ts_opts(placement="right")), 8, 11),
    ]
    flowlogs = [
        (panel("Flow log stream", "logs",
               [loki_t("{service_name=\"tailscale2otel\"} | event_name=`tailscale.network.flow` |~ `$log_filter`", maxlines=300)],
               options=logs_opts(),
               desc="Raw flow-log lines; filter with the Log filter variable."), 24, 10),
    ]
    posturelogs = [
        (panel("Posture log stream", "logs",
               [loki_t("{service_name=\"tailscale2otel\"} | event_name=`tailscale.device.posture` |~ `$log_filter`", maxlines=200)],
               options=logs_opts(),
               desc="Raw device-posture log lines; filter with the Log filter variable."), 24, 9),
    ]
    streamhealth = [
        (panel("Streams configured", "stat",
               [prom_t("sum(max by (tailscale_logstream_type) (%s)) or vector(0)" % lot("tailscale_logstream_configured_ratio", WIN_SLOW), instant=True)],
               unit="short", options=stat_opts(color="value"),
               desc="Configuration/network log streams delivering to a SIEM sink."), 4, 6),
        # No zero-fill: the error gauge is emitted only for a CONFIGURED stream, so the
        # zero rendered a green "OK" for log types that deliver nowhere (#385).
        (panel("Last delivery error", "stat",
               [prom_t("max(%s)" % lot("tailscale_logstream_error_ratio", WIN_FAST), instant=True)],
               mappings=bool_map("OK", "ERROR", "green", "red"),
               thresholds=thr([(None, "green"), (1, "red")]), options=stat_opts(color="background"),
               novalue="No delivery status — no configured log stream has reported one.",
               desc="1 if any stream's last delivery reported an error (see the Delivery errors log)."), 4, 6),
        (panel("Delivery throughput by type", "timeseries",
               [prom_t("sum by (tailscale_logstream_type) (rate(tailscale_logstream_bytes_sent_total[%s]))" % RI, legend="{{tailscale_logstream_type}}")],
               unit="Bps", custom=ts_custom(), options=ts_opts(),
               desc="Bytes sent to the configured SIEM sink, by log type."), 8, 6),
        (panel("Entries delivered/s by type", "timeseries",
               [prom_t("sum by (tailscale_logstream_type) (rate(tailscale_logstream_entries_sent_total[%s]))" % RI, legend="{{tailscale_logstream_type}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Log entries successfully delivered to the SIEM sink, by log type."), 8, 6),
        # #403 — log-stream request health. requests_total is the denominator the
        # already-charted requests_failed_total needs: a flat failure rate means
        # nothing without knowing whether anything was attempted.
        (panel("Delivery requests/s by type", "timeseries",
               [prom_t("sum by (tailscale_logstream_type) (rate(%s[%s]))"
                       % (sel("tailscale_logstream_requests_total"), RI),
                       legend="{{tailscale_logstream_type}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Delivery requests attempted to the sink, per log type. Zero here with a "
                    "configured stream means nothing is being sent at all, which the failure "
                    "rate alone cannot show."), 8, 6),
        (panel("Delivery failure ratio by type", "timeseries",
               [prom_t("sum by (tailscale_logstream_type) (rate(%s[%s])) / clamp_min("
                       "sum by (tailscale_logstream_type) (rate(%s[%s])), 0.001)"
                       % (sel("tailscale_logstream_requests_failed_total"), RI,
                          sel("tailscale_logstream_requests_total"), RI),
                       legend="{{tailscale_logstream_type}}")],
               unit="percentunit", min_=0, custom=ts_custom(), options=ts_opts(),
               desc="Failed share of attempted delivery requests. The denominator is clamped, so "
                    "a type with no attempts reads 0 rather than dividing by zero."), 8, 6),
        (panel("Failed requests/s by type", "timeseries",
               [prom_t("sum by (tailscale_logstream_type) (rate(tailscale_logstream_requests_failed_total[%s]))" % RI, legend="{{tailscale_logstream_type}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Failed delivery requests to the sink — alert on a sustained rate."), 8, 6),
        (panel("Backpressure: spoofed & max-body/s", "timeseries",
               [prom_t("sum by (tailscale_logstream_type) (rate(tailscale_logstream_spoofed_entries_total[%s]))" % RI, legend="spoofed {{tailscale_logstream_type}}", refid="A"),
                prom_t("sum by (tailscale_logstream_type) (rate(tailscale_logstream_max_body_requests_total[%s]))" % RI, legend="max-body {{tailscale_logstream_type}}", refid="B")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Entries rejected as spoofed and requests that hit the max body size (SIEM backpressure)."), 8, 6),
        (panel("Last activity age by type", "table",
               [prom_t("time() - %s" % lot("tailscale_logstream_last_activity_seconds", WIN_SLOW), instant=True, fmt="table")],
               unit="s", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                            "service_instance_id", "service_name", "service_namespace"],
                                                   rename={"tailscale_logstream_type": "Log type", "Value": "Last activity age"})],
               desc="Time since the most recent delivery activity per log type (alert on staleness)."), 8, 6),
        (panel("Delivery errors", "logs",
               [loki_t("{service_name=\"tailscale2otel\"} | event_name=`tailscale.logstream.error` |~ `$log_filter`", maxlines=100)],
               options=logs_opts(), desc="Per-stream delivery errors; the error text is the log body."), 16, 7),
    ]
    receiver = [
        (panel("Receiver in-flight", "timeseries",
               [prom_t("tailscale_stream_inflight", legend="stream"),
                prom_t("tailscale_webhook_inflight", legend="webhook")],
               unit="short", custom=ts_custom(), options=ts_opts(),
               desc="Requests currently being handled by the stream/webhook receivers — a "
                    "sustained non-zero value means the receiver is backed up."), 8, 7),
        (panel("Receiver latency p50/p95/p99 (stream)", "timeseries",
               [prom_t(hq("0.5", "tailscale_stream_request_duration_seconds"), legend="p50"),
                prom_t(hq("0.95", "tailscale_stream_request_duration_seconds"), legend="p95"),
                prom_t(hq("0.99", "tailscale_stream_request_duration_seconds"), legend="p99")],
               unit="s", custom=ts_custom(), options=ts_opts(),
               desc="Stream (HEC) receiver request-handling latency quantiles."), 8, 7),
        (panel("Receiver rejected/s", "timeseries",
               [prom_t("sum by (reason) (rate(tailscale_stream_rejected_total[%s]))" % RI, legend="stream {{reason}}"),
                prom_t("sum by (reason) (rate(tailscale_webhook_rejected_total[%s]))" % RI, legend="webhook {{reason}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(), novalue="0",
               desc="Requests rejected by either receiver, by reason — combines stream and webhook."), 8, 7),
    ]
    ingestvol = [
        (panel("Ingest records/s by source+signal", "timeseries",
               [prom_t("sum by (source, signal) (rate(tailscale2otel_ingest_records_total[%s]))" % RI, legend="{{source}}/{{signal}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Records accepted across every ingestion path, by source and signal type."), 12, 7),
        (panel("Ingest decoded bytes/s by source", "timeseries",
               [prom_t("sum by (source) (rate(tailscale2otel_ingest_size_bytes_total[%s]))" % RI, legend="{{source}}")],
               unit="Bps", custom=ts_custom(), options=ts_opts(),
               desc="Decoded payload bytes accepted across every ingestion path, by source."), 12, 7),
        # The three ingestion paths count rejections under three differently-named
        # metrics, so until now they could not be read on one axis against the
        # accepted volume directly above. The label_replace + `or` union is what
        # puts them on one footing; `+` would be a one-to-one join that silently
        # drops any source present in only one operand, which is the normal case
        # here because each receiver is independently optional.
        (panel("Rejected/s by ingestion source", "timeseries",
               [prom_t('sum by (source) ('
                       'label_replace(rate(tailscale_stream_rejected_total[%(w)s]), "source", "stream", "", "") or '
                       'label_replace(rate(tailscale_webhook_rejected_total[%(w)s]), "source", "webhook", "", "") or '
                       'label_replace(rate(tailscale2otel_objectstore_skipped_total[%(w)s]), "source", "objectstore", "", "")'
                       ')' % {"w": RI}, legend="{{source}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Rejections and skips from every ingestion path on one axis, to read against "
                    "accepted volume above. A source missing here is a source that has never "
                    "rejected anything, not one that is failing silently — the receivers are "
                    "independently optional. Object-store contributes its skipped-object count, "
                    "which includes per-cycle budget skips as well as faults, so read it alongside "
                    "the by-reason breakdown on Exporter Diagnostics rather than as pure loss."), 12, 7),
    ]
    ingestfresh = [
        (panel("Accepted event freshness", "timeseries",
               [prom_t("clamp_min(time() - max by (source, signal) "
                       "(last_over_time(tailscale2otel_ingest_last_event_timestamp_seconds[30d])), 0)",
                       legend="{{source}}/{{signal}}")],
               unit="s", custom=ts_custom(), options=ts_opts(),
               desc="Seconds since the greatest event timestamp accepted from each source/signal. "
                    "Unlike receiver liveness, this exposes stale-but-still-running ingestion."), 6, 7),
        (panel("Accepted event age p95", "timeseries",
               [prom_t("histogram_quantile(0.95, sum by (le, source, signal) "
                       "(rate(tailscale2otel_ingest_event_age_seconds_bucket[%s])))" % RI,
                       legend="{{source}}/{{signal}}")],
               unit="s", custom=ts_custom(), options=ts_opts(),
               desc="p95 age at acceptance. Backfills and retries raise this without moving the "
                    "last-event timestamp backwards."), 6, 7),
        (panel("Capture delay p95", "timeseries",
               [prom_t("histogram_quantile(0.95, sum by (le, source, signal) "
                       "(rate(tailscale2otel_ingest_capture_delay_seconds_bucket[%s])))" % RI,
                       legend="{{source}}/{{signal}}")],
               unit="s", custom=ts_custom(), options=ts_opts(),
               desc="p95 upstream capture/observation delay where the wire format supplies a "
                    "capture timestamp separately from event time."), 6, 7),
        (panel("Timestamp skew/s", "timeseries",
               [prom_t("sum by (source, signal) "
                       "(rate(tailscale2otel_ingest_timestamp_skew_total[%s]))" % RI,
                       legend="{{source}}/{{signal}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(), novalue="0",
               desc="Events whose timestamp is later than local acceptance, or whose capture "
                    "timestamp precedes event time. Negative derived durations are clamped to zero."), 6, 7),
    ]
    dedup = [
        (panel("Dedup hits/s", "stat",
               [prom_t("sum by (dedup_set) (rate(tailscale2otel_dedup_hits_total[%s]))" % RI, legend="{{dedup_set}}")],
               unit="cps", options=stat_opts(color="value"),
               desc="Duplicate records suppressed by the cross-source dedup failsafe, per set."), 6, 7),
        (panel("Dedup set fill", "timeseries",
               [prom_t("max by (dedup_set) (tailscale2otel_dedup_size_ratio)", legend="{{dedup_set}}")],
               unit="short", custom=ts_custom(), options=ts_opts(),
               desc="Keys currently held in each bounded dedup set (a count, despite the metric's "
                    "`_ratio` suffix)."), 9, 7),
        (panel("Dedup evictions/s", "timeseries",
               [prom_t("sum by (dedup_set) (rate(tailscale2otel_dedup_evictions_total[%s]))" % RI, legend="{{dedup_set}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Entries evicted from a dedup set on capacity pressure, per set."), 9, 7),
    ]
    return [row("Audit & event rates", rates),
            # #393 — ungated on purpose: the row's whole job is to tell "no data"
            # apart from "no collector", and a presence gate would hide the answer
            # in exactly the case an operator opened the tab to diagnose.
            row("Audit pipeline state", auditstate),
            row("Audit pipeline latency", auditlatency, present="has_audit"),
            row("Audit schema drift", auditdrift, present="has_audit"),
            row("Stream ingestion", ingest, present="has_stream"),
            row("Log streaming delivery (SIEM)", streamhealth, present="has_logstream"),
            row("Webhooks", webhook, present="has_webhook"),
            row("Receiver health", receiver, present="has_recv_dur"),
            row("Ingestion volume", ingestvol, present="has_ingest"),
            row("Accepted-data freshness", ingestfresh, present="has_ingest"),
            row("Dedup effectiveness", dedup, present="has_selfobs"),
            row("Log explorer", logstream),
            row("Flow logs", flowlogs, present="has_flows"), row("Posture logs", posturelogs, present="has_posture")]
