"""tab_security_audit_trail() — the "Audit Trail" leaf of Security & Audit (#526).

Wave 2 split of the old 49-panel tabs/security.py leaf into four sub-tabs. This one owns
the configuration-audit trail: what changed, who changed it, and the raw log explorer.

Content is COPIED from tabs/security.py (rows "Audit changes", "Configuration audit",
"Configuration audit — actors", "Audit correlation", "Audit action breakdown") and from
tabs/events.py (the "Log explorer" row) — #526 decision 9 dissolves the Events & Logs tab
and sends the log explorer to the domain tab that owns it, which is the audit trail.

Consolidation (#526 decision 7): the two single-panel rows are gone.
  * "Audit action breakdown" merged into "Configuration audit" — identical gate
    (present="has_audit"), so the merge is semantics-preserving.
  * "Audit correlation" merged into the actor row, which is renamed
    "Configuration audit — actors & correlation". Both carried hide_when=["pii_actor"];
    the correlation panel's own present gate moves from "has_audit_changes" to
    "has_audit". That is deliberate and it is the safer direction: the panel exists to
    show the change COUNTER diverging from the Loki event stream, and the case where the
    counter family is absent entirely is exactly the "missing ingestion path" reading it
    was built to surface. Gating it on the metric it is meant to catch the absence of
    would hide the answer.

Sentinel scoping (#526): all three are declared HERE at TAB scope. has_audit and
has_audit_changes and pii_actor were declared at DASHBOARD scope by tabs/security.py,
which the wiring pass deletes; a row gating on a sentinel nothing declares renders
PERMANENTLY hidden and is indistinguishable on screen from a correctly-gated empty row.
"""

from builder import (barchart_opts, logs_opts, loki_t, organize, panel, PII, pii_sentinel,
                     prom_t, RI, row, sentinel, stat_opts, thr, ts_custom, ts_opts)


def tab_security_audit_trail(scope):
    sentinel("has_audit", "tailscale_config_audit_events_total", scope)
    sentinel("has_audit_changes", "tailscale_config_audit_changes_total", scope)
    pii_sentinel("pii_actor",
                 '(%s{category="emails"} == 0) and ignoring(category) (%s{category="user_display_names"} == 0)'
                 % (PII, PII), scope)

    AUD = "{service_name=\"tailscale2otel\"} | event_name=`tailscale.config.audit`"

    # -----------------------------------------------------------------------
    # Audit changes (from tabs/security.py, unchanged)
    # -----------------------------------------------------------------------
    changes = [
        (panel("Security/lifecycle changes/s", "timeseries",
               [prom_t("sum by (tailscale_audit_change, tailscale_audit_action) "
                       "(rate(tailscale_config_audit_changes_total[%s]))" % RI,
                       legend="{{tailscale_audit_change}}/{{tailscale_audit_action}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Rate of audit change events by change kind and action."), 12, 7),
        (panel("Device churn", "timeseries",
               [prom_t('sum by (tailscale_audit_action) '
                       '(rate(tailscale_config_audit_changes_total{tailscale_audit_change="device"}[%s]))' % RI,
                       legend="{{tailscale_audit_action}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Device add/remove/update rate over time."), 12, 7),
        (panel("Changes by actor type", "barchart",
               [prom_t("sum by (tailscale_actor_type) "
                       "(increase(tailscale_config_audit_changes_total[$__range]))",
                       legend="{{tailscale_actor_type}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Total change events in the selected range, broken out by actor type (user/api-key/etc)."), 8, 7),
    ]

    # -----------------------------------------------------------------------
    # Configuration audit (aggregate, no actor identity) + the merged
    # "Audit action breakdown" single-panel row.
    # -----------------------------------------------------------------------
    audit = [
        (panel("Audit actions over time", "timeseries",
               [loki_t("sum by (tailscale_audit_action) (count_over_time(%s [$__auto]))" % AUD,
                       legend="{{tailscale_audit_action}}")],
               unit="cps", custom=ts_custom(stack="normal", fill=30), options=ts_opts(placement="right"),
               desc="Config-audit events over time, by action (create/update/delete/...)."), 12, 7),
        (panel("Audit events (range)", "stat",
               [loki_t("sum(count_over_time(%s [$__range]))" % AUD, instant=True)],
               unit="short", novalue="0", options=stat_opts(color="value"),
               desc="Total config-audit events over the selected dashboard time range."), 6, 7),
        (panel("Failed changes — WARN (range)", "stat",
               # severity field is severity_text (value "INFO"/"WARN"), verified live — NOT `severity`.
               # novalue="0": LogQL count_over_time over an empty match yields no series (not 0),
               # so show 0 rather than "No data" on a healthy tailnet with no WARN audits.
               [loki_t("sum(count_over_time(%s | severity_text=`WARN` [$__range]))" % AUD, instant=True)],
               unit="short", novalue="0", thresholds=thr([(None, "green"), (1, "red")]),
               options=stat_opts(color="background"),
               desc="Audit events emitted at WARN (the event carried an error)."), 6, 7),
        (panel("Audit events by target type", "timeseries",
               [loki_t("sum by (tailscale_target_type) "
                       "(count_over_time(%s | tailscale_target_type != `` [$__auto]))" % AUD,
                       legend="{{tailscale_target_type}}")],
               unit="cps", custom=ts_custom(stack="normal"), options=ts_opts(),
               desc="Config-audit events over time, by the kind of object changed."), 24, 7),
        # merged in from the former single-panel "Audit action breakdown" row.
        (panel("Audit action breakdown (logs)", "timeseries",
               [loki_t("sum by (tailscale_audit_action, tailscale_target_type) "
                       "(count_over_time(%s [$__auto]))" % AUD,
                       legend="{{tailscale_audit_action}}/{{tailscale_target_type}}")],
               unit="cps", novalue="0", custom=ts_custom(stack="normal"), options=ts_opts(placement="right"),
               desc="Audit log action/target-type breakdown — no actor identity, safe to show when PII redaction is active."), 24, 7),
    ]

    # -----------------------------------------------------------------------
    # Actor/identity panels — hidden when pii_actor redaction is active (actor
    # login and actor emails in log bodies are PII). The former single-panel
    # "Audit correlation" row is merged in here (see module docstring).
    # -----------------------------------------------------------------------
    audit_actors = [
        # Rendered as timeseries, not barchart — this dashboard has no Loki barchart
        # precedent (all barcharts are Prometheus instant+table); the proven Loki
        # aggregation pattern here is the range timeseries (see "Log volume by event").
        (panel("Top $topn actors over time", "timeseries",
               [loki_t("topk($topn, sum by (user_name) "
                       "(count_over_time(%s | user_name != `` [$__auto])))" % AUD,
                       legend="{{user_name}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Top-N config-change actors by event count. Hidden on explicit PII redaction "
                    "of actor identity."), 12, 8),
        (panel("Top $topn targets over time", "timeseries",
               [loki_t("topk($topn, sum by (tailscale_target_name) "
                       "(count_over_time(%s | tailscale_target_name != `` [$__auto])))" % AUD,
                       legend="{{tailscale_target_name}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Top-N config-change targets by event count. Hidden on explicit PII redaction "
                    "of actor identity."), 12, 8),
        (panel("Audit: metric vs log", "timeseries",
               [prom_t("sum by (tailscale_audit_change, tailscale_audit_action) "
                       "(rate(tailscale_config_audit_changes_total[%s]))" % RI,
                       legend="metric {{tailscale_audit_change}}/{{tailscale_audit_action}}"),
                loki_t("sum(rate(%s [%s]))" % (AUD, RI),
                       legend="log events")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               novalue="0",
               desc="Classified security- and lifecycle-relevant change counters vs the full Loki "
                    "audit log event rate. The populations are not expected to match; use divergence "
                    "from the established baseline to investigate a missing ingestion path."), 24, 8),
        (panel("Recent configuration changes", "logs",
               [loki_t("%s |~ `$log_filter`" % AUD, maxlines=200)],
               options=logs_opts(), desc="Live audit stream; filter with the Log filter variable."), 24, 10),
    ]

    # -----------------------------------------------------------------------
    # Log explorer — moved verbatim from tabs/events.py (#526 decision 9).
    # Ungated on purpose there and here: its job is to let an operator look at
    # ANY event type, including on a tailnet where nothing else on this tab has
    # data, so a presence gate would hide it in exactly the diagnostic case.
    # -----------------------------------------------------------------------
    logstream = [
        (panel("Log stream — $log_event", "logs",
               [loki_t("{service_name=\"tailscale2otel\"} | event_name=~`$log_event` |~ `$log_filter`", maxlines=300)],
               options=logs_opts(),
               desc="Pick an event type with the Log event variable; filter with Log filter."), 16, 11),
        (panel("Log volume by event", "timeseries",
               [loki_t("sum by (event_name) (count_over_time({service_name=\"tailscale2otel\"} | event_name != `` [$__auto]))",
                       legend="{{event_name}}")],
               unit="cps", custom=ts_custom(stack="normal", fill=30), options=ts_opts(placement="right"),
               desc="Volume of every exporter log event by event_name — the denominator for the "
                    "stream beside it, and the fastest way to see a signal stop being emitted."), 8, 11),
    ]

    return [
        row("Audit changes", changes, present="has_audit_changes"),
        row("Configuration audit", audit, present="has_audit"),
        # Collapsed: actor correlation is follow-up investigation after the audit change view.
        row("Configuration audit — actors & correlation", audit_actors,
            present="has_audit", hide_when=["pii_actor"], collapse=True),
        # Collapsed: raw logs are evidence, not first-open status.
        row("Log explorer", logstream, collapse=True),
    ]
