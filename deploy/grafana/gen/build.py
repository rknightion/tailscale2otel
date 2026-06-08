#!/usr/bin/env python3
"""Generate the comprehensive tailscale2otel Grafana dashboard (schema v2).

This emits a single multi-tab dashboard using the Grafana v2 dashboard schema
(`dashboard.grafana.app/v2`, Grafana 13+). It is "dashboards-as-code": edit this
file, regenerate the JSON, and push with gcx. The committed artifact is the JSON
(see ../tailscale2otel.json); this generator is the source of truth.

Why a generator instead of hand-written JSON: the v2 schema is verbose (every
panel is an element + a grid item + a query group + a viz config), and we want
uniform "dynamic" behaviour — sections that only appear when their data is
present in the target. That is implemented with hidden presence variables
(`label_values(<metric>, __name__)`) driving `ConditionalRenderingVariable`
rules on rows/tabs. (Data-presence `ConditionalRenderingData` is *also* a v2
feature but the static image renderer does not evaluate it, whereas
variable-driven rendering is evaluated both live and in snapshots.)

Robustness: many tailnet config gauges (ACL/DNS/settings/keys/users) are scraped
on a slow cadence, so a bare instant query at "now" frequently falls outside
Prometheus' 5m staleness window and returns "No data". All current-value reads
therefore use `last_over_time(<metric>[<window>])` so panels show the most recent
known value regardless of poll cadence.

Usage:
    python3 build.py --out ../tailscale2otel.json
    python3 build.py --flat --out /tmp/ts2_flat.json          # rows-only (all tabs) for full-page snapshots
    python3 build.py --tab "Network & Flows" --out /tmp/x.json # rows-only single tab for focused snapshots

Only the Python standard library is required.
"""

import argparse
import json

VERSION = "12.1.0"  # nominal panel-plugin version stamped into vizConfig

# Datasource defaults. The *value* is the datasource UID; "grafanacloud-prom" / "grafanacloud-logs"
# are the standard Grafana Cloud UIDs (present on every GC stack), so these defaults are portable
# and instance-agnostic. The display *text* is cosmetic — Grafana re-resolves it from the UID on load.
PROM_DS_TEXT = "grafanacloud-prom"
PROM_DS_VALUE = "grafanacloud-prom"
LOKI_DS_TEXT = "grafanacloud-logs"
LOKI_DS_VALUE = "grafanacloud-logs"
TEMPO_DS_TEXT = TEMPO_DS_VALUE = "grafanacloud-traces"

RI = "$__rate_interval"
WIN_FAST = "10m"   # last_over_time window for frequently-scraped series (devices, nodes, scrape, runtime)
WIN_SLOW = "2h"    # last_over_time window for slowly-scraped config series (acl, dns, settings, keys, users)

# Resource/infra labels that clutter every instant-vector table; hidden by default.
TBL_NOISE = ["Time", "__name__", "job", "instance", "service_instance_id",
             "service_name", "service_namespace", "Value"]

ELEMENTS = {}
_id = 0


def lot(metric, w=WIN_FAST):
    """last_over_time wrapper — returns the latest sample within w (staleness-proof)."""
    return "last_over_time(%s[%s])" % (metric, w)


def sv(expr):
    """Strip the deploy-churning service_version label from a raw instant-vector expr.

    service_version is a promoted resource label on every exporter series, so a redeploy
    forks each series (new color, duplicate legend / extra table row, a constant 'Version'
    column) for one staleness window. `max without (service_version)` collapses the fork
    while preserving every other label (host_name, tailscale_collector, …) so renames and
    organize() still resolve. Use on raw `lot(...)` table/stat exprs that have no other
    aggregation. NOT for the Diagnostics 'Build info' table, which shows Version on purpose."""
    return "max without (service_version) (%s)" % expr


PII = "tailscale2otel_pii_filter_category_ratio"  # PII filter self-obs gauge


def tn_join(expr):
    """Promote tailscale_tailnet onto a per-series expr via the target_info join, then
    filter by $tailnet. Use for fleet/self-obs panels that should respect the $tailnet var.
    For single-tailnet deployments $tailnet=All -> .* -> no-op."""
    return ('(%s) * on (job, instance) group_left(tailscale_tailnet) '
            'target_info{tailscale_tailnet=~"$tailnet"}') % expr


# ---------------------------------------------------------------------------
# low-level builders
# ---------------------------------------------------------------------------

def prom_t(expr, legend="", refid="A", instant=False, fmt="time_series"):
    return {"kind": "PanelQuery", "spec": {"refId": refid, "hidden": False,
            "query": {"kind": "DataQuery", "version": "v0", "group": "",
                      "datasource": {"name": "${ds_prometheus}"},
                      "spec": {"expr": expr, "instant": instant, "range": (not instant),
                               "legendFormat": legend, "format": fmt}}}}


def loki_t(expr, refid="A", instant=False, maxlines=200, legend=""):
    return {"kind": "PanelQuery", "spec": {"refId": refid, "hidden": False,
            "query": {"kind": "DataQuery", "version": "v0", "group": "",
                      "datasource": {"name": "${ds_loki}"},
                      "spec": {"expr": expr, "queryType": ("instant" if instant else "range"),
                               "maxLines": maxlines, "legendFormat": legend}}}}


def tempo_t(query, refid="A", query_type="traceql", table_type="traces"):
    """Tempo query. query_type 'traceql' (trace list/table) or 'traceqlSearch';
    for TraceQL-metrics timeseries set query like '{...} | rate() by (...)'."""
    return {"kind": "DataQuery", "version": "v0", "group": "",
            "datasource": {"name": "${ds_tempo}"},
            "spec": {"query": query, "queryType": query_type, "tableType": table_type, "refId": refid}}


def thr(steps, mode="absolute"):
    return {"mode": mode, "steps": [{"value": v, "color": c} for (v, c) in steps]}


def vmap(d):
    return [{"type": "value", "options": d}]


def organize(exclude=None, rename=None):
    return {"kind": "Transformation", "group": "organize", "spec": {"options": {
        "excludeByName": {k: True for k in (exclude or [])},
        "renameByName": rename or {}, "indexByName": {}}}}


def merge():
    return {"kind": "Transformation", "group": "merge", "spec": {"options": {}}}


def panel(title, ptype, targets, unit=None, desc="", min_=None, max_=None,
          mappings=None, thresholds=None, custom=None, options=None,
          overrides=None, decimals=None, version=VERSION, novalue=None,
          transformations=None):
    global _id
    _id += 1
    name = "panel-%d" % _id
    for i, _t in enumerate(targets):  # distinct refIds (A, B, C, ...) — duplicate refIds blank a panel
        _t["spec"]["refId"] = chr(65 + i)
    defaults = {}
    if unit is not None:
        defaults["unit"] = unit
    if decimals is not None:
        defaults["decimals"] = decimals
    if min_ is not None:
        defaults["min"] = min_
    if max_ is not None:
        defaults["max"] = max_
    if mappings:
        defaults["mappings"] = mappings
    if thresholds:
        defaults["thresholds"] = thresholds
    if novalue is not None:
        defaults["noValue"] = novalue
    if custom:
        defaults["custom"] = custom
    if ptype == "table" and transformations is None:
        transformations = [organize(exclude=TBL_NOISE)]
    ELEMENTS[name] = {"kind": "Panel", "spec": {
        "id": _id, "title": title, "description": desc, "links": [],
        "data": {"kind": "QueryGroup", "spec": {
            "queries": targets, "queryOptions": {}, "transformations": transformations or []}},
        "vizConfig": {"kind": "VizConfig", "group": ptype, "version": version, "spec": {
            "options": options or {}, "fieldConfig": {"defaults": defaults, "overrides": overrides or []}}}}}
    return name


# convenience option blocks -------------------------------------------------

def stat_opts(calc="lastNotNull", color="value", graph="none", text="auto"):
    return {"reduceOptions": {"calcs": [calc], "fields": "", "values": False},
            "colorMode": color, "graphMode": graph, "textMode": text, "justifyMode": "auto"}


def ts_opts(placement="bottom", mode="list", calcs=None, tt="multi"):
    return {"legend": {"displayMode": mode, "placement": placement, "showLegend": True,
                       "calcs": calcs or []},
            "tooltip": {"mode": tt, "sort": "desc"}}


def ts_custom(style="line", fill=15, width=1, stack=None, points="never", grad="opacity"):
    c = {"drawStyle": style, "lineInterpolation": "smooth", "lineWidth": width,
         "fillOpacity": fill, "showPoints": points, "gradientMode": grad, "axisPlacement": "auto"}
    if stack:
        c["stacking"] = {"mode": stack, "group": "A"}
    return c


def bargauge_opts(calc="lastNotNull", orient="horizontal", mode="gradient"):
    # values=False: reduce each series to ONE bar via `calc`. values=True renders one
    # bar per sample over the time range (a wall of identical bars), which hides the
    # per-series legend (the "loads of 6's" / "just owner/active/member" symptom).
    return {"reduceOptions": {"calcs": [calc], "fields": "", "values": False},
            "orientation": orient, "displayMode": mode, "showUnfilled": True}


def barchart_opts(legend=False):
    return {"orientation": "horizontal", "showValue": "auto", "stacking": "none",
            "legend": {"showLegend": legend, "displayMode": "list", "placement": "bottom"},
            "tooltip": {"mode": "single", "sort": "none"}}


def logs_opts():
    return {"showTime": True, "showLabels": False, "wrapLogMessage": True,
            "prettifyLogMessage": False, "enableLogDetails": True,
            "dedupStrategy": "none", "sortOrder": "Descending"}


# layout builders -----------------------------------------------------------

def place(panel_specs):
    items = []
    x = y = rowh = 0
    for (name, w, h) in panel_specs:
        if x + w > 24:
            x = 0
            y += rowh
            rowh = 0
        items.append({"kind": "GridLayoutItem", "spec": {
            "x": x, "y": y, "width": w, "height": h,
            "element": {"kind": "ElementReference", "name": name}}})
        x += w
        rowh = max(rowh, h)
    return {"kind": "GridLayout", "spec": {"items": items}}


def hq(q, metric, by="", win=RI):
    """histogram_quantile over <metric>_bucket. `by` = extra group labels (besides le)."""
    grp = ("le, " + by) if by else "le"
    return "histogram_quantile(%s, sum by (%s) (rate(%s_bucket[%s])))" % (q, grp, metric, win)


def cond_item(var, op="matches", value=".+"):
    return {"kind": "ConditionalRenderingVariable",
            "spec": {"variable": var, "operator": op, "value": value}}


def cond_group(items, condition="and"):
    return {"kind": "ConditionalRenderingGroup",
            "spec": {"visibility": "show", "condition": condition, "items": items}}


def cond_present(var):  # back-compat: show when presence var is non-empty
    return cond_group([cond_item(var)])


def row(title, panel_specs, present=None, hide_when=None, collapse=False):
    spec = {"title": title, "collapse": collapse, "layout": place(panel_specs)}
    items = []
    if present:
        items.append(cond_item(present))
    for hv in (hide_when or []):
        # show UNLESS the redaction var is non-empty (==0 observed) -> hide-only-on-explicit-redaction
        items.append(cond_item(hv, op="notMatches"))
    if items:
        spec["conditionalRendering"] = cond_group(items)
    return {"kind": "RowsLayoutRow", "spec": spec}


def tab(title, rowlist, present=None):
    spec = {"title": title, "layout": {"kind": "RowsLayout", "spec": {"rows": rowlist}}}
    if present:
        spec["conditionalRendering"] = cond_present(present)
    return {"kind": "TabsLayoutTab", "spec": spec}


# ---------------------------------------------------------------------------
# variables
# ---------------------------------------------------------------------------

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
        query_var("tailnet", "Tailnet", "label_values(target_info, tailscale_tailnet)"),
        query_var("provider", "Provider", "label_values(target_info, tailscale2otel_provider)"),
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
    ]
    for (name, metric) in presence:
        v.append(presence_var(name, metric))
    # has_multitailnet gates on >1 distinct tailnet (not a metric existing), so it is a
    # custom query_result var rather than a presence_var.
    v.append({"kind": "QueryVariable", "spec": {
        "name": "has_multitailnet", "label": "has_multitailnet", "hide": "hideVariable",
        "query": {"kind": "DataQuery", "version": "v0", "group": "",
                  "datasource": {"name": "${ds_prometheus}"},
                  "spec": {"query": "query_result(count(count by (tailscale_tailnet) (target_info{service_name=\"tailscale2otel\", tailscale_tailnet!=\"\", tailscale_tailnet!=\"-\"})) > 1)",
                           "refId": "has_multitailnet"}},
        # FIX-4: exclude "" and "-" (single-tailnet placeholder) so stale/placeholder target_info
        # series don't false-positive has_multitailnet on single-tailnet deployments.
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


# ---------------------------------------------------------------------------
# tabs
# ---------------------------------------------------------------------------

UP_MAP = vmap({"0": {"text": "DOWN", "color": "red", "index": 0},
               "1": {"text": "UP", "color": "green", "index": 1}})
BOOL_MAP = vmap({"0": {"text": "off", "color": "red", "index": 0},
                 "1": {"text": "on", "color": "green", "index": 1}})


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
        (panel("Updates available", "stat",
               [prom_t("count(%s == 1) or vector(0)" % lot("tailscale_device_update_available_ratio"))],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]), options=stat_opts(color="value")), 3, 5),
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
               mappings=BOOL_MAP, thresholds=thr([(None, "red"), (1, "green")]),
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
    return [row("Tailnet health", health), row("Exporter health", exporter),
            row("Activity", activity), row("Capabilities", capabilities)]


def tab_fleet():
    # tailscale_tags=~"$device_tag" (allValue ".*") matches series that lack the
    # label too, so untagged devices still appear under "All".
    df = "{os_type=~\"$os_type\", host_name=~\"$host_name\", tailscale_user=~\"$device_user\", tailscale_tags=~\"$device_tag\"}"
    on = lot("tailscale_device_online_ratio" + df)
    inv = [
        (panel("Online", "stat", [prom_t("count(%s == 1) or vector(0)" % on)],
               unit="short", thresholds=thr([(None, "red"), (1, "green")]), options=stat_opts(color="background")), 3, 5),
        (panel("Total", "stat", [prom_t("count(%s) or vector(0)" % on)], unit="short", options=stat_opts()), 3, 5),
        (panel("Offline", "stat", [prom_t("count(%s == 0) or vector(0)" % on)], unit="short", options=stat_opts(color="value")), 3, 5),
        (panel("Updates available", "stat",
               [prom_t("count(%s == 1) or vector(0)" % lot("tailscale_device_update_available_ratio" + df))],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]), options=stat_opts(color="value")), 3, 5),
        (panel("Distinct users", "stat", [prom_t("count(count by (tailscale_user) (%s))" % on)],
               unit="short", options=stat_opts()), 3, 5),
        (panel("Devices by OS", "bargauge",
               [prom_t("sum by (os_type) (max by (os_type, tailscale_authorized, tailscale_external) (%s))" % lot("tailscale_devices_count_ratio", WIN_SLOW), legend="{{os_type}}")],
               unit="short", options=bargauge_opts()), 9, 5),
        (panel("Devices by tag", "bargauge",
               [prom_t("count by (tailscale_tags) (%s)" % lot("tailscale_device_online_ratio" + df), legend="{{tailscale_tags}}")],
               unit="short", options=bargauge_opts(),
               desc="Device count per ACL tag combination (untagged devices group under an empty bar). "
                    "Requires the tailscale.tags label (exporter >= this release)."), 9, 5),
    ]
    overtime = [
        (panel("Online vs total", "timeseries",
               [prom_t("count(tailscale_device_online_ratio%s == 1)" % df, legend="online"),
                prom_t("count(tailscale_device_online_ratio%s)" % df, legend="total")],
               unit="short", custom=ts_custom(fill=10), options=ts_opts()), 12, 7),
        (panel("Devices by OS over time", "timeseries",
               [prom_t("sum by (os_type) (tailscale_devices_count_ratio)", legend="{{os_type}}")],
               unit="short", custom=ts_custom(stack="normal", fill=30), options=ts_opts(placement="right")), 12, 7),
    ]
    tables = [
        (panel("Updates available", "table",
               [prom_t("%s == 1" % sv(lot("tailscale_device_update_available_ratio" + df)), instant=True, fmt="table")],
               transformations=[organize(exclude=["Time", "__name__", "job", "instance", "host_id",
                                                   "service_instance_id", "service_name", "service_namespace", "Value"],
                                          rename={"host_name": "Host", "os_type": "OS", "os_version": "OS version",
                                                  "tailscale_user": "User"})],
               desc="Devices with a client update available."), 8, 8),
        (panel("Device key expiry (time until)", "table",
               [prom_t("%s - time()" % sv(lot("tailscale_device_key_expiry_seconds" + df, WIN_SLOW)), instant=True, fmt="table")],
               unit="s", transformations=[organize(exclude=["Time", "__name__", "job", "instance", "host_id",
                                                             "service_instance_id", "service_name", "service_namespace"],
                                                    rename={"host_name": "Host", "tailscale_user": "User",
                                                            "Value": "Expires in"})],
               desc="Time until each device node key expires."), 8, 8),
        (panel("Last seen (time since)", "table",
               [prom_t("time() - %s" % sv(lot("tailscale_device_last_seen_seconds" + df, WIN_SLOW)), instant=True, fmt="table")],
               unit="s", transformations=[organize(exclude=["Time", "__name__", "job", "instance", "host_id",
                                                            "service_instance_id", "service_name", "service_namespace"],
                                                   rename={"host_name": "Host", "tailscale_user": "User",
                                                           "Value": "Last seen"})],
               desc="Time since each device was last seen."), 8, 8),
    ]
    derp = [
        (panel("DERP latency by host / region", "table",
               [prom_t(sv(lot("tailscale_device_derp_latency_seconds{host_name=~\"$host_name\"}")), instant=True, fmt="table")],
               unit="s", transformations=[organize(exclude=["Time", "__name__", "job", "instance", "host_id",
                                                            "service_instance_id", "service_name", "service_namespace"],
                                                   rename={"host_name": "Host", "tailscale_derp_region": "Region",
                                                           "tailscale_derp_preferred": "Preferred", "Value": "Latency"})]), 14, 8),
        (panel("Preferred DERP regions", "bargauge",
               [prom_t("count by (tailscale_derp_region) (max by (tailscale_derp_region, host_name) (%s))"
                       % lot("tailscale_device_derp_latency_seconds{tailscale_derp_preferred=\"true\"}"),
                       legend="{{tailscale_derp_region}}")], unit="short", options=bargauge_opts()), 10, 8),
    ]
    routes = [
        (panel("Subnet routes — advertised vs enabled", "table",
               [prom_t(sv(lot("tailscale_device_routes_advertised{host_name=~\"$host_name\"}")), instant=True, fmt="table", refid="A"),
                prom_t(sv(lot("tailscale_device_routes_enabled{host_name=~\"$host_name\"}")), instant=True, fmt="table", refid="B")],
               unit="short", transformations=[merge(),
                                              organize(exclude=["Time", "__name__", "job", "instance", "host_id",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"host_name": "Host", "Value #A": "Advertised", "Value #B": "Enabled"})]), 24, 8),
    ]
    posture = [
        (panel("Posture overview", "table",
               [prom_t(sv(lot("tailscale_device_posture_ratio{host_name=~\"$host_name\"}", WIN_SLOW)), instant=True, fmt="table")],
               transformations=[organize(exclude=["Time", "__name__", "job", "instance", "host_id",
                                                  "service_instance_id", "service_name", "service_namespace", "Value"])],
               desc="Per-device posture: OS, client version, auto-update, encryption, track."), 16, 8),
        (panel("Clients by version", "barchart",
               [prom_t("count by (ts_version) (max by (ts_version, host_name) (%s))" % lot("tailscale_device_posture_ratio", WIN_SLOW), legend="{{ts_version}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 8, 8),
    ]
    return [row("Inventory", inv), row("Trends", overtime), row("Device health", tables),
            row("Connectivity / DERP", derp, present="has_derp"),
            row("Subnet routes", routes, present="has_routes"),
            row("Device posture", posture, present="has_posture")]


def tab_network():
    tf = "{network_transport=~\"$net_transport\", tailscale_traffic_type=~\"$traffic_type\"}"
    # tf, but also exclude unclassified (empty-label) services so the top-services
    # barcharts name every bar instead of falling back to "Value" for the empty group.
    tsf = tf[:-1] + ", tailscale_dst_service!=\"\"}"
    summary = [
        (panel("Throughput (now)", "stat",
               [prom_t("sum(rate(tailscale_network_io_rollup_bytes_total[%s])) or "
                       "sum(rate(tailscale_network_io_bytes_total[%s]))" % (RI, RI), instant=True)],
               unit="Bps", options=stat_opts(graph="area", color="value")), 4, 5),
        (panel("Packets/s (now)", "stat",
               [prom_t("sum(rate(tailscale_network_packets_rollup_total[%s])) or "
                       "sum(rate(tailscale_network_packets_total[%s]))" % (RI, RI), instant=True)],
               unit="pps", options=stat_opts(graph="area")), 4, 5),
        (panel("Flows/s (now)", "stat", [prom_t("sum(rate(tailscale_network_flows_total[%s]))" % RI, instant=True)],
               unit="cps", options=stat_opts(graph="area")), 4, 5),
        (panel("Flows/s by transport", "timeseries",
               [prom_t("sum by (network_transport) (rate(tailscale_network_flows_total%s[%s]))" % (tf, RI), legend="{{network_transport}}")],
               unit="cps", custom=ts_custom(stack="normal"), options=ts_opts()), 6, 5),
        (panel("Flows/s by traffic type", "timeseries",
               [prom_t("sum by (tailscale_traffic_type) (rate(tailscale_network_flows_total%s[%s]))" % (tf, RI), legend="{{tailscale_traffic_type}}")],
               unit="cps", custom=ts_custom(stack="normal"), options=ts_opts()), 6, 5),
    ]
    rollup = [
        (panel("Throughput by direction", "timeseries",
               [prom_t("sum by (network_io_direction) (rate(tailscale_network_io_rollup_bytes_total%s[%s]))" % (tf, RI), legend="{{network_io_direction}}")],
               unit="Bps", custom=ts_custom(stack="normal", fill=25), options=ts_opts()), 8, 7),
        (panel("Throughput by transport", "timeseries",
               [prom_t("sum by (network_transport) (rate(tailscale_network_io_rollup_bytes_total%s[%s]))" % (tf, RI), legend="{{network_transport}}")],
               unit="Bps", custom=ts_custom(stack="normal", fill=25), options=ts_opts()), 8, 7),
        (panel("Throughput by traffic type", "timeseries",
               [prom_t("sum by (tailscale_traffic_type) (rate(tailscale_network_io_rollup_bytes_total%s[%s]))" % (tf, RI), legend="{{tailscale_traffic_type}}")],
               unit="Bps", custom=ts_custom(stack="normal", fill=25), options=ts_opts()), 8, 7),
        (panel("Top $topn source nodes", "barchart",
               [prom_t("topk($topn, sum by (tailscale_src_node) (rate(tailscale_network_io_rollup_bytes_total%s[%s])))" % (tf, RI), legend="{{tailscale_src_node}}", instant=True, fmt="table")],
               unit="Bps", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 8, 8),
        (panel("Top $topn destination nodes", "barchart",
               [prom_t("topk($topn, sum by (tailscale_dst_node) (rate(tailscale_network_io_rollup_bytes_total%s[%s])))" % (tf, RI), legend="{{tailscale_dst_node}}", instant=True, fmt="table")],
               unit="Bps", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 8, 8),
        (panel("Top destination services", "barchart",
               [prom_t("topk($topn, sum by (tailscale_dst_service) (rate(tailscale_network_io_rollup_bytes_total%s[%s])))" % (tsf, RI), legend="{{tailscale_dst_service}}", instant=True, fmt="table")],
               unit="Bps", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 8, 8),
        (panel("__other__ rollup share", "stat",
               [prom_t("(sum(rate(tailscale_network_io_rollup_bytes_total{tailscale_dst_node=\"__other__\"}[%s])) or vector(0)) / "
                       "clamp_min(sum(rate(tailscale_network_io_rollup_bytes_total[%s])), 1)" % (RI, RI), instant=True)],
               unit="percentunit", thresholds=thr([(None, "green"), (0.5, "yellow"), (0.8, "red")]),
               options=stat_opts(color="background"), desc="Fraction of rollup bytes folded into the bounded __other__ bucket."), 8, 6),
        (panel("Unique dst peers per src", "table",
               [prom_t(sv(lot("tailscale_network_unique_dst_peers")), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"tailscale_src_node": "Source node", "Value": "Unique peers"})],
               desc="Distinct destination peers per source node (last flush)."), 8, 6),
        (panel("Unique dst ports per src", "table",
               [prom_t(sv(lot("tailscale_network_unique_dst_ports")), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"tailscale_src_node": "Source node", "Value": "Unique ports"})],
               desc="Distinct destination ports per source node (last flush)."), 8, 6),
    ]
    raw = [
        (panel("Throughput by direction (raw)", "timeseries",
               [prom_t("sum by (network_io_direction) (rate(tailscale_network_io_bytes_total%s[%s]))" % (tf, RI), legend="{{network_io_direction}}")],
               unit="Bps", custom=ts_custom(stack="normal", fill=25), options=ts_opts()), 8, 7),
        (panel("Packets by direction (raw)", "timeseries",
               [prom_t("sum by (network_io_direction) (rate(tailscale_network_packets_total%s[%s]))" % (tf, RI), legend="{{network_io_direction}}")],
               unit="pps", custom=ts_custom(stack="normal"), options=ts_opts()), 8, 7),
        (panel("Throughput by transport (raw)", "timeseries",
               [prom_t("sum by (network_transport) (rate(tailscale_network_io_bytes_total%s[%s]))" % (tf, RI), legend="{{network_transport}}")],
               unit="Bps", custom=ts_custom(stack="normal", fill=25), options=ts_opts()), 8, 7),
        (panel("Top $topn source nodes (raw)", "barchart",
               [prom_t("topk($topn, sum by (tailscale_src_node) (rate(tailscale_network_io_bytes_total%s[%s])))" % (tf, RI), legend="{{tailscale_src_node}}", instant=True, fmt="table")],
               unit="Bps", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 8, 8),
        (panel("Top $topn destination nodes (raw)", "barchart",
               [prom_t("topk($topn, sum by (tailscale_dst_node) (rate(tailscale_network_io_bytes_total%s[%s])))" % (tf, RI), legend="{{tailscale_dst_node}}", instant=True, fmt="table")],
               unit="Bps", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 8, 8),
        (panel("Top $topn destination services (raw)", "barchart",
               [prom_t("topk($topn, sum by (tailscale_dst_service) (rate(tailscale_network_io_bytes_total%s[%s])))" % (tsf, RI), legend="{{tailscale_dst_service}}", instant=True, fmt="table")],
               unit="Bps", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 8, 8),
    ]
    return [row("Flow summary", summary, present="has_flows"),
            row("Throughput & talkers — ROLLUP (bounded top-N)", rollup, present="has_rollup_flow"),
            row("Throughput & talkers — RAW (full detail)", raw, present="has_raw_flow")]


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
        (panel("Last delivery error", "stat",
               [prom_t("max(%s) or vector(0)" % lot("tailscale_logstream_error_ratio", WIN_FAST), instant=True)],
               mappings=vmap({"0": {"text": "OK", "color": "green", "index": 0},
                              "1": {"text": "ERROR", "color": "red", "index": 1}}),
               thresholds=thr([(None, "green"), (1, "red")]), options=stat_opts(color="background"),
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
               [prom_t("time() - %s" % sv(lot("tailscale_logstream_last_activity_seconds", WIN_SLOW)), instant=True, fmt="table")],
               unit="s", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                            "service_instance_id", "service_name", "service_namespace"],
                                                   rename={"tailscale_logstream_type": "Log type", "Value": "Last activity age"})],
               desc="Time since the most recent delivery activity per log type (alert on staleness)."), 8, 6),
        (panel("Delivery errors", "logs",
               [loki_t("{service_name=\"tailscale2otel\"} | event_name=`tailscale.logstream.error` |~ `$log_filter`", maxlines=100)],
               options=logs_opts(), desc="Per-stream delivery errors; the error text is the log body."), 16, 7),
    ]
    return [row("Audit & event rates", rates), row("Stream ingestion", ingest, present="has_stream"),
            row("Log streaming delivery (SIEM)", streamhealth, present="has_logstream"),
            row("Webhooks", webhook, present="has_webhook"), row("Log explorer", logstream),
            row("Flow logs", flowlogs, present="has_flows"), row("Posture logs", posturelogs, present="has_posture")]


def tab_policy():
    acl = [
        (panel("ACL last changed", "stat", [prom_t("time() - max(%s)" % lot("tailscale_acl_last_changed_seconds", WIN_SLOW))],
               unit="s", options=stat_opts(graph="none")), 6, 5),
        (panel("ACL size", "stat", [prom_t("max(%s)" % lot("tailscale_acl_size_bytes", WIN_SLOW))],
               unit="bytes", options=stat_opts()), 6, 5),
        (panel("ACL rules by section", "bargauge",
               [prom_t("max by (tailscale_acl_section) (%s)" % lot("tailscale_acl_rules_ratio", WIN_SLOW), legend="{{tailscale_acl_section}}")],
               unit="short", options=bargauge_opts()), 12, 5),
    ]
    dns = [
        (panel("MagicDNS", "stat", [prom_t("max(%s)" % lot("tailscale_dns_magic_dns_ratio", WIN_SLOW))],
               mappings=BOOL_MAP, thresholds=thr([(None, "red"), (1, "green")]), options=stat_opts(color="background")), 6, 5),
        (panel("Nameservers", "stat", [prom_t("max(%s)" % lot("tailscale_dns_nameservers_count_ratio", WIN_SLOW))], unit="short", options=stat_opts()), 6, 5),
        (panel("Search paths", "stat", [prom_t("max(%s)" % lot("tailscale_dns_search_paths_count_ratio", WIN_SLOW))], unit="short", options=stat_opts()), 6, 5),
        (panel("Split-DNS zones", "stat", [prom_t("max(%s)" % lot("tailscale_dns_split_zones_count_ratio", WIN_SLOW))], unit="short", options=stat_opts()), 6, 5),
    ]
    settings = [
        (panel("Tailnet settings", "table", [prom_t(sv(lot("tailscale_setting_enabled_ratio", WIN_SLOW)), instant=True, fmt="table")],
               transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                  "service_instance_id", "service_name", "service_namespace"],
                                         rename={"tailscale_setting_name": "Setting", "Value": "Enabled"})],
               desc="Per-setting enabled (1) / disabled (0)."), 8, 7),
        (panel("Device key duration", "stat", [prom_t("max(%s)" % lot("tailscale_setting_devices_key_duration_days", WIN_SLOW))],
               unit="d", options=stat_opts()), 4, 7),
        (panel("Tailnet features", "table", [prom_t(sv(lot("tailscale_feature_enabled_ratio", WIN_SLOW)), instant=True, fmt="table")],
               transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                  "service_instance_id", "service_name", "service_namespace"],
                                         rename={"tailscale_feature": "Feature", "Value": "Enabled"})],
               desc="Per-feature enabled (1) / disabled (0)."), 12, 7),
    ]
    users = [
        (panel("Users by role", "barchart",
               [prom_t("sum by (tailscale_user_role) (max by (tailscale_user_role, tailscale_user_status, tailscale_user_type) (%s))" % lot("tailscale_users_count_ratio", WIN_SLOW), legend="{{tailscale_user_role}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 8, 6),
        (panel("Users by status", "barchart",
               [prom_t("sum by (tailscale_user_status) (max by (tailscale_user_role, tailscale_user_status, tailscale_user_type) (%s))" % lot("tailscale_users_count_ratio", WIN_SLOW), legend="{{tailscale_user_status}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 8, 6),
        (panel("Users by type", "barchart",
               [prom_t("sum by (tailscale_user_type) (max by (tailscale_user_role, tailscale_user_status, tailscale_user_type) (%s))" % lot("tailscale_users_count_ratio", WIN_SLOW), legend="{{tailscale_user_type}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 8, 6),
    ]
    users_pe = [
        (panel("Per-user detail", "table",
               [prom_t(sv(lot("tailscale_user_connected_ratio", WIN_SLOW)), instant=True, fmt="table", refid="A"),
                prom_t(sv(lot("tailscale_user_devices_ratio", WIN_SLOW)), instant=True, fmt="table", refid="B"),
                prom_t("time() - %s" % sv(lot("tailscale_user_last_seen_seconds", WIN_SLOW)), instant=True, fmt="table", refid="C")],
               transformations=[merge(),
                                organize(exclude=["Time", "__name__", "job", "instance", "enduser_id",
                                                  "service_instance_id", "service_name", "service_namespace"],
                                         rename={"tailscale_user_login": "User", "Value #A": "Connected",
                                                 "Value #B": "Devices", "Value #C": "Last seen"})],
               overrides=[{"matcher": {"id": "byName", "options": "Last seen"},
                           "properties": [{"id": "unit", "value": "s"}]}],
               desc="Per-user connected / device count / time since last seen."), 24, 8),
    ]
    invites = [
        (panel("User invites", "bargauge",
               [prom_t("max by (tailscale_user_invite_role, tailscale_user_invite_accepted) (%s)" % lot("tailscale_user_invites_count_ratio", WIN_SLOW),
                       legend="{{tailscale_user_invite_role}} accepted={{tailscale_user_invite_accepted}}")],
               unit="short", options=bargauge_opts()), 24, 5),
    ]
    keys = [
        (panel("Keys by type", "bargauge",
               [prom_t("sum by (tailscale_key_type, tailscale_key_revoked, tailscale_key_invalid) (%s)" % lot("tailscale_keys_count_ratio", WIN_SLOW),
                       legend="{{tailscale_key_type}} revoked={{tailscale_key_revoked}} invalid={{tailscale_key_invalid}}")],
               unit="short", options=bargauge_opts()), 10, 7),
        (panel("Key expiry (time until)", "table",
               [prom_t("%s - time()" % sv(lot("tailscale_key_expiry_seconds", WIN_SLOW)), instant=True, fmt="table")],
               unit="s", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                            "service_instance_id", "service_name", "service_namespace"],
                                                   rename={"tailscale_key_id": "Key ID", "tailscale_key_type": "Type",
                                                           "tailscale_key_description": "Description", "Value": "Expires in"})],
               desc="Time until each API/auth key expires."), 14, 7),
    ]
    services_vip = [
        (panel("Services (VIP)", "stat",
               [prom_t("max(%s) or vector(0)" % lot("tailscale_services_count_ratio", WIN_SLOW), instant=True)],
               unit="short", options=stat_opts(color="value"),
               desc="Tailscale Services (VIP services) advertised in the tailnet."), 6, 6),
        (panel("Port rules per service", "bargauge",
               [prom_t("max by (tailscale_service_name) (%s)" % lot("tailscale_service_ports", WIN_SLOW), legend="{{tailscale_service_name}}")],
               unit="short", options=bargauge_opts(),
               desc="Port rules exposed by each Service. Gated by cardinality.per_entity.service."), 18, 6),
        (panel("Backing hosts by service", "table",
               [prom_t(sv(lot("tailscale_service_hosts_ratio", WIN_SLOW)), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"tailscale_service_name": "Service",
                                                               "tailscale_service_approval": "Approval",
                                                               "tailscale_service_configured": "Configured",
                                                               "Value": "Hosts"})],
               desc="Backing-host count per Service, bucketed by approval + configured state. "
                    "Gated by collect_hosts + cardinality.per_entity.service."), 24, 7),
    ]
    return [row("Access control (ACL)", acl), row("DNS", dns), row("Settings & features", settings),
            row("Services / VIP", services_vip, present="has_services"),
            row("Users", users), row("Per-user detail", users_pe, present="has_users_pe"),
            row("User invites", invites, present="has_invites"), row("API keys", keys)]


def tab_nodemetrics():
    health = [
        (panel("Targets up", "stat", [prom_t("count(%s == 1) or vector(0)" % lot("tailscale_node_up_ratio", "15m"))],
               unit="short", thresholds=thr([(None, "red"), (1, "green")]), options=stat_opts(color="background")), 5, 5),
        (panel("Targets total", "stat", [prom_t("count(%s) or vector(0)" % lot("tailscale_node_up_ratio", "15m"))],
               unit="short", options=stat_opts()), 5, 5),
        (panel("Discovery OK", "stat", [prom_t("max(%s)" % lot("tailscale2otel_nodemetrics_discovery_success_ratio"))],
               mappings=UP_MAP, thresholds=thr([(None, "red"), (1, "green")]), options=stat_opts(color="background")), 5, 5),
        (panel("Discovered targets", "stat", [prom_t("max(%s)" % lot("tailscale2otel_nodemetrics_discovery_targets"))],
               unit="short", options=stat_opts()), 5, 5),
        (panel("Node up", "table", [prom_t(sv(lot("tailscale_node_up_ratio", "15m")), instant=True, fmt="table")],
               transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                  "service_instance_id", "service_name", "service_namespace"],
                                         rename={"tailscale_node": "Node", "Value": "Up"})],
               desc="Per-target scrape health (1=up)."), 4, 5),
    ]
    traffic = [
        (panel("Inbound bytes/s", "timeseries",
               [prom_t("sum by (tailscale_node) (rate(tailscaled_inbound_bytes_total[%s]))" % RI, legend="{{tailscale_node}}")],
               unit="Bps", custom=ts_custom(), options=ts_opts(placement="right")), 12, 7),
        (panel("Outbound bytes/s", "timeseries",
               [prom_t("sum by (tailscale_node) (rate(tailscaled_outbound_bytes_total[%s]))" % RI, legend="{{tailscale_node}}")],
               unit="Bps", custom=ts_custom(), options=ts_opts(placement="right")), 12, 7),
        (panel("Inbound packets/s", "timeseries",
               [prom_t("sum by (tailscale_node) (rate(tailscaled_inbound_packets_total[%s]))" % RI, legend="{{tailscale_node}}")],
               unit="pps", custom=ts_custom(), options=ts_opts()), 12, 7),
        (panel("Outbound packets/s", "timeseries",
               [prom_t("sum by (tailscale_node) (rate(tailscaled_outbound_packets_total[%s]))" % RI, legend="{{tailscale_node}}")],
               unit="pps", custom=ts_custom(), options=ts_opts()), 12, 7),
    ]
    routing = [
        (panel("Advertised routes", "table", [prom_t(sv(lot("tailscaled_advertised_routes", "15m")), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"tailscale_node": "Node", "Value": "Advertised"})]), 8, 7),
        (panel("Approved routes", "table", [prom_t(sv(lot("tailscaled_approved_routes", "15m")), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"tailscale_node": "Node", "Value": "Approved"})]), 8, 7),
        (panel("Health messages", "table", [prom_t(sv(lot("tailscaled_health_messages", "15m")), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"tailscale_node": "Node", "Value": "Messages"})],
               desc="tailscaled self-reported health warnings."), 8, 7),
        (panel("Home DERP region", "table", [prom_t(sv(lot("tailscaled_home_derp_region_id", "15m")), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"tailscale_node": "Node", "Value": "Region ID"})]), 12, 6),
        (panel("Peer relay endpoints", "table", [prom_t(sv(lot("tailscaled_peer_relay_endpoints", "15m")), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"tailscale_node": "Node", "Value": "Endpoints"})]), 12, 6),
    ]
    paths = [
        (panel("% traffic via DERP relay by node", "timeseries",
               [prom_t("sum by (tailscale_node) (rate(tailscaled_inbound_bytes_total{path=\"derp\"}[%s]) + rate(tailscaled_outbound_bytes_total{path=\"derp\"}[%s])) / "
                       "clamp_min(sum by (tailscale_node) (rate(tailscaled_inbound_bytes_total[%s]) + rate(tailscaled_outbound_bytes_total[%s])), 1)"
                       % (RI, RI, RI, RI), legend="{{tailscale_node}}")],
               unit="percentunit", min_=0, max_=1, custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Fraction of each node's traffic relayed via DERP rather than sent direct. Sustained "
                    "high values indicate NAT-traversal problems (added latency)."), 12, 7),
        (panel("Throughput by path", "timeseries",
               [prom_t("sum by (path) (rate(tailscaled_inbound_bytes_total[%s]) + rate(tailscaled_outbound_bytes_total[%s]))"
                       % (RI, RI), legend="{{path}}")],
               unit="Bps", custom=ts_custom(stack="normal", fill=25), options=ts_opts(),
               desc="Total tailnet throughput split by path: DERP relay vs direct IPv4 vs direct IPv6."), 12, 7),
        (panel("Fleet DERP share (now)", "stat",
               [prom_t("sum(rate(tailscaled_inbound_bytes_total{path=\"derp\"}[%s]) + rate(tailscaled_outbound_bytes_total{path=\"derp\"}[%s])) / "
                       "clamp_min(sum(rate(tailscaled_inbound_bytes_total[%s]) + rate(tailscaled_outbound_bytes_total[%s])), 1)"
                       % (RI, RI, RI, RI), instant=True)],
               unit="percentunit", thresholds=thr([(None, "green"), (0.3, "yellow"), (0.6, "red")]),
               options=stat_opts(color="background"),
               desc="Fleet-wide fraction of bytes relayed via DERP."), 8, 6),
        (panel("Path mix (DERP / IPv4 / IPv6)", "barchart",
               [prom_t("sum by (path) (rate(tailscaled_inbound_bytes_total[%s]) + rate(tailscaled_outbound_bytes_total[%s]))"
                       % (RI, RI), legend="{{path}}", instant=True, fmt="table")],
               unit="Bps", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 16, 6),
    ]
    derprollup = [
        (panel("Best latency per DERP region", "bargauge",
               [prom_t("max by (tailscale_derp_region) (%s)" % lot("tailscale_derp_region_latency_min_seconds"), legend="{{tailscale_derp_region}}")],
               unit="s", options=bargauge_opts(),
               desc="Best (minimum) device→DERP-region latency across the tailnet, per region."), 8, 7),
        (panel("Devices per DERP region", "bargauge",
               [prom_t("max by (tailscale_derp_region) (%s)" % lot("tailscale_derp_region_devices_ratio"), legend="{{tailscale_derp_region}}")],
               unit="short", options=bargauge_opts(),
               desc="Number of devices reporting latency to each DERP region."), 8, 7),
        (panel("Preferred DERP region distribution", "bargauge",
               [prom_t("max by (tailscale_derp_region) (%s)" % lot("tailscale_derp_region_preferred_ratio"), legend="{{tailscale_derp_region}}")],
               unit="short", options=bargauge_opts(),
               desc="Number of devices that prefer each DERP region."), 8, 7),
    ]
    return [row("Scraper health", health), row("Traffic (tailscaled)", traffic),
            row("Connection paths (DERP vs direct)", paths, present="has_path"),
            row("DERP regions (tailnet rollup)", derprollup, present="has_derp_rollup"),
            row("Routing & health", routing)]


def tab_diagnostics():
    cf = "{tailscale_collector=~\"$collector\"}"
    live = [
        (panel("Exporter up", "stat", [prom_t("max(%s)" % lot("tailscale2otel_up_ratio"))],
               mappings=UP_MAP, thresholds=thr([(None, "red"), (1, "green")]), options=stat_opts(color="background")), 4, 5),
        (panel("Collectors OK", "stat", [prom_t("count(%s == 1) or vector(0)" % lot("tailscale2otel_scrape_success_ratio"))],
               unit="short", thresholds=thr([(None, "green")]), options=stat_opts(color="value")), 4, 5),
        (panel("Goroutines", "stat", [prom_t("max(%s)" % lot("tailscale2otel_runtime_goroutines_ratio"))],
               unit="short", options=stat_opts(graph="area")), 4, 5),
        (panel("GOMAXPROCS", "stat", [prom_t("max(%s)" % lot("tailscale2otel_runtime_gomaxprocs_ratio"))],
               unit="short", options=stat_opts()), 4, 5),
        (panel("Build info", "table", [prom_t(lot("tailscale2otel_build_info_ratio", WIN_SLOW), instant=True, fmt="table")],
               transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                  "service_instance_id", "service_name", "service_namespace", "Value"],
                                         rename={"service_version": "Version", "go_version": "Go version"})],
               desc="Version / Go version (labels)."), 8, 5),
    ]
    collectors = [
        (panel("Scrape duration by collector", "timeseries",
               [prom_t("max by (tailscale_collector) (tailscale2otel_scrape_duration_seconds%s)" % cf, legend="{{tailscale_collector}}")],
               unit="s", custom=ts_custom(), options=ts_opts(placement="right")), 12, 7),
        (panel("Scrape success by collector", "timeseries",
               [prom_t("max by (tailscale_collector) (tailscale2otel_scrape_success_ratio%s)" % cf, legend="{{tailscale_collector}}")],
               unit="short", min_=0, max_=1, custom=ts_custom(style="line", fill=10), options=ts_opts(placement="right")), 12, 7),
        (panel("Last scrape age", "table",
               [prom_t("time() - %s" % sv(lot("tailscale2otel_scrape_last_timestamp_seconds" + cf)), instant=True, fmt="table")],
               unit="s", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                            "service_instance_id", "service_name", "service_namespace"],
                                                   rename={"tailscale_collector": "Collector", "Value": "Age"})],
               desc="Seconds since each collector's last scrape."), 12, 7),
        (panel("Scrape errors/s by collector / type", "timeseries",
               [prom_t("sum by (tailscale_collector, error_type) (rate(tailscale2otel_scrape_errors_total%s[%s]))" % (cf, RI), legend="{{tailscale_collector}} / {{error_type}}")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 12, 7),
    ]
    api = [
        (panel("API requests/s by status", "timeseries",
               [prom_t("sum by (http_response_status_code) (rate(tailscale2otel_api_requests_total[%s]))" % RI, legend="{{http_response_status_code}}")],
               unit="reqps", custom=ts_custom(stack="normal"), options=ts_opts(placement="right")), 12, 7),
        (panel("API requests/s by endpoint", "timeseries",
               [prom_t("sum by (endpoint) (rate(tailscale2otel_api_requests_total[%s]))" % RI, legend="{{endpoint}}")],
               unit="reqps", custom=ts_custom(stack="normal"), options=ts_opts(placement="right")), 12, 7),
    ]
    api_cond = [
        (panel("API retries/s by endpoint", "timeseries",
               [prom_t("sum by (endpoint) (rate(tailscale2otel_api_retries_total[%s]))" % RI, legend="{{endpoint}}")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 12, 6),
        (panel("Export failures/s by type", "timeseries",
               [prom_t("sum by (error_type) (rate(tailscale2otel_export_failures_total[%s]))" % RI, legend="{{error_type}}")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 12, 6),
    ]
    cardinality = [
        (panel("Active series by metric (top $topn)", "timeseries",
               [prom_t("topk($topn, max by (metric_name) (tailscale2otel_series_active))", legend="{{metric_name}}")],
               unit="short", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Per-metric active series (cap 10k). Watch the flow families."), 12, 8),
        (panel("Dedup set size", "timeseries", [prom_t("max by (dedup_set) (tailscale2otel_dedup_size_ratio)", legend="{{dedup_set}}")],
               unit="short", custom=ts_custom(), options=ts_opts()), 6, 8),
        (panel("Dedup evictions/s", "timeseries",
               [prom_t("sum by (dedup_set) (rate(tailscale2otel_dedup_evictions_total[%s]))" % RI, legend="{{dedup_set}}")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 6, 8),
    ]
    enrich = [
        (panel("Enrich cache age", "timeseries", [prom_t("max(tailscale2otel_enrich_cache_age_seconds)", legend="age")],
               unit="s", custom=ts_custom(), options=ts_opts()), 12, 6),
        (panel("Enrich cache size", "timeseries", [prom_t("max(tailscale2otel_enrich_cache_size_ratio)", legend="devices")],
               unit="short", custom=ts_custom(), options=ts_opts()), 12, 6),
    ]
    runtime = [
        (panel("Memory breakdown", "timeseries",
               [prom_t("max(tailscale2otel_runtime_memory_heap_inuse_bytes)", legend="heap in-use"),
                prom_t("max(tailscale2otel_runtime_memory_heap_sys_bytes - tailscale2otel_runtime_memory_heap_inuse_bytes)", legend="heap idle"),
                prom_t("max(tailscale2otel_runtime_memory_stack_inuse_bytes)", legend="stack in-use"),
                prom_t("max(tailscale2otel_runtime_memory_sys_bytes - tailscale2otel_runtime_memory_heap_sys_bytes - tailscale2otel_runtime_memory_stack_inuse_bytes)", legend="other (non-heap)")],
               unit="bytes", custom=ts_custom(stack="normal", fill=25), options=ts_opts(placement="right"),
               desc="Go memory obtained from the OS (runtime.memory.sys), stacked into in-use heap, idle/reserved heap, stacks, and other non-heap runtime (GC, mspan/mcache). Total height = total sys."), 12, 7),
        (panel("Goroutines & stack", "timeseries",
               [prom_t("max(tailscale2otel_runtime_goroutines_ratio)", legend="goroutines"),
                prom_t("max(tailscale2otel_runtime_memory_stack_inuse_bytes)", legend="stack inuse")],
               unit="short", custom=ts_custom(), options=ts_opts(),
               overrides=[{"matcher": {"id": "byName", "options": "stack inuse"},
                           "properties": [{"id": "unit", "value": "bytes"}, {"id": "custom.axisPlacement", "value": "right"}]}]), 12, 7),
        (panel("GC cycles/s", "timeseries", [prom_t("sum(rate(tailscale2otel_runtime_gc_count_total[%s]))" % RI, legend="gc/s")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 8, 6),
        (panel("GC pause/s", "timeseries", [prom_t("sum(rate(tailscale2otel_runtime_gc_pause_time_seconds_total[%s]))" % RI, legend="pause s/s")],
               unit="s", custom=ts_custom(), options=ts_opts()), 8, 6),
        (panel("GC CPU fraction", "timeseries", [prom_t("max(tailscale2otel_runtime_gc_cpu_fraction_ratio)", legend="gc cpu")],
               unit="percentunit", custom=ts_custom(), options=ts_opts()), 8, 6),
        (panel("GC next-target vs live heap", "timeseries",
               [prom_t("max(tailscale2otel_runtime_gc_next_target_bytes)", legend="next GC target"),
                prom_t("max(tailscale2otel_runtime_memory_heap_alloc_bytes)", legend="live heap")],
               unit="bytes", custom=ts_custom(), options=ts_opts(),
               desc="Live heap vs the heap size that triggers the next GC; the gap is GC headroom."), 8, 6),
        (panel("Heap alloc churn", "timeseries",
               [prom_t("sum(rate(tailscale2otel_runtime_memory_alloc_bytes_total[%s]))" % RI, legend="alloc/s")],
               unit="Bps", custom=ts_custom(), options=ts_opts(),
               desc="Cumulative heap-allocation rate (includes freed); allocation churn / GC pressure."), 8, 6),
        (panel("Live heap objects", "timeseries",
               [prom_t("max(tailscale2otel_runtime_memory_heap_objects_ratio)", legend="objects")],
               unit="short", custom=ts_custom(), options=ts_opts(),
               desc="Number of live heap objects (a count, despite the _ratio suffix)."), 8, 6),
    ]
    reliability = [
        (panel("Scrape errors/s", "timeseries",
               [prom_t("sum by (tailscale_collector) (rate(tailscale2otel_scrape_errors_total[%s]))" % RI, legend="{{tailscale_collector}}")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 6, 6),
        (panel("Checkpoint persist errors/s", "timeseries",
               [prom_t("sum by (tailscale_collector) (rate(tailscale2otel_checkpoint_persist_errors_total[%s]))" % RI, legend="{{tailscale_collector}}")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 6, 6),
        (panel("Component errors/s", "timeseries",
               [prom_t("sum by (component) (rate(tailscale2otel_component_errors_total[%s]))" % RI, legend="{{component}}")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 6, 6),
        (panel("Admin auth rejected/s", "timeseries",
               [prom_t("sum by (reason) (rate(tailscale2otel_admin_auth_rejected_total[%s]))" % RI, legend="{{reason}}")],
               unit="cps", custom=ts_custom(), options=ts_opts()), 6, 6),
    ]
    return [row("Liveness & build", live), row("Collectors", collectors), row("API & export", api),
            row("API retries & export failures", api_cond, present="has_api_retry"),
            row("Cardinality & dedup", cardinality), row("Enrichment cache", enrich),
            row("Go runtime", runtime), row("Reliability", reliability, present="has_scrape_err")]


def tab_cardinality():
    OVF = "{otel_metric_overflow=\"true\", __name__=~\"tailscale.*\"}"
    overflow = [
        (panel("Metrics over cardinality cap", "stat",
               [prom_t("count(count by (__name__) (%s)) or vector(0)" % OVF, instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "red")]),
               options=stat_opts(color="background"),
               desc="Metric families that exceeded the per-metric series cap (cardinality.metric_limit, "
                    "default 10000) and are now collapsing excess series into one otel_metric_overflow "
                    "series — SILENT per-series detail loss. >0 means raise metric_limit or lower flow "
                    "cardinality (ephemeral source_port is the biggest driver)."), 6, 5),
        (panel("Busiest metric — % of cap", "stat",
               [prom_t("max(tailscale2otel_series_active) / 10000", instant=True)],
               unit="percentunit", min_=0, max_=1, thresholds=thr([(None, "green"), (0.8, "yellow"), (1, "red")]),
               options=stat_opts(color="background"),
               desc="Highest per-metric active-series count as a fraction of the 10k cap."), 6, 5),
        (panel("Total active series", "stat",
               [prom_t("sum(tailscale2otel_series_active)", instant=True)],
               unit="short", options=stat_opts(graph="area", color="value"),
               desc="Sum of active series across all tailscale2otel metrics (a proxy for ingest cost)."), 6, 5),
        (panel("Metric families tracked", "stat",
               [prom_t("count(tailscale2otel_series_active)", instant=True)],
               unit="short", options=stat_opts()), 6, 5),
        (panel("Overflowing families", "table",
               [prom_t("count by (__name__) (%s)" % OVF, instant=True, fmt="table")],
               novalue="No metrics over cap — all series fully resolved.",
               transformations=[organize(exclude=["Time", "Value", "job", "instance",
                                                   "service_instance_id", "service_name", "service_namespace"],
                                          rename={"__name__": "Metric"})],
               desc="Metric families currently over the per-metric cap (otel_metric_overflow=true)."), 24, 6),
    ]
    budget = [
        (panel("Active series vs 10k cap (top $topn)", "bargauge",
               [prom_t("topk($topn, max by (metric_name) (tailscale2otel_series_active))", legend="{{metric_name}}")],
               unit="short", max_=10000, thresholds=thr([(None, "green"), (8000, "yellow"), (10000, "red")]),
               options=bargauge_opts(),
               desc="Per-metric active series against the cap. Watch the flow families."), 12, 8),
        (panel("Active series over time (top $topn)", "timeseries",
               [prom_t("topk($topn, max by (metric_name) (tailscale2otel_series_active))", legend="{{metric_name}}")],
               unit="short", custom=ts_custom(), options=ts_opts(placement="right")), 12, 8),
    ]
    flow = [
        (panel("Flow series: raw vs bounded rollup", "timeseries",
               [prom_t("max(tailscale2otel_series_active{metric_name=\"tailscale.network.io\"})", legend="io raw"),
                prom_t("max(tailscale2otel_series_active{metric_name=\"tailscale.network.io.rollup\"})", legend="io rollup"),
                prom_t("max(tailscale2otel_series_active{metric_name=\"tailscale.network.packets\"})", legend="packets raw"),
                prom_t("max(tailscale2otel_series_active{metric_name=\"tailscale.network.packets.rollup\"})", legend="packets rollup")],
               unit="short", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Raw flow families saturate the 10k cap; the bounded rollup stays small. When raw is "
                    "at cap, trust the ROLLUP talker panels on the Network tab."), 12, 7),
        (panel("__other__ rollup share", "stat",
               [prom_t("(sum(rate(tailscale_network_io_rollup_bytes_total{tailscale_dst_node=\"__other__\"}[%s])) or vector(0)) / "
                       "clamp_min(sum(rate(tailscale_network_io_rollup_bytes_total[%s])), 1)" % (RI, RI), instant=True)],
               unit="percentunit", thresholds=thr([(None, "green"), (0.5, "yellow"), (0.8, "red")]),
               options=stat_opts(color="background"),
               desc="Fraction of rollup bytes folded into the bounded __other__ bucket. High = many small talkers."), 6, 7),
        (panel("Flow log records dropped/s", "timeseries",
               [prom_t("sum(rate(tailscale_network_flow_logs_dropped_total[%s])) or vector(0)" % RI, legend="dropped/s")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Flow LOG records suppressed by the per-window volume guard "
                    "(collectors.flowlogs.max_log_records_per_window). Metrics are never dropped, only logs."), 6, 7),
    ]
    dedup = [
        (panel("Dedup set size", "timeseries", [prom_t("max by (dedup_set) (tailscale2otel_dedup_size_ratio)", legend="{{dedup_set}}")],
               unit="short", custom=ts_custom(), options=ts_opts(),
               desc="Keys held in each cross-source de-duplication set (a count)."), 12, 6),
        (panel("Dedup evictions/s", "timeseries",
               [prom_t("sum by (dedup_set) (rate(tailscale2otel_dedup_evictions_total[%s]))" % RI, legend="{{dedup_set}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Sustained evictions mean a dedup set is undersized."), 12, 6),
    ]
    return [row("Cardinality cap & overflow", overflow), row("Series budget", budget),
            row("Flow cardinality drivers", flow), row("Cross-source dedup", dedup)]


def tab_security():
    AUD = "{service_name=\"tailscale2otel\"} | event_name=`tailscale.config.audit`"
    POS = lot("tailscale_device_posture_ratio", WIN_FAST)  # posture is emitted every scrape
    audit = [
        (panel("Audit actions over time", "timeseries",
               [loki_t("sum by (tailscale_audit_action) (count_over_time(%s [$__auto]))" % AUD,
                       legend="{{tailscale_audit_action}}")],
               unit="cps", custom=ts_custom(stack="normal", fill=30), options=ts_opts(placement="right")), 12, 7),
        (panel("Audit events (range)", "stat",
               [loki_t("sum(count_over_time(%s [$__range]))" % AUD, instant=True)],
               unit="short", novalue="0", options=stat_opts(color="value")), 6, 7),
        (panel("Failed changes — WARN (range)", "stat",
               # severity field is severity_text (value "INFO"/"WARN"), verified live — NOT `severity`.
               # novalue="0": LogQL count_over_time over an empty match yields no series (not 0),
               # so show 0 rather than "No data" on a healthy tailnet with no WARN audits.
               [loki_t("sum(count_over_time(%s | severity_text=`WARN` [$__range]))" % AUD, instant=True)],
               unit="short", novalue="0", thresholds=thr([(None, "green"), (1, "red")]), options=stat_opts(color="background"),
               desc="Audit events emitted at WARN (the event carried an error)."), 6, 7),
        # Rendered as timeseries, not barchart — this dashboard has no Loki barchart
        # precedent (all barcharts are Prometheus instant+table); the proven Loki
        # aggregation pattern here is the range timeseries (see "Log volume by event").
        (panel("Top $topn actors over time", "timeseries",
               [loki_t("topk($topn, sum by (tailscale_actor_login) "
                       "(count_over_time(%s | tailscale_actor_login != `` [$__auto])))" % AUD,
                       legend="{{tailscale_actor_login}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right")), 12, 8),
        (panel("Top $topn targets over time", "timeseries",
               [loki_t("topk($topn, sum by (tailscale_target_name) "
                       "(count_over_time(%s | tailscale_target_name != `` [$__auto])))" % AUD,
                       legend="{{tailscale_target_name}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right")), 12, 8),
        (panel("Audit events by target type", "timeseries",
               [loki_t("sum by (tailscale_target_type) "
                       "(count_over_time(%s | tailscale_target_type != `` [$__auto]))" % AUD,
                       legend="{{tailscale_target_type}}")],
               unit="cps", custom=ts_custom(stack="normal"), options=ts_opts()), 24, 7),
        (panel("Recent configuration changes", "logs",
               [loki_t("%s |~ `$log_filter`" % AUD, maxlines=200)],
               options=logs_opts(), desc="Live audit stream; filter with the Log filter variable."), 24, 10),
    ]
    posture = [
        # The {label=...} selector MUST be INSIDE last_over_time(...) — appending a
        # matcher to a function result (lot(x){...}) is a PromQL parse error.
        (panel("Auto-update coverage", "stat",
               [prom_t("count(%s) / clamp_min(count(%s), 1)"
                       % (lot("tailscale_device_posture_ratio{auto_update=\"true\"}", WIN_FAST),
                          lot("tailscale_device_posture_ratio", WIN_FAST)), instant=True)],
               unit="percentunit", min_=0, max_=1, thresholds=thr([(None, "red"), (0.8, "yellow"), (0.95, "green")]),
               options=stat_opts(color="background"),
               desc="Fraction of devices with Tailscale client auto-update enabled."), 6, 6),
        (panel("State-encryption coverage", "stat",
               [prom_t("count(%s) / clamp_min(count(%s), 1)"
                       % (lot("tailscale_device_posture_ratio{encrypted=\"true\"}", WIN_FAST),
                          lot("tailscale_device_posture_ratio", WIN_FAST)), instant=True)],
               unit="percentunit", min_=0, max_=1, thresholds=thr([(None, "red"), (0.8, "yellow"), (0.95, "green")]),
               options=stat_opts(color="background"),
               desc="Fraction of devices reporting an encrypted local state store."), 6, 6),
        (panel("Devices needing update", "stat",
               [prom_t("count(%s == 1) or vector(0)" % lot("tailscale_device_update_available_ratio"), instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]), options=stat_opts(color="background")), 6, 6),
        (panel("Release track split", "barchart",
               [prom_t("count by (track) (max by (track, host_id) (%s))" % POS,
                       legend="{{track}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 6, 6),
        (panel("Client version distribution", "barchart",
               [prom_t("count by (ts_version) (max by (ts_version, host_id) (%s))" % POS,
                       legend="{{ts_version}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 24, 7),
    ]
    expiry = [
        (panel("Device keys ≤7d", "stat",
               [prom_t("count((%s - time() < 7*86400) and (%s - time() > 0)) or vector(0)"
                       % (lot("tailscale_device_key_expiry_seconds", WIN_SLOW), lot("tailscale_device_key_expiry_seconds", WIN_SLOW)), instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow"), (3, "red")]), options=stat_opts(color="background")), 6, 5),
        (panel("Device keys ≤30d", "stat",
               [prom_t("count((%s - time() < 30*86400) and (%s - time() > 0)) or vector(0)"
                       % (lot("tailscale_device_key_expiry_seconds", WIN_SLOW), lot("tailscale_device_key_expiry_seconds", WIN_SLOW)), instant=True)],
               unit="short", options=stat_opts()), 6, 5),
        (panel("API/auth keys ≤7d", "stat",
               [prom_t("count((%s - time() < 7*86400) and (%s - time() > 0)) or vector(0)"
                       % (lot("tailscale_key_expiry_seconds", WIN_SLOW), lot("tailscale_key_expiry_seconds", WIN_SLOW)), instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]), options=stat_opts(color="background")), 6, 5),
        (panel("API/auth keys ≤30d", "stat",
               [prom_t("count((%s - time() < 30*86400) and (%s - time() > 0)) or vector(0)"
                       % (lot("tailscale_key_expiry_seconds", WIN_SLOW), lot("tailscale_key_expiry_seconds", WIN_SLOW)), instant=True)],
               unit="short", options=stat_opts()), 6, 5),
    ]
    posture_integ = [
        (panel("Integrations configured", "stat",
               [prom_t("max(%s) or vector(0)" % lot("tailscale_posture_integrations_count_ratio", WIN_SLOW), instant=True)],
               unit="short", options=stat_opts(color="value"),
               desc="Configured device-posture (MDM/EDR) integrations, e.g. Intune."), 6, 5),
        (panel("Devices matched by integration", "bargauge",
               [prom_t("max by (tailscale_posture_provider, tailscale_posture_integration) (%s)"
                       % lot("tailscale_posture_integration_matched_ratio", WIN_SLOW),
                       legend="{{tailscale_posture_provider}} / {{tailscale_posture_integration}}")],
               unit="short", options=bargauge_opts(),
               desc="Devices matched to a provider host by each posture integration."), 12, 5),
        (panel("Oldest sync age", "stat",
               [prom_t("max(time() - %s) or vector(0)" % sv(lot("tailscale_posture_integration_last_sync_seconds", WIN_SLOW)), instant=True)],
               unit="s", thresholds=thr([(None, "green"), (3600, "yellow"), (86400, "red")]),
               options=stat_opts(color="background"),
               desc="Time since the least-recently-synced integration last synced (alert on staleness)."), 6, 5),
        (panel("Integration sync detail", "table",
               [prom_t(sv(lot("tailscale_posture_integration_matched_ratio", WIN_SLOW)), instant=True, fmt="table", refid="A"),
                prom_t(sv(lot("tailscale_posture_integration_possible_matched_ratio", WIN_SLOW)), instant=True, fmt="table", refid="B"),
                prom_t(sv(lot("tailscale_posture_integration_provider_hosts_ratio", WIN_SLOW)), instant=True, fmt="table", refid="C"),
                prom_t("time() - %s" % sv(lot("tailscale_posture_integration_last_sync_seconds", WIN_SLOW)), instant=True, fmt="table", refid="D")],
               transformations=[merge(),
                                organize(exclude=["Time", "__name__", "job", "instance",
                                                  "service_instance_id", "service_name", "service_namespace"],
                                         rename={"tailscale_posture_provider": "Provider",
                                                 "tailscale_posture_integration": "Integration",
                                                 "Value #A": "Matched", "Value #B": "Possible",
                                                 "Value #C": "Provider hosts", "Value #D": "Last sync age"})],
               overrides=[{"matcher": {"id": "byName", "options": "Last sync age"},
                           "properties": [{"id": "unit", "value": "s"}]}],
               desc="Per integration: matched / possible-match / provider-host counts and sync age."), 24, 7),
    ]
    tlock = [
        (panel("Tailnet-lock errors", "stat",
               [prom_t("max(%s) or vector(0)" % lot("tailscale_tailnet_lock_errors_ratio", WIN_FAST), instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "red")]), options=stat_opts(color="background"),
               desc="Devices with a non-empty tailnet-lock error (e.g. an unsigned node). >0 means a "
                    "signing node must sign the affected keys."), 6, 6),
        (panel("Nodes with tailnet-lock errors", "logs",
               [loki_t("{service_name=\"tailscale2otel\"} | event_name=`tailscale.device.tailnet_lock_error` |~ `$log_filter`", maxlines=100)],
               options=logs_opts(), desc="Per-device tailnet-lock error events; the error text is the log body."), 18, 6),
    ]
    return [row("Configuration audit", audit, present="has_audit"),
            row("Posture integrations (MDM/EDR sync)", posture_integ, present="has_posture_integration"),
            row("Security posture", posture, present="has_posture"),
            row("Tailnet lock", tlock, present="has_tailnet_lock"),
            row("Key & access expiry risk", expiry)]


# ---------------------------------------------------------------------------
# assembly
# ---------------------------------------------------------------------------

def build(uid, title, flat, only=None, folder=None):
    global ELEMENTS, _id
    ELEMENTS = {}
    _id = 0
    variables = build_variables()

    tab_defs = [
        ("Overview", tab_overview, None),
        ("Fleet & Devices", tab_fleet, None),
        ("Network & Flows", tab_network, None),
        ("Events & Logs", tab_events, None),
        ("Security & Audit", tab_security, None),
        ("Policy & Config", tab_policy, None),
        ("Node Metrics", tab_nodemetrics, "has_nodemetrics"),
        ("Exporter Diagnostics", tab_diagnostics, None),
        ("Cardinality & Cost", tab_cardinality, None),
    ]
    if only:
        tab_defs = [d for d in tab_defs if d[0] == only]
        if not tab_defs:
            raise SystemExit("unknown tab: %s" % only)
        flat = True
    # build only the selected tabs so previews don't carry orphan elements from other tabs
    tabs = [(ttl, fn(), present) for (ttl, fn, present) in tab_defs]

    if flat:
        allrows = []
        for entry in tabs:
            ttl, rws = entry[0], entry[1]
            for r in rws:
                r2 = json.loads(json.dumps(r))
                r2["spec"]["title"] = "[%s] %s" % (ttl, r2["spec"].get("title", ""))
                allrows.append(r2)
        layout = {"kind": "RowsLayout", "spec": {"rows": allrows}}
    else:
        layout = {"kind": "TabsLayout", "spec": {"tabs": [tab(ttl, rws, present) for (ttl, rws, present) in tabs]}}

    spec = {
        "title": title,
        "description": "Comprehensive tailscale2otel observability — fleet, network flows "
                       "(rollup + raw), events & logs, policy/config, node metrics, and exporter "
                       "diagnostics. Dynamic: sections appear only when their data is present in "
                       "the target. Generated by deploy/grafana/gen/build.py.",
        "tags": ["tailscale", "tailscale2otel"],
        "editable": True, "liveNow": False, "preload": False, "cursorSync": "Crosshair",
        "timeSettings": {"from": "now-6h", "to": "now", "autoRefresh": "1m",
                         "autoRefreshIntervals": ["10s", "30s", "1m", "5m", "15m", "30m", "1h"],
                         "timezone": "browser", "hideTimepicker": False, "fiscalYearStartMonth": 0},
        "annotations": [{"kind": "AnnotationQuery", "spec": {
            "builtIn": True, "enable": True, "hide": True, "iconColor": "rgba(0, 211, 255, 1)",
            "name": "Annotations & Alerts",
            "query": {"kind": "DataQuery", "version": "v0", "group": "grafana",
                      "datasource": {"name": "-- Grafana --"}, "spec": {}}}}],
        "links": [], "variables": variables, "elements": ELEMENTS, "layout": layout,
    }
    meta = {"name": uid}
    if folder:
        meta["annotations"] = {"grafana.app/folder": folder}
    return {"apiVersion": "dashboard.grafana.app/v2", "kind": "Dashboard",
            "metadata": meta, "spec": spec}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", required=True)
    ap.add_argument("--uid", default="tailscale2otel")
    ap.add_argument("--title", default="Tailscale2OTel — Overview")
    ap.add_argument("--flat", action="store_true", help="emit a rows-only variant (no tabs) for full-page snapshots")
    ap.add_argument("--tab", help="emit a rows-only dashboard for just this tab (for focused snapshots)")
    ap.add_argument("--folder", default=None, help="pin to a Grafana folder UID via metadata annotation (omit for a portable, folder-agnostic artifact)")
    args = ap.parse_args()
    if args.tab:
        slug = "-".join("".join(c if c.isalnum() else " " for c in args.tab.lower()).split())
        uid = args.uid + "-prev-" + slug
        title = args.title + " — " + args.tab
    else:
        uid = args.uid + ("-flat" if args.flat else "")
        title = args.title + (" (flat)" if args.flat else "")
    doc = build(uid, title, args.flat, only=args.tab, folder=args.folder)
    with open(args.out, "w") as f:
        json.dump(doc, f, indent=2)
    print("wrote %s  (%d panels)" % (args.out, len(ELEMENTS)))


if __name__ == "__main__":
    main()
