"""tab_health_overview() — the tailscale2otel-health dashboard's "Overview" tab (#526).

Golden signals for the EXPORTER itself: is it up, is it collecting from the Tailscale
API, is it delivering to the OTLP/log backend, and — when something is wrong — a
degradation summary naming what. This is the first tab an operator opens when a panel
on the companion tailscale2otel-tailnet dashboard is empty and they need to know
"quiet tailnet, or broken exporter?" (the health dashboard's whole reason to exist —
see dashboards.py's HEALTH.description).

Every panel here is copied from tabs/diagnostics.py (which this tab, alongside its
four pipeline-stage siblings, supersedes on the health dashboard — see the wave-2
issue comment on #526) rather than invented fresh: queries, units, thresholds and
descriptions are kept as-is unless noted. Detail belongs on Collection/Ingestion/
Delivery/Runtime/Cost & Cardinality — this tab stays a summary.
"""

from builder import (autogrid_row, hq, lot, organize, panel, prom_t, RI, row,
                     sentinel, stat_opts, TBL_NOISE, thr, ts_custom, ts_opts, WIN_SLOW)
from maps import BOOL_HEALTHY_ON, UP_MAP


# Panel titles on this tab carry a "(summary)" qualifier where the same panel also
# appears in full on a detail tab (#526). That is not cosmetic: an alert rule links
# to a panel BY TITLE via the __dashboardUid__/__panelId__ annotation pair, and
# deploy/alerts/gen/build_rules.py treats a title matching more than one panel as a
# hard error rather than picking one — because picking one sends an on-call engineer
# to the wrong tab, or since the split, to the wrong dashboard. The summary/detail
# duplication is deliberate (this tab answers "is the exporter healthy?", the detail
# tabs answer "which stage broke?"), so the titles have to differ.


def tab_health_overview(scope):
    # Presence sentinels this tab declares — all tab-scoped (#526): each name below
    # is already in builder._SENTINEL_ORDER from tabs/diagnostics.py's prior use, so
    # no new name is being invented here, only re-declared at this tab's own scope
    # (registration is keyed by (scope, name), so a tab-scoped declaration here does
    # not collide with a same-named declaration any sibling tab makes at ITS scope).
    sentinel("has_selfobs", "tailscale2otel_series_active", scope)
    sentinel("has_export_hist", "tailscale2otel_export_duration_seconds_count", scope)
    sentinel("has_scrape_err", "tailscale2otel_scrape_errors_total", scope)
    sentinel("has_api_retry", "tailscale2otel_api_retries_total", scope)

    # --- "is it up?" ---------------------------------------------------------
    golden = [
        (panel("Exporter up", "stat", [prom_t("max(%s)" % lot("tailscale2otel_up_ratio"))],
               mappings=UP_MAP, thresholds=thr([(None, "red"), (1, "green")]),
               options=stat_opts(color="background"),
               desc="Whether the process itself is reporting alive (tailscale2otel.up)."), 4, 5),
        (panel("Collectors OK", "stat",
               [prom_t("count(%s == 1) or vector(0)" % lot("tailscale2otel_scrape_success_ratio"))],
               unit="short", thresholds=thr([(None, "green")]), options=stat_opts(color="value"),
               desc="Count of enabled collectors whose last scrape succeeded."), 4, 5),
        (panel("Goroutines (summary)", "stat", [prom_t("max(%s)" % lot("tailscale2otel_runtime_goroutines_ratio"))],
               unit="short", options=stat_opts(graph="area"),
               desc="Current goroutine count — a runaway climb usually means a stuck collector "
                    "or a leaking retry loop."), 4, 5),
        (panel("GOMAXPROCS (summary)", "stat", [prom_t("max(%s)" % lot("tailscale2otel_runtime_gomaxprocs_ratio"))],
               unit="short", options=stat_opts(),
               desc="Go scheduler's configured max OS threads — mostly a deployment sanity check."), 4, 5),
        (panel("Build info", "table",
               [prom_t(lot("tailscale2otel_build_info_ratio", WIN_SLOW), instant=True, fmt="table")],
               transformations=[organize(exclude=TBL_NOISE,
                                         rename={"version": "Version", "go_version": "Go version"})],
               desc="Running version / Go toolchain version (label values)."), 8, 5),
    ]

    # --- "is it collecting?" --------------------------------------------------
    collecting = [
        panel("Scrape success by collector (summary)", "timeseries",
              [prom_t("max by (tailscale_collector) (tailscale2otel_scrape_success_ratio)",
                      legend="{{tailscale_collector}}")],
              unit="short", min_=0, max_=1, custom=ts_custom(style="line", fill=10),
              options=ts_opts(placement="right"),
              desc="1/0 success flag per enabled collector's most recent scrape attempt."),
        panel("Last scrape age (summary)", "table",
              [prom_t("time() - %s" % lot("tailscale2otel_scrape_last_timestamp_seconds"),
                      instant=True, fmt="table")],
              unit="s",
              transformations=[organize(exclude=TBL_NOISE,
                                        rename={"tailscale_collector": "Collector", "Value": "Age"})],
              desc="Seconds since each collector's last scrape — a row aging past its configured "
                   "interval means that collector stopped running."),
        panel("API requests/s by status (summary)", "timeseries",
              [prom_t("sum by (http_response_status_code) (rate(tailscale2otel_api_requests_total[%s]))" % RI,
                      legend="{{http_response_status_code}}")],
              unit="reqps", custom=ts_custom(stack="normal"), options=ts_opts(placement="right"),
              desc="Tailscale API call volume by HTTP status — a rising non-2xx share is the "
                   "exporter losing its ability to collect, before any collector shows it as down."),
    ]

    # --- "is it delivering?" (present="has_export_hist") ---------------------
    delivering = [
        panel("Export outcome rate (summary)", "timeseries",
              [prom_t("sum by (outcome) (rate(tailscale2otel_export_duration_seconds_count[%s]))" % RI,
                      legend="{{outcome}}")],
              unit="cps", custom=ts_custom(), options=ts_opts(),
              desc="OTLP export attempts completed per second, by outcome (success/failure). "
                   "Requires self_observability.enabled — absent otherwise."),
        panel("Export latency by signal (summary)", "timeseries",
              [prom_t(hq("0.5", "tailscale2otel_export_duration_seconds", by="signal"), legend="p50 {{signal}}"),
               prom_t(hq("0.95", "tailscale2otel_export_duration_seconds", by="signal"), legend="p95 {{signal}}", refid="B"),
               prom_t(hq("0.99", "tailscale2otel_export_duration_seconds", by="signal"), legend="p99 {{signal}}", refid="C")],
              unit="s", custom=ts_custom(), options=ts_opts(placement="right"),
              desc="OTLP export latency quantiles, by signal (metrics/logs/traces). Requires "
                   "self_observability.enabled — absent otherwise."),
    ]

    # --- application health (present="has_selfobs") ---------------------------
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
        # No zero-fill: emitCheckpointStats() deliberately emits nothing when the file is
        # absent (in-memory store, or nothing persisted yet), so a zero here would invent
        # a healthy-looking answer out of "not applicable" (#385).
        (panel("Checkpoint disk", "stat", [prom_t("max(tailscale2otel_checkpoint_disk_size_bytes)")],
               unit="bytes", options=stat_opts(),
               novalue="No checkpoint file — the store is not file-backed, or nothing has "
                       "persisted yet.",
               desc="On-disk size of the file-backed checkpoint store."), 4, 5),
        (panel("Process CPU (summary)", "timeseries",
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

    # --- degradation summary (present="has_scrape_err") ------------------------
    #
    # The detail behind each of these lives on the pipeline-stage tabs; here they
    # exist purely so "something is degraded" is visible without leaving Overview.
    degraded = [
        panel("Scrape errors/s", "timeseries",
              [prom_t("sum by (tailscale_collector) (rate(tailscale2otel_scrape_errors_total[%s]))" % RI,
                      legend="{{tailscale_collector}}")],
              unit="cps", custom=ts_custom(), options=ts_opts(),
              desc="Collector scrape attempts that failed, by collector."),
        panel("Checkpoint persist errors/s", "timeseries",
              [prom_t("sum by (tailscale_collector) (rate(tailscale2otel_checkpoint_persist_errors_total[%s]))" % RI,
                      legend="{{tailscale_collector}}")],
              unit="cps", custom=ts_custom(), options=ts_opts(),
              desc="Checkpoint-store writes that failed, by collector."),
        panel("Component errors/s", "timeseries",
              [prom_t("sum by (component) (rate(tailscale2otel_component_errors_total[%s]))" % RI, legend="{{component}}")],
              unit="cps", custom=ts_custom(), options=ts_opts(),
              desc="Internal component errors, by component."),
        panel("Admin auth rejected/s", "timeseries",
              [prom_t("sum by (reason) (rate(tailscale2otel_admin_auth_rejected_total[%s]))" % RI, legend="{{reason}}")],
              unit="cps", custom=ts_custom(), options=ts_opts(),
              desc="Admin-endpoint requests rejected by auth, by reason."),
    ]

    # --- API-throttling degradation (present="has_api_retry") ------------------
    api_degraded = [
        (panel("API 429 / retries (summary)", "timeseries",
               [prom_t('sum(rate(tailscale2otel_api_requests_total{http_response_status_code="429"}[%s]))' % RI, legend="429/s"),
                prom_t("sum(rate(tailscale2otel_api_retries_total[%s]))" % RI, legend="retries/s", refid="B")],
               unit="cps", novalue="0", custom=ts_custom(), options=ts_opts(),
               desc="Rate-limit responses and the retries they trigger, fleet-wide."), 12, 7),
    ]

    return [
        row("Golden signals", golden),
        autogrid_row("Collecting", collecting),
        autogrid_row("Delivering", delivering, present="has_export_hist"),
        row("Application health", apphealth, present="has_selfobs"),
        autogrid_row("Degradation summary", degraded, present="has_scrape_err"),
        row("API throttling", api_degraded, present="has_api_retry"),
    ]
