"""Low-level builders for the tailscale2otel Grafana dashboard generator (schema v2).

This module holds the constants, query/panel/layout builders, and convenience option
blocks shared by every tab in the generator. It emits pieces of the v2 dashboard schema
(`dashboard.grafana.app/v2`, Grafana 13+). See build.py for the CLI entrypoint and the
overall "dashboards-as-code" rationale.

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
"""

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


PII = "tailscale2otel_pii_filter_category_ratio"  # PII filter self-obs gauge


# Tailnet/provider are now real per-series metric labels (roadmap item L, commit 6cfbb52)
# — emitted as metric data-point attributes, not OTEL Resource attributes. So panels filter
# `tailscale_tailnet`/`tailscale2otel_provider` directly with no `target_info` join. The
# former tn_join() helper (and its group_left target_info dance) is gone; just put the label
# matcher in the metric selector. For enumerating tailnets where no single metric is
# guaranteed present, match any per-tailnet series: {__name__=~"tailscale_.+", tailscale_tailnet!=""}.


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
    """Tempo query (PanelQuery-wrapped, same shape as prom_t/loki_t so panel() can
    consume it). query_type 'traceql' (trace list/table) or 'traceqlSearch'; for
    TraceQL-metrics timeseries set query like '{...} | rate() by (...)'."""
    return {"kind": "PanelQuery", "spec": {"refId": refid, "hidden": False,
            "query": {"kind": "DataQuery", "version": "v0", "group": "",
                      "datasource": {"name": "${ds_tempo}"},
                      "spec": {"query": query, "queryType": query_type, "tableType": table_type}}}}


def thr(steps, mode="absolute"):
    return {"mode": mode, "steps": [{"value": v, "color": c} for (v, c) in steps]}


def vmap(d):
    return [{"type": "value", "options": d}]


def check_bool_polarity(title, mappings, thresholds):
    """Fail the build when a 0/1 value map contradicts the panel's own thresholds.

    A boolean panel states its polarity twice — once in the colours of its value map
    and once in its thresholds — and the two silently disagreeing is exactly how a
    healthy state came to render red (#385). Only the unambiguous shape is checked:
    a value map carrying both "0" and "1", against a two-step base/at-1 threshold.
    """
    steps = (thresholds or {}).get("steps") or []
    if len(steps) != 2 or steps[0]["value"] is not None or steps[1]["value"] != 1:
        return
    for m in mappings or []:
        if m.get("type") != "value":
            continue
        opts = m.get("options") or {}
        if "0" not in opts or "1" not in opts:
            continue
        for (key, step) in (("0", steps[0]), ("1", steps[1])):
            if opts[key]["color"] != step["color"]:
                raise ValueError(
                    "panel %r: value map colours %s for %s but its threshold says %s — "
                    "pick the semantic map that matches the panel's polarity (see maps.py)"
                    % (title, opts[key]["color"], key, step["color"]))


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
    check_bool_polarity(title, mappings, thresholds)
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


def derp_byte_fraction(by=""):
    """Fraction of bytes relayed via DERP, robust to asymmetric inbound-/outbound-only series.

    `rate(in)+rate(out)` is a one-to-one join on all shared labels, so a node/path present in only
    one direction (asymmetric relay traffic) is silently dropped before the sum. Instead union the
    two directions (disambiguated with a synthetic `dir` label via label_replace) then sum, so no
    series is lost. Numerator restricts to path="derp"; denominator is all paths. `by` adds a
    grouping label (e.g. "tailscale_node") for the per-node breakdown; empty = fleet-wide."""
    grp = ("by (%s) " % by) if by else ""
    def _u(sel):
        return ('sum %s(label_replace(rate(tailscaled_inbound_bytes_total%s[%s]), "dir", "in", "", "") '
                'or label_replace(rate(tailscaled_outbound_bytes_total%s[%s]), "dir", "out", "", ""))'
                % (grp, sel, RI, sel, RI))
    return '%s / clamp_min(%s, 1)' % (_u('{path="derp"}'), _u(''))


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
