"""Dashboard template-variable builders (moved out of build.py in the module split).

Presence sentinels (has_*/pii_*) are no longer declared here (#495) — each tab
module declares the sentinels ITS rows consume, via builder.sentinel() /
builder.pii_sentinel() / builder.raw_sentinel(), as a side effect of building
that tab (see tabs/*.py). This module only builds the base variables
(datasources, topn, the query filters, log event/filter) and appends
whatever sentinels got registered, via builder.registered_sentinels().
"""

from builder import (PROM_DS_TEXT, PROM_DS_VALUE, LOKI_DS_TEXT, LOKI_DS_VALUE,
                     TEMPO_DS_TEXT, TEMPO_DS_VALUE, registered_sentinels)


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
    v.extend(registered_sentinels())
    return v
