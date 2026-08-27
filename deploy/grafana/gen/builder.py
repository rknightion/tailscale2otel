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


def acl_change_evidence(authoritative, observed, window=WIN_SLOW):
    """Age of the newest ACL audit event, with an explicitly labelled fallback."""
    audit = "max(%s)" % lot(authoritative, window)
    first = "time() - max(%s)" % lot(observed, window)
    return (
        'label_replace(time() - %s, "provenance", "audit event", "", "") or '
        'label_replace((%s) and on() absent(%s), "provenance", '
        '"revision first observed", "", "")'
    ) % (audit, first, audit)


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


# --- top-talker category axis (#391) -----------------------------------------
#
# A Prometheus instant query in `table` format returns one wide frame: a Time column,
# one column per label (including the OTLP resource labels), and Value. A barchart
# with no explicit xField takes the FIRST string field as its category axis, so an
# un-excluded Time or `instance` column silently becomes the category — which is
# exactly what the live top-talker panels rendered (#391: "no device labels, a
# time-like category"). Excluding the noise is not enough on its own: pin xField too,
# so the category is stated rather than inferred from column order.
BAR_NOISE = ["Time", "__name__", "job", "instance", "service_instance_id",
             "service_name", "service_namespace", "tailscale_tailnet",
             "tailscale2otel_provider"]


def category_bar_opts(xfield):
    """barchart_opts() with the category axis pinned to a named (post-transform) field."""
    opts = barchart_opts()
    opts["xField"] = xfield
    return opts


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


# ---------------------------------------------------------------------------
# sentinel (presence-variable) registry (#495)
# ---------------------------------------------------------------------------
#
# Feature-gated rows/tabs reference a hidden presence variable by name via
# row(present=...)/row(hide_when=...)/tab(present=...). Before #495 every
# sentinel was declared in one place (variables.py's build_variables()), so
# two agents adding gated rows to different tabs both had to edit that same
# function. Sentinels are now declared where they are consumed: each tab
# module calls sentinel()/pii_sentinel()/raw_sentinel() for the names ITS
# rows reference, as a side effect of building that tab (see tabs/*.py).
# build() resets the registry (alongside ELEMENTS/_id) before (re-)building
# the tabs; build_variables() collects everything registered afterwards.
#
# A sentinel consumed by more than one tab still needs exactly one declaring
# module — see the comment at each such call site for which tab won and why.

_SENTINEL_ORDER = [
    # The exact order build_variables() emitted these before #495 — frozen so
    # a pure declaration-order refactor cannot reorder the rendered variables
    # array. Extend at the END for a brand-new sentinel; never reorder
    # existing entries to match a new declaration site.
    "has_flows", "has_raw_flow", "has_rollup_flow", "has_unique", "has_posture",
    "has_routes", "has_derp", "has_nodemetrics", "has_stream", "has_webhook",
    "has_keys", "has_users_pe", "has_invites", "has_api_retry", "has_scrape_err",
    "is_degraded",
    "has_path", "has_audit", "has_posture_integration", "has_logstream",
    "has_services", "has_tailnet_lock", "has_derp_rollup", "has_connectivity",
    "has_exit", "has_subnet", "has_exit_io", "has_acl_risk", "has_audit_changes",
    "has_invites_dev", "has_key_scopes", "has_dns_resolver", "has_version_skew",
    "has_selfobs", "has_api_hist", "has_export_hist", "has_recv_dur", "has_ingest",
    "has_staleness", "has_pii", "has_key_expiry_hist", "has_rdns", "has_device_attr",
    "has_svc", "has_dropped", "has_node_curated",
    "has_device_flags", "has_multitailnet",
    "pii_node", "pii_perdevice", "pii_emails", "pii_usernames",
    "pii_actor", "pii_topology",
    # #462: Kubernetes-audit tab (tabs/k8saudit.py). Appended at the end per the
    # append-only rule — never reorder the entries above.
    "has_k8s_audit", "has_k8s_sensitive_reads", "has_k8s_exec_sessions",
    "has_k8s_mutations", "has_k8s_rbac_probes", "has_k8s_sessions",
    "has_k8s_schema_drift", "pii_command_text",
    # #526: the health dashboard's opt-in subsystems. Appended at the end per the
    # append-only rule. A name listed here but never registered is simply not
    # emitted — this list is an ORDERING allowlist, not a declaration — so it is
    # safe to reserve a name before the row that gates on it exists. The reverse
    # is not: registering a name absent from this list raises.
    "has_profiling", "has_tls", "has_annotations", "has_metrics_endpoint",
    "has_processor_queue", "has_log_truncation", "has_siem",
]

# --- scopes (#526) ----------------------------------------------------------
#
# A sentinel is registered against a SCOPE: the dashboard, or one leaf tab. The
# v2 schema carries `variables` on RowsLayoutRowSpec/TabsLayoutTabSpec as well as
# on the dashboard, so a presence variable consumed by rows in exactly one tab
# belongs on that tab. 66 of the 82 dashboard-level variables were hidden
# sentinels, 50 of them consumed by a single tab.
#
# Two rules, and neither is a matter of taste:
#   * a sentinel that gates a TAB must stay dashboard-level — a tab cannot gate
#     itself on a variable scoped to itself, because the variable only exists
#     once the tab renders;
#   * the same name may live in two different TAB scopes (the duplication rule
#     for a sentinel spanning <=2 tabs) but never at both dashboard and tab
#     scope, where a row's reference would resolve to whichever Grafana finds
#     first.


class _Scope:
    """An opaque sentinel-scope token. Compared by identity, so two scopes with
    the same title are the same scope (see tab_scope)."""

    __slots__ = ("title",)

    def __init__(self, title):
        self.title = title

    def __repr__(self):
        return "<scope %s>" % self.title


DASHBOARD = _Scope("__dashboard__")

_tab_scopes = {}


def tab_scope(title):
    """The scope token for the leaf tab titled `title`.

    Memoized: tab modules are re-executed on every build() call, so a fresh token
    per call would scatter one tab's sentinels across two scopes.
    """
    if title not in _tab_scopes:
        _tab_scopes[title] = _Scope(title)
    return _tab_scopes[title]


_sentinels = {}  # (scope, name) -> QueryVariable dict
_scopes_by_name = {}  # name -> list[_Scope], for the cross-scope collision rule


def reset_sentinels():
    """Clear the sentinel registry. Called at the start of build() alongside
    ELEMENTS/_id: tab modules re-declare their sentinels every build() call
    (they are ordinary statements inside tab_xxx(), executed each time that
    function runs), so without a reset a second build() in the same process
    — every test file here does this — would raise spurious "already
    registered" errors."""
    _sentinels.clear()
    _scopes_by_name.clear()


def _claim_sentinel(name, scope):
    """Reserve a sentinel name within `scope`, or raise.

    Must raise, not silently dedupe: a silent dedupe would let a name be
    registered twice with two different queries, with whichever call wins
    silently gating unrelated rows on the wrong metric.
    """
    if not isinstance(scope, _Scope):
        raise TypeError(
            "sentinel %r: scope must be builder.DASHBOARD or builder.tab_scope(...), "
            "got %r. It is required with no default so a tab module cannot forget "
            "it and silently re-create a dashboard-level variable." % (name, scope))
    if (scope, name) in _sentinels:
        raise ValueError(
            "sentinel %r is already registered in %r; two presence variables cannot "
            "share a name in one scope (give the second one its own)" % (name, scope))
    for other in _scopes_by_name.get(name, ()):
        if (other is DASHBOARD) != (scope is DASHBOARD):
            raise ValueError(
                "sentinel %r is registered at both dashboard scope and tab scope "
                "(%r and %r). A row referencing it would resolve to whichever "
                "declaration Grafana finds first — pick one scope." % (name, other, scope))
    _scopes_by_name.setdefault(name, []).append(scope)


def sentinel(name, metric, scope):
    """Register a hidden presence QueryVariable: non-empty when <metric> has series.

    Structural on purpose — callers pass a metric name, never a hand-written
    query string, so the query shape (label_values(...)) lives in exactly one
    place and no call site can invent a divergent one. Returns `name`, so a
    call site can optionally capture it: `HAS_X = sentinel("has_x", "...")`.
    """
    _claim_sentinel(name, scope)
    _sentinels[(scope, name)] = {"kind": "QueryVariable", "spec": {
        "name": name, "label": name, "hide": "hideVariable",
        "query": {"kind": "DataQuery", "version": "v0", "group": "",
                  "datasource": {"name": "${ds_prometheus}"},
                  "spec": {"query": "label_values(%s, __name__)" % metric, "refId": name}},
        "current": {"text": "", "value": ""}, "options": [], "multi": False,
        "includeAll": False, "allowCustomValue": False, "refresh": "onDashboardLoad",
        "regex": "", "skipUrlSync": True, "sort": "disabled"}}
    return name


def pii_sentinel(name, expr, scope):
    """Register a hidden PII-redaction sentinel: non-empty ONLY when <expr> returns
    series, i.e. when the redaction condition holds. Used with row(hide_when=[...])
    -> notMatches so panels hide only on explicit redaction and stay visible when
    the pii_filter gauge is absent."""
    _claim_sentinel(name, scope)
    _sentinels[(scope, name)] = {"kind": "QueryVariable", "spec": {
        "name": name, "label": name, "hide": "hideVariable",
        "query": {"kind": "DataQuery", "version": "v0", "group": "",
                  "datasource": {"name": "${ds_prometheus}"},
                  "spec": {"query": "query_result(%s)" % expr, "refId": name}},
        "current": {"text": "", "value": ""}, "options": [], "multi": False,
        "includeAll": False, "allowCustomValue": False, "refresh": "onDashboardLoad",
        "regex": "", "skipUrlSync": True, "sort": "disabled"}}
    return name


def raw_sentinel(name, query, scope):
    """Register a hidden presence sentinel from an already-formed query string.

    For the one case that is neither a plain metric-presence check nor a PII
    expr: has_multitailnet gates on ">1 distinct tailnet observed", not a
    metric existing, so its query has no single metric name to hand to
    sentinel().
    """
    _claim_sentinel(name, scope)
    _sentinels[(scope, name)] = {"kind": "QueryVariable", "spec": {
        "name": name, "label": name, "hide": "hideVariable",
        "query": {"kind": "DataQuery", "version": "v0", "group": "",
                  "datasource": {"name": "${ds_prometheus}"},
                  "spec": {"query": query, "refId": name}},
        "current": {"text": "", "value": ""}, "options": [], "multi": False,
        "includeAll": False, "allowCustomValue": False, "refresh": "onDashboardLoad",
        "regex": "", "skipUrlSync": True, "sort": "disabled"}}
    return name


def registered_sentinels(scope):
    """Every sentinel registered against `scope`, in the FROZEN historical order
    (_SENTINEL_ORDER) rather than registration order — registration order is now
    a function of which tabs got built and in what sequence, and must not leak
    into the emitted variables array (#495)."""
    unordered = sorted({n for (_s, n) in _sentinels if n not in _SENTINEL_ORDER})
    if unordered:
        raise ValueError(
            "sentinel(s) %r registered but missing from _SENTINEL_ORDER — add "
            "them there (at the end, unless intentionally matching a specific "
            "historical position)" % unordered)
    return [_sentinels[(scope, n)] for n in _SENTINEL_ORDER if (scope, n) in _sentinels]


def _gating(present, hide_when):
    """The conditionalRendering group for a row/tab, or None when it is ungated."""
    items = []
    if present:
        items.append(cond_item(present))
    for hv in (hide_when or []):
        # show UNLESS the redaction var is non-empty (==0 observed) -> hide-only-on-explicit-redaction
        items.append(cond_item(hv, op="notMatches"))
    return cond_group(items) if items else None


def _scoped(spec, variables):
    """Attach a grouping-level `variables` list, if there is one.

    Set only when non-empty, never as an empty list: `variables` exists on
    RowsLayoutRowSpec/TabsLayoutTabSpec ONLY in v2beta1 and v2 — v2alpha1 has no
    such field at all — so emitting [] everywhere would make an accidental
    apiVersion downgrade, which silently drops every scoped variable, impossible
    to detect from the artifact (#526).
    """
    if variables:
        spec["variables"] = variables
    return spec


def row(title, panel_specs, present=None, hide_when=None, collapse=False, variables=None):
    spec = {"title": title, "collapse": collapse, "layout": place(panel_specs)}
    gate = _gating(present, hide_when)
    if gate:
        spec["conditionalRendering"] = gate
    return {"kind": "RowsLayoutRow", "spec": _scoped(spec, variables)}


def autogrid_row(title, panel_names, present=None, hide_when=None, collapse=False,
                 max_columns=None, variables=None):
    """A row of N SAME-SIZE panels that reflows responsively (AutoGridLayout).

    `panel_names` is a flat list of element names — NOT the (name, w, h) triples
    row() takes. That is the entire point: the sizes are not stated, so Grafana
    reflows the row to the viewport (verified: 3 panels wrap to 2 columns at
    1400px). Use row() instead when the row has deliberate asymmetry — a wide
    timeseries beside two stats — because AutoGrid cannot express that and would
    flatten it to equal cells (#526 decision 6).
    """
    lay = {"items": [{"kind": "AutoGridLayoutItem",
                      "spec": {"element": {"kind": "ElementReference", "name": n}}}
                     for n in panel_names],
           "columnWidthMode": "standard", "rowHeightMode": "standard",
           "fillScreen": False}
    if max_columns is not None:
        lay["maxColumnCount"] = max_columns
    spec = {"title": title, "collapse": collapse,
            "layout": {"kind": "AutoGridLayout", "spec": lay}}
    gate = _gating(present, hide_when)
    if gate:
        spec["conditionalRendering"] = gate
    return {"kind": "RowsLayoutRow", "spec": _scoped(spec, variables)}


def dashboard_link(title, uid, link_vars):
    """A `spec.links` entry pointing at the sibling dashboard.

    Propagates the time range and the named variables, so an operator following
    "this panel is empty" -> "is the exporter broken?" lands on the same window
    and the same tailnet rather than a fresh default view. Before #526 the
    artifact shipped `spec.links: []` — no cross-navigation at all.
    """
    qs = "".join("&${%s:queryparam}" % v for v in link_vars)
    return {"title": title, "type": "link", "icon": "external link",
            "tooltip": "", "tags": [], "asDropdown": False, "targetBlank": False,
            "includeVars": True, "keepTime": True, "placement": "inControlsMenu",
            "url": "/d/%s?${__url_time_range}%s" % (uid, qs)}


def tab(title, rowlist, present=None, variables=None):
    spec = {"title": title, "layout": {"kind": "RowsLayout", "spec": {"rows": rowlist}}}
    if present:
        spec["conditionalRendering"] = cond_present(present)
    return {"kind": "TabsLayoutTab", "spec": _scoped(spec, variables)}


def tab_group(title, children, present=None, variables=None):
    """A top-level domain tab whose content is a second, NESTED TabsLayout (#495).

    `children` are complete `TabsLayoutTab` dicts already built via `tab()` — each keeps
    its own `conditionalRendering` untouched, so a leaf gated on a sentinel (e.g. Node
    Metrics on `has_nodemetrics`) stays gated identically whether it sits at the top
    level or nested inside a domain. `present` optionally gates the DOMAIN itself: a tab
    containing only conditional rows still renders unless the tab is itself conditional,
    and the same is true one level up — a domain whose every child is optional must be
    gated too, or Grafana leaves an empty top-level domain visible when none of its
    children fire. Do NOT pass `present` for a domain that carries any always-present
    child — that would hide core content behind a feature flag.
    """
    spec = {"title": title, "layout": {"kind": "TabsLayout", "spec": {"tabs": children}}}
    if present:
        spec["conditionalRendering"] = cond_present(present)
    return {"kind": "TabsLayoutTab", "spec": _scoped(spec, variables)}
