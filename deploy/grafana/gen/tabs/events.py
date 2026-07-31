"""tab_events() — moved out of build.py in the module split."""

from builder import (hq, logs_opts, loki_t, lot, organize, panel, prom_t, RI, row,
                     sentinel, stat_opts, thr, ts_custom, ts_opts, WIN_FAST, WIN_SLOW)
from maps import bool_map, BOOL_HEALTHY_ON
from builder import DASHBOARD  # #526: wave 1 leaves every sentinel dashboard-level

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


def tab_events(scope):
    # Presence sentinels this tab declares (moved from variables.py, #495).
    sentinel("has_webhook", "tailscale_webhook_events_total", DASHBOARD)

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
    # #526 decision 9 moved the exporter-pipeline rows to the health dashboard:
    # stream/webhook INTAKE, receiver health, ingestion volume, accepted-data
    # freshness and dedup effectiveness (-> health/Ingestion), and SIEM log delivery
    # (-> health/Delivery). They described whether the EXPORTER was working, on a tab
    # an operator opens to read what happened on their TAILNET — and a panel that
    # answers a different question than the tab it sits on is one an operator learns
    # to scroll past.
    #
    # What stays is what a tailnet event actually is: the audit trail, the webhook
    # events themselves, the log explorer, and the per-signal log streams.
    return [row("Audit & event rates", rates),
            # #393 — ungated on purpose: the row's whole job is to tell "no data"
            # apart from "no collector", and a presence gate would hide the answer
            # in exactly the case an operator opened the tab to diagnose.
            row("Audit pipeline state", auditstate),
            row("Audit pipeline latency", auditlatency, present="has_audit"),
            row("Audit schema drift", auditdrift, present="has_audit"),
            row("Webhooks", webhook, present="has_webhook"),
            row("Log explorer", logstream),
            row("Flow logs", flowlogs, present="has_flows"),
            row("Posture logs", posturelogs, present="has_posture")]
