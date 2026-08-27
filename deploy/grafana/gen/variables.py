"""Dashboard template-variable builders (moved out of build.py in the module split).

Presence sentinels (has_*/pii_*) are no longer declared here (#495) — each tab
module declares the sentinels ITS rows consume, via builder.sentinel() /
builder.pii_sentinel() / builder.raw_sentinel(), as a side effect of building
that tab (see tabs/*.py). This module only builds the base variables
(datasources, topn, the query filters, log event/filter) and appends
whatever sentinels got registered, via builder.registered_sentinels().
"""

from builder import (DASHBOARD, PROM_DS_TEXT, PROM_DS_VALUE, LOKI_DS_TEXT, LOKI_DS_VALUE,
                     TEMPO_DS_TEXT, TEMPO_DS_VALUE, PYROSCOPE_DS_TEXT,
                     PYROSCOPE_DS_VALUE, registered_sentinels)


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


def adhoc_var(name, label, base_filters, ds="${ds_prometheus}", group="prometheus"):
    """An AdhocVariable — one free-form label filter replacing several dropdowns.

    `base_filters` is a list of (key, operator, value) tuples pinning the variable
    to a metric family, so the key/value suggestions an operator gets are the ones
    that mean something for that family rather than every label in the TSDB.

    SHAPE TRAP, and the reason this helper exists at all: AdhocVariableKind puts
    `datasource` and a REQUIRED `group` at the KIND level, as siblings of `spec` —
    unlike QueryVariable, which nests its datasource inside the query. Putting
    either inside `spec` produces a 422 whose CUE disjunction error names
    `layout.kind` and never mentions the variable, so the real cause is masked;
    bisect from a known-valid fragment if a v2 push ever 422s (#526).
    """
    return {"kind": "AdhocVariable",
            "group": group,
            "datasource": {"name": ds},
            "spec": {
                "name": name, "label": label, "description": "",
                "hide": "dontHide", "skipUrlSync": False, "allowCustomValue": True,
                "defaultKeys": [],
                "baseFilters": [{"key": k, "operator": op, "value": v}
                                for (k, op, v) in base_filters],
                "filters": []}}


def textbox_var(name, label):
    return {"kind": "TextVariable", "spec": {
        "name": name, "label": label, "current": {"text": "", "value": ""},
        "hide": "dontHide", "query": "", "skipUrlSync": False}}


# --- the base controls, one builder each -----------------------------------
#
# Named rather than inlined into a single list because #526 places each one
# either on a dashboard or on a specific tab, and a builder can be called from
# both tables below without the definition being duplicated.

def _ds_prometheus():
    return ds_var("ds_prometheus", "Prometheus", "prometheus", PROM_DS_TEXT, PROM_DS_VALUE)


def _ds_loki():
    return ds_var("ds_loki", "Loki", "loki", LOKI_DS_TEXT, LOKI_DS_VALUE)


def _ds_tempo():
    return ds_var("ds_tempo", "Tempo", "tempo", TEMPO_DS_TEXT, TEMPO_DS_VALUE)


def _ds_pyroscope():
    return ds_var("ds_pyroscope", "Pyroscope", "grafana-pyroscope-datasource",
                  PYROSCOPE_DS_TEXT, PYROSCOPE_DS_VALUE)


def _topn():
    return custom_var("topn", "Top N",
                      [("5", "5"), ("10", "10"), ("15", "15"), ("20", "20"), ("30", "30")],
                      "10", "10")


def _tailnet():
    return query_var("tailnet", "Tailnet",
                     'label_values({__name__=~"tailscale_.+", tailscale_tailnet!=""}, tailscale_tailnet)')


def _provider():
    return query_var("provider", "Provider",
                     'label_values({__name__=~"tailscale.+", tailscale2otel_provider!=""}, tailscale2otel_provider)')


def _collector():
    return query_var("collector", "Collector",
                     "label_values(tailscale2otel_scrape_success_ratio, tailscale_collector)")


def _os_type():
    return query_var("os_type", "OS", "label_values(tailscale_device_online_ratio, os_type)")


def _host_name():
    return query_var("host_name", "Host",
                     "label_values(tailscale_device_online_ratio{os_type=~\"$os_type\"}, host_name)")


def _device_filters():
    """The hybrid of #526 decision 3.

    `os_type` and `host_name` stay explicit dropdowns because an operator picks
    from them constantly and a known, short option list beats free-form typing.
    `device_user`, `device_tag` and `posture_attr` were three more dropdowns that
    between them answered one question — "which slice of the fleet?" — and they
    fold into this single adhoc filter, whose baseFilters pin the key/value
    suggestions to the device metric family so the operator is offered labels
    that mean something here rather than every label in the TSDB.
    """
    return adhoc_var("device_filters", "Device filters",
                     [("__name__", "=~", "tailscale_device_.*")])


def _net_transport():
    return query_var("net_transport", "Transport",
                     "label_values(tailscale_network_flows_total, network_transport)")


def _traffic_type():
    return query_var("traffic_type", "Traffic type",
                     "label_values(tailscale_network_flows_total, tailscale_traffic_type)")


def _log_event():
    return custom_var("log_event", "Log event",
                      [("All", ".+"), ("audit", "tailscale.config.audit"),
                       ("flow", "tailscale.network.flow"), ("posture", "tailscale.device.posture"),
                       ("key expiring", "tailscale.key.expiring"),
                       ("webhook", "tailscale.webhook.*")], "All", ".+")


def _log_filter():
    return textbox_var("log_filter", "Log filter")


# --- where each control lives ----------------------------------------------
#
# The single dashboard shipped all 16 on every page. #526 puts a control on the
# dashboard only when it is read from three or more tabs; anything narrower goes
# on the tab (or the two tabs) that read it. Grafana resolves a tab-scoped
# variable PER TAB — verified live, by rendering two tabs that declared the same
# name over opposite-truth queries and confirming each row gated on its own — so
# duplicating a name across two tabs is safe and is not a collision.

DASHBOARD_CONTROLS = {
    # Product: the five the whole tailnet view is read through, plus the log
    # filter, which every per-signal log stream on every domain tab honours.
    "tailscale2otel-tailnet": (_ds_prometheus, _ds_loki, _topn, _tailnet, _provider, _log_filter),
    # Health: the three datasources its panels query plus the two dimensions
    # they split by. ds_tempo and collector left the product dashboard entirely
    # — they were only ever read by the old Exporter Diagnostics tab, which is
    # what this dashboard replaced.
    "tailscale2otel-health": (_ds_prometheus, _ds_loki, _ds_tempo, _ds_pyroscope,
                               _collector, _tailnet),
}

# Leaf tab title -> the controls scoped to it. A title appearing here must be a
# leaf on exactly one dashboard; build.py raises if a scoped control collides
# with a dashboard-level one, because the same name would then mean two
# different things depending on which tab you were standing on.
TAB_CONTROLS = {
    # -- tailscale2otel-tailnet --------------------------------------------
    "Inventory & Hygiene": (_os_type, _host_name, _device_filters),
    "Posture & Security": (_os_type, _host_name, _device_filters),
    "Connectivity & Routing": (_os_type, _host_name, _device_filters),
    "Network & Flows": (_net_transport, _traffic_type),
    "Audit Trail": (_log_event,),
    # -- tailscale2otel-health ---------------------------------------------
    #
    # The health dashboard keeps five controls at dashboard level. These three
    # are read by exactly one of its tabs each, so they live there instead of
    # widening that bar. Each arrived with a panel that #526 moved onto this
    # dashboard from one where the variable was global — a query keeps its
    # `$var` reference when it moves, and the variable does not follow it.
    "Ingestion": (_provider,),          # the audit-pipeline rows, ex-Events & Logs
    "Delivery": (_log_filter,),         # the SIEM delivery-error log panel
    "Cost & Cardinality": (_topn,),     # the top-N series tables
}


def tab_controls(title):
    """The base controls scoped to leaf tab `title` (never sentinels — build.py
    appends those separately, from the registry that tab's module wrote to)."""
    return [make() for make in TAB_CONTROLS.get(title, ())]


def build_variables(spec):
    """The DASHBOARD-LEVEL variables for `spec`.

    Base controls this dashboard reads from three or more tabs, plus whatever
    sentinels its tab modules registered against builder.DASHBOARD — which after
    #526 is only the handful that gate a whole TAB, since a tab cannot gate
    itself on a variable scoped to itself.
    """
    v = [make() for make in DASHBOARD_CONTROLS[spec.uid]]
    v.extend(registered_sentinels(DASHBOARD))
    return v
