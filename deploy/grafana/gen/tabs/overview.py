"""tab_overview() — moved out of build.py in the module split."""

from builder import (bargauge_opts, hq, lot, organize, panel, prom_t, RI, row,
                     stat_opts, thr, ts_custom, ts_opts, WIN_SLOW)
from maps import bool_map, BOOL_HEALTHY_ON, UP_MAP


def tab_overview():
    health = [
        (panel("Devices online", "stat",
               [prom_t("count(%s == 1) or vector(0)" % lot("tailscale_device_online_ratio"))],
               unit="short", thresholds=thr([(None, "red"), (1, "green")]),
               options=stat_opts(color="background", graph="area"), desc="Devices currently reporting online."), 3, 5),
        (panel("Total devices", "stat",
               [prom_t("count(%s) or vector(0)" % lot("tailscale_device_online_ratio"))],
               unit="short", options=stat_opts(color="value")), 3, 5),
        (panel("Offline", "stat",
               [prom_t("count(%s == 0) or vector(0)" % lot("tailscale_device_online_ratio"))],
               unit="short", options=stat_opts(color="value"), desc="Devices currently offline (normal for laptops/phones)."), 3, 5),
        # No zero-fill: update availability is suppressed entirely when the control plane
        # does not report it (Headscale) or per-device metrics are off, and "0 updates
        # needed" is a different statement from "we were not told" (#385).
        (panel("Updates available", "stat",
               [prom_t("count(%s == 1)" % lot("tailscale_device_update_available_ratio"))],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]), options=stat_opts(color="value"),
               novalue="No device update data — needs the devices collector and a control plane "
                       "that reports update availability."), 3, 5),
        (panel("Users", "stat", [prom_t("sum(max by (tailscale_user_role, tailscale_user_status, tailscale_user_type) (%s)) or vector(0)" % lot("tailscale_users_count_ratio", WIN_SLOW))],
               unit="short", options=stat_opts()), 3, 5),
        (panel("Device keys ≤7d", "stat",
               [prom_t("count((%s - time() < 7*86400) and (%s - time() > 0)) or vector(0)"
                       % (lot("tailscale_device_key_expiry_seconds", WIN_SLOW), lot("tailscale_device_key_expiry_seconds", WIN_SLOW)))],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow"), (3, "red")]),
               options=stat_opts(color="background"), desc="Device node keys expiring within 7 days."), 3, 5),
        (panel("ACL changed", "stat", [prom_t("time() - max(%s)" % lot("tailscale_acl_last_changed_seconds", WIN_SLOW))],
               unit="s", options=stat_opts(graph="none"), desc="Time since the ACL policy last changed."), 3, 5),
        (panel("Flow logging", "stat",
               [prom_t("max(%s)" % lot("tailscale_feature_enabled_ratio{tailscale_feature=\"network_flow_logging\"}", WIN_SLOW))],
               mappings=BOOL_HEALTHY_ON, thresholds=thr([(None, "red"), (1, "green")]),
               options=stat_opts(color="background"), desc="Tailnet network-flow-logging feature state."), 3, 5),
    ]
    exporter = [
        (panel("Exporter up", "stat", [prom_t("max(%s)" % lot("tailscale2otel_up_ratio"))],
               mappings=UP_MAP, thresholds=thr([(None, "red"), (1, "green")]), options=stat_opts(color="background")), 4, 5),
        (panel("Collectors OK", "stat",
               [prom_t("count(%s == 1) or vector(0)" % lot("tailscale2otel_scrape_success_ratio"))],
               unit="short", thresholds=thr([(None, "green")]), options=stat_opts(color="value"),
               desc="Collectors whose last scrape succeeded. Failures show as Scrape errors/s and on the Diagnostics tab."), 4, 5),
        (panel("Scrape errors/s", "stat",
               [prom_t("sum(rate(tailscale2otel_scrape_errors_total[%s])) or vector(0)" % RI)],
               unit="cps", thresholds=thr([(None, "green"), (0.001, "red")]), options=stat_opts(color="background")), 4, 5),
        (panel("Export failures/s", "stat",
               [prom_t("sum(rate(tailscale2otel_export_failures_total[%s])) or vector(0)" % RI)],
               unit="cps", thresholds=thr([(None, "green"), (0.001, "red")]), options=stat_opts(color="background")), 4, 5),
        (panel("Active series (max)", "stat", [prom_t("max(%s)" % lot("tailscale2otel_series_active"))],
               unit="short", thresholds=thr([(None, "green"), (8000, "yellow"), (10000, "red")]),
               options=stat_opts(color="background"), desc="Largest per-metric active series count (cap is 10k)."), 4, 5),
        (panel("Enrich cache devices", "stat", [prom_t("max(%s)" % lot("tailscale2otel_enrich_cache_size_ratio"))],
               unit="short", options=stat_opts(), desc="Devices held in the IP/nodeID→name enrichment cache."), 4, 5),
    ]
    activity = [
        (panel("Network throughput", "timeseries",
               [prom_t("sum(rate(tailscale_network_io_rollup_bytes_total[%s])) or "
                       "sum(rate(tailscale_network_io_bytes_total[%s]))" % (RI, RI), legend="throughput")],
               unit="Bps", custom=ts_custom(fill=20), options=ts_opts(),
               desc="Total flow throughput (rollup if present, else raw)."), 8, 7),
        (panel("Audit & flow events/s", "timeseries",
               [prom_t("sum(rate(tailscale_config_audit_events_total[%s]))" % RI, legend="audit/s"),
                prom_t("sum(rate(tailscale_network_flows_total[%s]))" % RI, legend="flows/s")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 8, 7),
        (panel("Devices online over time", "timeseries",
               [prom_t("count(tailscale_device_online_ratio == 1)", legend="online"),
                prom_t("count(tailscale_device_online_ratio)", legend="total")],
               unit="short", custom=ts_custom(fill=10), options=ts_opts()), 8, 7),
    ]
    capabilities = [
        (panel("Tailnet features", "timeseries", [prom_t("max by (tailscale_feature) (tailscale_feature_enabled_ratio)", legend="{{tailscale_feature}}")],
               unit="short", min_=0, max_=1, custom=ts_custom(style="line", fill=0, points="always"),
               options=ts_opts(placement="right"), desc="Per-feature enabled (1) / disabled (0)."), 12, 6),
        (panel("Tailnet settings", "timeseries", [prom_t("max by (tailscale_setting_name) (tailscale_setting_enabled_ratio)", legend="{{tailscale_setting_name}}")],
               unit="short", min_=0, max_=1, custom=ts_custom(style="line", fill=0, points="always"),
               options=ts_opts(placement="right")), 12, 6),
    ]
    # Step 1: Multi-tailnet / MSP summary row (gated — only visible when >1 tailnet detected)
    msp = [
        (panel("Tailnets observed", "stat",
               [prom_t('count(count by (tailscale_tailnet) '
                       '({__name__=~"tailscale_.+", tailscale_tailnet!="", tailscale_tailnet!="-"})) or vector(1)')],
               unit="short", options=stat_opts(color="value"),
               desc="Number of distinct tailnets observed by this exporter instance."), 3, 5),
        (panel("Tailnets", "table",
               [prom_t('count by (tailscale_tailnet) ({__name__=~"tailscale_.+", tailscale_tailnet=~"$tailnet", tailscale_tailnet!=""})',
                       instant=True, fmt="table")],
               transformations=[organize(
                   exclude=["Time", "__name__", "job", "instance", "service_instance_id",
                            "service_name", "service_namespace"],
                   rename={"tailscale_tailnet": "Tailnet", "Value": "Series"})],
               desc="Active per-tailnet series count (tailscale_tailnet is a real metric label, item L)."), 9, 5),
        (panel("Providers", "table",
               [prom_t('count by (tailscale2otel_provider) ({__name__=~"tailscale.+", tailscale2otel_provider=~"$provider", tailscale2otel_provider!=""})',
                       instant=True, fmt="table")],
               transformations=[organize(
                   exclude=["Time", "__name__", "job", "instance", "service_instance_id",
                            "service_name", "service_namespace"],
                   rename={"tailscale2otel_provider": "Provider", "Value": "Series"})],
               desc="Control-plane provider (tailscale, headscale) and its active series count."), 6, 5),
        (panel("Devices per tailnet", "bargauge",
               [prom_t('count by (tailscale_tailnet) (max by (tailscale_tailnet, host_id) (%s)) or vector(0)'
                       % lot("tailscale_device_online_ratio"),
                       legend="{{tailscale_tailnet}}")],
               unit="short", options=bargauge_opts(),
               desc="Device count per tailnet (all devices visible to the exporter, online or not)."), 6, 5),
    ]
    # Step 2: Golden signals "Service health" row (gated — only when self-obs metrics present)
    golden = [
        (panel("API p95 latency", "stat",
               [prom_t(hq("0.95", "tailscale2otel_api_duration_seconds"), instant=True)],
               unit="s", thresholds=thr([(None, "green"), (1, "yellow"), (5, "red")]),
               options=stat_opts(color="background"),
               desc="95th-percentile Tailscale API request latency."), 3, 5),
        (panel("Export p99 latency", "stat",
               [prom_t(hq("0.99", "tailscale2otel_export_duration_seconds"), instant=True)],
               unit="s", thresholds=thr([(None, "green"), (2, "yellow"), (10, "red")]),
               options=stat_opts(color="background"),
               desc="99th-percentile OTLP export duration."), 3, 5),
        (panel("Scrape budget (max)", "stat",
               [prom_t("max(tailscale2otel_scrape_budget_ratio) or vector(0)", instant=True)],
               unit="percentunit",
               thresholds=thr([(None, "green"), (0.8, "yellow"), (1, "red")]),
               options=stat_opts(color="background"),
               desc="Worst-case fraction of scrape budget consumed across all collectors."), 3, 5),
        # No zero-fill: series.limit is emitted only when a positive cap is configured, so
        # the zero read as "0% of the cap consumed" for a deployment that has no cap (#385).
        (panel("Series headroom", "stat",
               [prom_t("max(tailscale2otel_series_active) / on() group_left() max(tailscale2otel_series_limit)",
                       instant=True)],
               unit="percentunit",
               thresholds=thr([(None, "green"), (0.8, "yellow"), (1, "red")]),
               options=stat_opts(color="background"),
               novalue="No per-metric series cap configured (cardinality.metric_limit unset) — "
                       "nothing to measure headroom against.",
               desc="Fraction of the per-metric series limit consumed (0 = plenty of headroom)."), 3, 5),
        (panel("Export cost (DPM + log rec/s)", "timeseries",
               [prom_t("rate(tailscale2otel_export_datapoints_total[%s])" % RI, legend="datapoints/s"),
                prom_t("rate(tailscale2otel_export_log_records_total[%s])" % RI, legend="logs/s")],
               unit="cps", custom=ts_custom(fill=15), options=ts_opts(),
               desc="Telemetry export volume — datapoints/s and log records/s going to the OTLP backend."), 12, 5),
    ]
    # Step 3: Security scorecard row (gated — only when ACL risk metrics present)
    scorecard = [
        (panel("Unrestricted ACL rules", "stat",
               [prom_t("sum(%(e)s) or vector(0)" % {"e": lot("tailscale_acl_unrestricted_rules_ratio", WIN_SLOW)},
                       instant=True)],
               unit="short",
               thresholds=thr([(None, "green"), (1, "red")]),
               options=stat_opts(color="background"),
               desc="Total ACL rules that grant unrestricted access (wildcard src/dst). Any non-zero value warrants review."), 4, 5),
        (panel("Auto-approved exit nodes", "stat",
               [prom_t('sum(%(e)s) or vector(0)' % {
                   "e": lot('tailscale_acl_autoapprovers_ratio{tailscale_acl_autoapprover_kind="exit_node"}', WIN_SLOW)},
                       instant=True)],
               unit="short",
               thresholds=thr([(None, "green"), (1, "yellow"), (3, "red")]),
               options=stat_opts(color="background"),
               desc="ACL auto-approver entries for exit-node routes. Review whether automatic exit-node approval is intended."), 4, 5),
        # #395: this row is gated on has_acl_risk (acl collector), but this panel's
        # metric comes from the devices collector. Zero-filling here would render a
        # healthy-looking "0" when devices is absent rather than never checked.
        (panel("Unapproved subnet routes", "stat",
               [prom_t("max(%(e)s)" % {"e": lot("tailscale_subnet_routes_unapproved", WIN_SLOW)},
                       instant=True)],
               unit="short",
               thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="background"),
               novalue="No subnet-route data — needs the devices collector "
                       "(collectors.devices) and a control plane that reports approved routes.",
               desc="Subnet routes advertised but not yet approved by an admin."), 4, 5),
        # #395: same gating mismatch as above — devices collector, acl-gated row.
        (panel("Untagged devices", "stat",
               [prom_t("max(%(e)s)" % {"e": lot("tailscale_devices_untagged_ratio")},
                       instant=True)],
               unit="short",
               thresholds=thr([(None, "green"), (1, "yellow"), (5, "red")]),
               options=stat_opts(color="background"),
               novalue="No device-tag data — needs the devices collector (collectors.devices) "
                       "and a control plane that reports device tags.",
               desc="Devices not associated with any ACL tag — harder to audit and apply granular policies to."), 4, 5),
        # #395: same gating mismatch — device-invite data comes from the devices
        # collector, not acl.
        (panel("Pending exit-node shares", "stat",
               [prom_t('sum(%(e)s)' % {
                   "e": lot('tailscale_device_invites_count_ratio{tailscale_device_invite_accepted="false",tailscale_device_invite_allow_exit_node="true"}', WIN_SLOW)},
                       instant=True)],
               unit="short",
               thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="background"),
               novalue="No device-invite data — needs the devices collector "
                       "(collectors.devices.collect_device_invites).",
               desc="Pending device share invitations that grant exit-node access."), 4, 5),
        (panel("SSH wildcard enabled", "stat",
               [prom_t("max(%(e)s) or vector(0)" % {"e": lot("tailscale_acl_ssh_wildcard_ratio", WIN_SLOW)},
                       instant=True)],
               unit="short",
               # Inverse risk: a wildcard SSH rule (1) is the state to act on. The
               # scorecard rates it yellow, so the map is yellow too, not red.
               mappings=bool_map(off_color="green", on_color="yellow"),
               thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="background"),
               desc="Whether the tailnet ACL contains a wildcard SSH rule."), 4, 5),
    ]
    # Step 4: Wire all rows into the return list (keep existing 4 rows + add 3 new)
    return [row("Tailnet health", health), row("Exporter health", exporter),
            row("Activity", activity), row("Capabilities", capabilities),
            row("MSP / multi-tailnet summary", msp, present="has_multitailnet"),
            row("Service health (golden signals)", golden, present="has_selfobs"),
            row("Security scorecard", scorecard, present="has_acl_risk")]
