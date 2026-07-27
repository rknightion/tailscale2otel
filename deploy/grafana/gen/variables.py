"""Dashboard template-variable builders (moved out of build.py in the module split)."""

from builder import (PROM_DS_TEXT, PROM_DS_VALUE, LOKI_DS_TEXT, LOKI_DS_VALUE,
                     TEMPO_DS_TEXT, TEMPO_DS_VALUE, PII)


def ds_var(name, label, plugin, text, value):
    return {"kind": "DatasourceVariable", "spec": {
        "name": name, "label": label, "pluginId": plugin,
        "current": {"text": text, "value": value}, "options": [],
        "multi": False, "includeAll": False, "allowCustomValue": True,
        "hide": "dontHide", "refresh": "onDashboardLoad", "regex": "", "skipUrlSync": False}}


def query_var(name, label, query, multi=True, allval=True, hide="dontHide",
              ds="${ds_prometheus}", refresh="onTimeRangeChanged", regex="", sort="alphabeticalAsc"):
    spec = {
        "name": name, "label": label, "hide": hide,
        "query": {"kind": "DataQuery", "version": "v0", "group": "",
                  "datasource": {"name": ds}, "spec": {"query": query, "refId": name}},
        "current": {"text": ("All" if allval else ""), "value": ("$__all" if allval else "")},
        "options": [], "multi": multi, "includeAll": allval, "allowCustomValue": True,
        "refresh": refresh, "regex": regex, "skipUrlSync": False, "sort": sort}
    if allval:
        spec["allValue"] = ".*"  # make $__all expand to match-all even if the renderer can't resolve options
    return {"kind": "QueryVariable", "spec": spec}


def presence_var(name, metric):
    return {"kind": "QueryVariable", "spec": {
        "name": name, "label": name, "hide": "hideVariable",
        "query": {"kind": "DataQuery", "version": "v0", "group": "",
                  "datasource": {"name": "${ds_prometheus}"},
                  "spec": {"query": "label_values(%s, __name__)" % metric, "refId": name}},
        "current": {"text": "", "value": ""}, "options": [], "multi": False,
        "includeAll": False, "allowCustomValue": False, "refresh": "onDashboardLoad",
        "regex": "", "skipUrlSync": True, "sort": "disabled"}}


def pii_var(name, expr):
    """Hidden var: non-empty (matches .+) ONLY when <expr> returns series, i.e. when the
    redaction condition holds. Used with row(hide_when=[...]) -> notMatches so panels hide
    only on explicit redaction and stay visible when the pii_filter gauge is absent."""
    return {"kind": "QueryVariable", "spec": {
        "name": name, "label": name, "hide": "hideVariable",
        "query": {"kind": "DataQuery", "version": "v0", "group": "",
                  "datasource": {"name": "${ds_prometheus}"},
                  "spec": {"query": "query_result(%s)" % expr, "refId": name}},
        "current": {"text": "", "value": ""}, "options": [], "multi": False,
        "includeAll": False, "allowCustomValue": False, "refresh": "onDashboardLoad",
        "regex": "", "skipUrlSync": True, "sort": "disabled"}}


def custom_var(name, label, csv, current_text, current_value, multi=False, allval=False):
    opts = [{"selected": (v == current_value), "text": t, "value": v} for (t, v) in csv]
    return {"kind": "CustomVariable", "spec": {
        "name": name, "label": label, "query": ", ".join("%s : %s" % (t, v) for (t, v) in csv),
        "current": {"text": current_text, "value": current_value}, "options": opts,
        "multi": multi, "includeAll": allval, "allowCustomValue": False,
        "hide": "dontHide", "skipUrlSync": False}}


def textbox_var(name, label):
    return {"kind": "TextVariable", "spec": {
        "name": name, "label": label, "current": {"text": "", "value": ""},
        "hide": "dontHide", "query": "", "skipUrlSync": False}}


def build_variables():
    v = [
        ds_var("ds_prometheus", "Prometheus", "prometheus", PROM_DS_TEXT, PROM_DS_VALUE),
        ds_var("ds_loki", "Loki", "loki", LOKI_DS_TEXT, LOKI_DS_VALUE),
        ds_var("ds_tempo", "Tempo", "tempo", TEMPO_DS_TEXT, TEMPO_DS_VALUE),
        custom_var("topn", "Top N", [("5", "5"), ("10", "10"), ("15", "15"), ("20", "20"), ("30", "30")], "10", "10"),
        query_var("os_type", "OS", "label_values(tailscale_device_online_ratio, os_type)"),
        query_var("host_name", "Host", "label_values(tailscale_device_online_ratio{os_type=~\"$os_type\"}, host_name)"),
        query_var("device_user", "Device user", "label_values(tailscale_device_online_ratio, tailscale_user)"),
        query_var("device_tag", "Tag", "label_values(tailscale_device_online_ratio, tailscale_tags)"),
        query_var("net_transport", "Transport", "label_values(tailscale_network_flows_total, network_transport)"),
        query_var("traffic_type", "Traffic type", "label_values(tailscale_network_flows_total, tailscale_traffic_type)"),
        query_var("collector", "Collector", "label_values(tailscale2otel_scrape_success_ratio, tailscale_collector)"),
        query_var("tailnet", "Tailnet", 'label_values({__name__=~"tailscale_.+", tailscale_tailnet!=""}, tailscale_tailnet)'),
        query_var("provider", "Provider", 'label_values({__name__=~"tailscale.+", tailscale2otel_provider!=""}, tailscale2otel_provider)'),
        query_var("posture_attr", "Posture attr", "label_values(tailscale_device_attribute_ratio, attribute)"),
        custom_var("log_event", "Log event",
                   [("All", ".+"), ("audit", "tailscale.config.audit"), ("flow", "tailscale.network.flow"),
                    ("posture", "tailscale.device.posture"), ("key expiring", "tailscale.key.expiring"),
                    ("webhook", "tailscale.webhook.*")], "All", ".+"),
        textbox_var("log_filter", "Log filter"),
    ]
    presence = [
        ("has_flows", "tailscale_network_flows_total"),
        ("has_raw_flow", "tailscale_network_io_bytes_total"),
        ("has_rollup_flow", "tailscale_network_io_rollup_bytes_total"),
        ("has_unique", "tailscale_network_unique_dst_peers"),
        ("has_posture", "tailscale_device_posture_ratio"),
        ("has_routes", "tailscale_device_routes_advertised"),
        ("has_derp", "tailscale_device_derp_latency_seconds"),
        ("has_nodemetrics", "tailscale_node_up_ratio"),
        ("has_stream", "tailscale_stream_records_total"),
        ("has_webhook", "tailscale_webhook_events_total"),
        ("has_keys", "tailscale_key_expiry_seconds"),
        ("has_users_pe", "tailscale_user_connected_ratio"),
        ("has_invites", "tailscale_user_invites_count_ratio"),
        ("has_api_retry", "tailscale2otel_api_retries_total"),
        ("has_scrape_err", "tailscale2otel_scrape_errors_total"),
        ("has_path", "tailscaled_inbound_bytes_total"),
        ("has_audit", "tailscale_config_audit_events_total"),
        # new collectors (3131e672+): all emit nothing until the tailnet actually has the
        # data (no MDM posture integrations / VIP services / tailnet-lock errors / SIEM sink,
        # and DERP rollup is gated by cardinality.derp_region_rollup) — so gate every row.
        ("has_posture_integration", "tailscale_posture_integrations_count_ratio"),
        ("has_logstream", "tailscale_logstream_configured_ratio"),
        ("has_services", "tailscale_services_count_ratio"),
        ("has_tailnet_lock", "tailscale_tailnet_lock_errors_ratio"),
        ("has_derp_rollup", "tailscale_derp_region_devices_ratio"),
        ("has_connectivity", "tailscale_device_connectivity_hard_nat_ratio"),
        ("has_exit", "tailscale_device_exit_node_ratio"),
        ("has_subnet", "tailscale_subnet_routes_advertised"),
        ("has_exit_io", "tailscale_exit_node_io_bytes_total"),
        ("has_acl_risk", "tailscale_acl_unrestricted_rules_ratio"),
        ("has_audit_changes", "tailscale_config_audit_changes_total"),
        ("has_invites_dev", "tailscale_device_invites_count_ratio"),
        ("has_key_scopes", "tailscale_key_scopes_ratio"),
        ("has_dns_resolver", "tailscale_dns_resolver_ratio"),
        ("has_version_skew", "tailscale_device_version_skew_ratio"),
        ("has_selfobs", "tailscale2otel_series_active"),
        ("has_api_hist", "tailscale2otel_api_duration_seconds_count"),
        ("has_export_hist", "tailscale2otel_export_duration_seconds_count"),
        ("has_recv_dur", "tailscale_stream_request_duration_seconds_count"),
        ("has_ingest", "tailscale2otel_ingest_records_total"),
        ("has_staleness", "tailscale2otel_scrape_staleness_seconds"),
        ("has_pii", "tailscale2otel_pii_filter_category_ratio"),
        ("has_key_expiry_hist", "tailscale_devices_key_expiry_days_count"),
        # Phase 1H additions
        ("has_rdns", "tailscale_rdns_cache_entries_ratio"),
        ("has_device_attr", "tailscale_device_attribute_ratio"),
        ("has_svc", "tailscale_service_ports"),
        ("has_posture_int", "tailscale_posture_integration_matched_ratio"),
        ("has_dropped", "tailscaled_outbound_dropped_packets_total"),
        # #172: curated client-metrics family (#171) — present once the node-metrics scraper
        # has produced the curated tailscale_node_* series.
        ("has_node_curated", "tailscale_node_io_bytes_total"),
    ]
    for (name, metric) in presence:
        v.append(presence_var(name, metric))
    # has_multitailnet gates on >1 distinct tailnet (not a metric existing), so it is a
    # custom query_result var rather than a presence_var.
    v.append({"kind": "QueryVariable", "spec": {
        "name": "has_multitailnet", "label": "has_multitailnet", "hide": "hideVariable",
        "query": {"kind": "DataQuery", "version": "v0", "group": "",
                  "datasource": {"name": "${ds_prometheus}"},
                  "spec": {"query": "query_result(count(count by (tailscale_tailnet) ({__name__=~\"tailscale_.+\", tailscale_tailnet!=\"\", tailscale_tailnet!=\"-\"})) > 1)",
                           "refId": "has_multitailnet"}},
        # Exclude "" and "-" (single-tailnet placeholder) so placeholder/unnamed-tailnet series
        # don't false-positive has_multitailnet on single-tailnet deployments. (tailscale_tailnet
        # is now a real per-series label, item L — counts distinct tailnets across all series.)
        "current": {"text": "", "value": ""}, "options": [], "multi": False,
        "includeAll": False, "allowCustomValue": False, "refresh": "onDashboardLoad",
        "regex": "", "skipUrlSync": True, "sort": "disabled"}})
    pii_defs = [
        ("pii_host", PII + '{category="hostnames"} == 0'),
        ("pii_node", PII + '{category="node_ids"} == 0'),
        ("pii_perdevice",
         '(%s{category="hostnames"} == 0) and ignoring(category) (%s{category="node_ids"} == 0)' % (PII, PII)),
        ("pii_emails", PII + '{category="emails"} == 0'),
        ("pii_usernames", PII + '{category="user_display_names"} == 0'),
        ("pii_actor",
         '(%s{category="emails"} == 0) and ignoring(category) (%s{category="user_display_names"} == 0)' % (PII, PII)),
        ("pii_int_ips", PII + '{category="internal_ips"} == 0'),
        ("pii_ext_ips", PII + '{category="external_ips"} == 0'),
        ("pii_ts_ips", PII + '{category="tailscale_ips"} == 0'),
        ("pii_topology", PII + '{category="network_topology"} == 0'),
    ]
    for (name, expr) in pii_defs:
        v.append(pii_var(name, expr))
    return v
