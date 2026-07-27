"""tab_events() — moved out of build.py in the module split."""

from builder import (hq, logs_opts, loki_t, lot, organize, panel, prom_t, RI, row,
                     stat_opts, thr, ts_custom, ts_opts, WIN_FAST, WIN_SLOW)
from maps import bool_map


def tab_events():
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
               unit="cps", custom=ts_custom(), options=ts_opts()), 8, 7),
        (panel("Stream rejected/s by reason", "timeseries",
               [prom_t("sum by (reason) (rate(tailscale_stream_rejected_total[%s]))" % RI, legend="rejected {{reason}}")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 8, 7),
        (panel("Stream decode errors/s", "timeseries",
               [prom_t("sum by (type) (rate(tailscale_stream_decode_errors_total[%s]))" % RI, legend="{{type}}")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 8, 7),
    ]
    webhook = [
        (panel("Webhook events/s by type", "timeseries",
               [prom_t("sum by (tailscale_webhook_type) (rate(tailscale_webhook_events_total[%s]))" % RI, legend="{{tailscale_webhook_type}}")],
               unit="cps", custom=ts_custom(stack="normal"), options=ts_opts(placement="right")), 12, 7),
        (panel("Webhook rejected/s by reason", "timeseries",
               [prom_t("sum by (reason) (rate(tailscale_webhook_rejected_total[%s]))" % RI, legend="{{reason}}")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 12, 7),
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
               options=logs_opts()), 24, 10),
    ]
    posturelogs = [
        (panel("Posture log stream", "logs",
               [loki_t("{service_name=\"tailscale2otel\"} | event_name=`tailscale.device.posture` |~ `$log_filter`", maxlines=200)],
               options=logs_opts()), 24, 9),
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
               [prom_t("sum by (tailscale_logstream_type) (rate(tailscale_logstream_bytes_sent_bytes_total[%s]))" % RI, legend="{{tailscale_logstream_type}}")],
               unit="Bps", custom=ts_custom(), options=ts_opts()), 8, 6),
        (panel("Entries delivered/s by type", "timeseries",
               [prom_t("sum by (tailscale_logstream_type) (rate(tailscale_logstream_entries_sent_total[%s]))" % RI, legend="{{tailscale_logstream_type}}")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 8, 6),
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
               unit="short", custom=ts_custom(), options=ts_opts()), 8, 7),
        (panel("Receiver latency p50/p95/p99 (stream)", "timeseries",
               [prom_t(hq("0.5", "tailscale_stream_request_duration_seconds"), legend="p50"),
                prom_t(hq("0.95", "tailscale_stream_request_duration_seconds"), legend="p95"),
                prom_t(hq("0.99", "tailscale_stream_request_duration_seconds"), legend="p99")],
               unit="s", custom=ts_custom(), options=ts_opts()), 8, 7),
        (panel("Receiver rejected/s", "timeseries",
               [prom_t("sum by (reason) (rate(tailscale_stream_rejected_total[%s]))" % RI, legend="stream {{reason}}"),
                prom_t("sum by (reason) (rate(tailscale_webhook_rejected_total[%s]))" % RI, legend="webhook {{reason}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(), novalue="0"), 8, 7),
    ]
    ingestvol = [
        (panel("Ingest records/s by source+signal", "timeseries",
               [prom_t("sum by (source, signal) (rate(tailscale2otel_ingest_records_total[%s]))" % RI, legend="{{source}}/{{signal}}")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 12, 7),
        (panel("Ingest decoded bytes/s by source", "timeseries",
               [prom_t("sum by (source) (rate(tailscale2otel_ingest_size_bytes_total[%s]))" % RI, legend="{{source}}")],
               unit="Bps", custom=ts_custom(), options=ts_opts()), 12, 7),
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
               unit="cps", options=stat_opts(color="value")), 6, 7),
        (panel("Dedup set fill", "timeseries",
               [prom_t("max by (dedup_set) (tailscale2otel_dedup_size_ratio)", legend="{{dedup_set}}")],
               unit="short", custom=ts_custom(), options=ts_opts()), 9, 7),
        (panel("Dedup evictions/s", "timeseries",
               [prom_t("sum by (dedup_set) (rate(tailscale2otel_dedup_evictions_total[%s]))" % RI, legend="{{dedup_set}}")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 9, 7),
    ]
    return [row("Audit & event rates", rates), row("Audit schema drift", auditdrift, present="has_audit"),
            row("Stream ingestion", ingest, present="has_stream"),
            row("Log streaming delivery (SIEM)", streamhealth, present="has_logstream"),
            row("Webhooks", webhook, present="has_webhook"),
            row("Receiver health", receiver, present="has_recv_dur"),
            row("Ingestion volume", ingestvol, present="has_ingest"),
            row("Accepted-data freshness", ingestfresh, present="has_ingest"),
            row("Dedup effectiveness", dedup, present="has_selfobs"),
            row("Log explorer", logstream),
            row("Flow logs", flowlogs, present="has_flows"), row("Posture logs", posturelogs, present="has_posture")]
