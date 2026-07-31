#!/usr/bin/env python3
"""Generate the tailscale2otel Grafana dashboards (schema v2).

This emits the whole dashboard FAMILY — one artifact per entry in
dashboards.ALL — using the Grafana v2 dashboard schema
(`dashboard.grafana.app/v2`, Grafana 13+). It is "dashboards-as-code": edit the
generator, regenerate the JSON, push with gcx. The committed artifacts are the
JSON files; this generator is the source of truth.

Both dashboards are built in ONE invocation on purpose (#526): the signal
coverage gate takes the union across them, and a gate that could only see one
artifact would report a metric as missing the moment its panel moved to the
other file.

The apiVersion is load-bearing. `variables` on RowsLayoutRowSpec/TabsLayoutTabSpec
— the field that lets a sentinel live on the tab that consumes it instead of on
the dashboard — exists only in v2beta1 and v2. v2alpha1 has no such field at all,
so "simplifying" the apiVersion would silently drop every scoped variable.

See builder.py for the low-level builders and the schema/rendering rationale,
dashboards.py for what dashboards exist and what is on each, and the tabs/
package for each tab's panel definitions.

Usage:
    python3 build.py --out-dir .                      # write every artifact to its spec.out
    python3 build.py --dashboard health --flat --out /tmp/x.json   # rows-only, all tabs
    python3 build.py --dashboard tailnet --tab "Network & Flows" --out /tmp/x.json

`--flat`/`--tab` require `--dashboard`: "Overview" exists on both dashboards, so
without the selector a preview request is ambiguous.

Only the Python standard library is required.
"""

import argparse
import json
import os

import builder
import dashboards
from builder import tab, tab_group
from dashboards import DomainDef, SubTabbedDef, TabDef
from variables import build_variables, tab_controls


# ---------------------------------------------------------------------------
# assembly
# ---------------------------------------------------------------------------


# ---- annotation layers ---------------------------------------------------
#
# These read Grafana's own annotation STORE, not a datasource. tailscale2otel
# pushes the markers itself when `grafana_annotations.url` is configured
# (internal/annotations, #518), so there is no PromQL or LogQL here and nothing
# to keep in step with a metric name — the contract is the TAG SET, which
# internal/annotations.Annotation.Tags() owns:
#
#   tailscale2otel        on every annotation — the root selector
#   category:<c>          config_change | expiry | lifecycle
#   rule:<id>             the curated rule that produced it
#
# Degradation needs no conditionalRendering: a deployment that never enabled the
# writer has no annotations carrying the root tag, and a tag query with no
# matches renders nothing. Absent, not noisy.
#
# `matchAny: False` is load-bearing on the category layers — it ANDs the tags. With
# matchAny true they would each match every tailscale2otel annotation and the three
# layers would be identical.
def tag_annotation(name, tags, color, enable, hide):
    """One annotation layer over the Grafana annotation store, selected by tags."""
    return {"kind": "AnnotationQuery", "spec": {
        "builtIn": False, "enable": enable, "hide": hide, "iconColor": color, "name": name,
        "query": {"kind": "DataQuery", "version": "v0", "group": "grafana",
                  "datasource": {"name": "-- Grafana --"},
                  "spec": {"queryType": "tags", "matchAny": False, "tags": tags, "limit": 100}}}}


def annotation_layers():
    return [
        {"kind": "AnnotationQuery", "spec": {
            "builtIn": True, "enable": True, "hide": True, "iconColor": "rgba(0, 211, 255, 1)",
            "name": "Annotations & Alerts",
            "query": {"kind": "DataQuery", "version": "v0", "group": "grafana",
                      "datasource": {"name": "-- Grafana --"}, "spec": {}}}},
        # On by default and the only one that is: it is the whole timeline, and an
        # operator reading a step in a rate panel wants "something happened here"
        # before they want to know which category it was.
        tag_annotation("Tailnet events", ["tailscale2otel"],
                       "light-blue", enable=True, hide=False),
        # The two below are SUBSETS of the layer above, so they are off by default —
        # enabling one alongside it double-draws every marker it matches. They exist
        # for the case where a busy tailnet's config churn is burying everything else.
        tag_annotation("— config changes only", ["tailscale2otel", "category:config_change"],
                       "light-orange", enable=False, hide=False),
        tag_annotation("— key expiry only", ["tailscale2otel", "category:expiry"],
                       "light-yellow", enable=False, hide=False),
    ]


def _build_leaf(node, flat_rows=None):
    """Build one leaf TabDef into a TabsLayoutTab (or, for a flat preview, append
    its rows to flat_rows with the tab title prefixed onto each row title)."""
    rows = node.build(builder.tab_scope(node.title))
    if flat_rows is not None:
        for r in rows:
            r2 = json.loads(json.dumps(r))
            r2["spec"]["title"] = "[%s] %s" % (node.title, r2["spec"].get("title", ""))
            flat_rows.append(r2)
        return None
    # Two sources, one list: the base controls this leaf is the only reader of
    # (variables.TAB_CONTROLS, a static table) and the presence sentinels its own
    # module registered while building the rows above (the scoped registry).
    scoped = tab_controls(node.title) + builder.registered_sentinels(builder.tab_scope(node.title))
    return tab(node.title, rows, node.present, variables=scoped)


def _build_node(node, flat_rows=None, only=None):
    """Build one layout node — leaf, domain, or sub-tabbed leaf — recursively.

    Returns a TabsLayoutTab dict, or None when building flat or when `only`
    filtered every leaf underneath it out.
    """
    if isinstance(node, TabDef):
        if only is not None and node.title != only:
            return None
        return _build_leaf(node, flat_rows)
    children_defs = node.children
    built = [_build_node(c, flat_rows, only) for c in children_defs]
    built = [b for b in built if b is not None]
    if flat_rows is not None or not built:
        return None
    if isinstance(node, SubTabbedDef):
        # The fourth grouping level: a leaf that is itself a TabsLayout.
        return tab_group(node.title, built, node.present)
    return tab_group(node.title, built)


def build(spec, flat=False, only=None, folder=None):
    """Build one DashboardSpec into a complete v2 Dashboard document.

    Resets the element registry, the panel-id counter AND the sentinel registry
    first. The reset is not housekeeping: without it the second dashboard built
    in a process inherits the first's elements and continues its id counter, so
    the two artifacts would share panel ids while the per-file uniqueness test
    passed on each of them individually.
    """
    builder.ELEMENTS = {}
    builder._id = 0
    builder.reset_sentinels()

    if only is not None:
        known = {leaf.title for leaf in dashboards.leaves(spec)}
        if only not in known:
            raise SystemExit("unknown tab %r on %s; known: %s"
                             % (only, spec.uid, ", ".join(sorted(known))))
        flat = True

    flat_rows = [] if flat else None
    tabs = [_build_node(node, flat_rows, only) for node in spec.layout]
    tabs = [t for t in tabs if t is not None]

    if flat:
        layout = {"kind": "RowsLayout", "spec": {"rows": flat_rows}}
    else:
        layout = {"kind": "TabsLayout", "spec": {"tabs": tabs}}

    # Sentinels are declared as a side effect of building the tabs above (#495),
    # so this must run AFTER them, not before.
    variables = build_variables(spec)

    doc_spec = {
        "title": spec.title,
        "description": spec.description,
        "tags": list(spec.tags),
        "editable": True, "liveNow": False, "preload": False, "cursorSync": "Crosshair",
        "timeSettings": {"from": "now-6h", "to": "now", "autoRefresh": "1m",
                         "autoRefreshIntervals": ["10s", "30s", "1m", "5m", "15m", "30m", "1h"],
                         "timezone": "browser", "hideTimepicker": False, "fiscalYearStartMonth": 0},
        "annotations": annotation_layers(),
        "links": [builder.dashboard_link(spec.sibling_title, spec.sibling, spec.link_vars)],
        "variables": variables, "elements": builder.ELEMENTS, "layout": layout,
    }
    meta = {"name": spec.uid}
    if folder:
        meta["annotations"] = {"grafana.app/folder": folder}
    return {"apiVersion": "dashboard.grafana.app/v2", "kind": "Dashboard",
            "metadata": meta, "spec": doc_spec}


def build_family():
    """One synthetic document holding every panel the project ships.

    Most of the generator's test suites assert over "every panel we ship" — the
    palette and CVD rules, panel descriptions, unit consistency, the query
    budget, the semantic maps. Those questions are about the family, not about
    one artifact, and scoping them to a single file after the #526 split would
    silently stop covering whatever moved to the other one. This is NOT a
    shippable dashboard and is never written to disk: it exists so those suites
    keep asking their original question.

    Element names come from a per-dashboard counter, so the two artifacts both
    start at `panel-1`. Rather than renumber, the second dashboard's elements are
    namespaced and its ElementReferences rewritten to match, which keeps every
    reference resolvable inside the merged document.

    Suites that assert ARRANGEMENT (which domain a leaf sits under, how many
    panels a tab carries) must build one DashboardSpec instead — the merged
    layout is a concatenation and has no meaningful arrangement of its own.
    """
    merged = None
    for spec in dashboards.ALL:
        doc = build(spec)
        if merged is None:
            merged = doc
            continue
        prefix = spec.uid + "/"
        rename = {name: prefix + name for name in doc["spec"]["elements"]}

        def _rewrite(node):
            if isinstance(node, dict):
                if node.get("kind") == "ElementReference" and node.get("name") in rename:
                    node["name"] = rename[node["name"]]
                for v in node.values():
                    _rewrite(v)
            elif isinstance(node, list):
                for v in node:
                    _rewrite(v)

        _rewrite(doc["spec"]["layout"])
        for name, el in doc["spec"]["elements"].items():
            merged["spec"]["elements"][rename[name]] = el
        merged["spec"]["layout"]["spec"]["tabs"].extend(doc["spec"]["layout"]["spec"]["tabs"])
        have = {v["spec"]["name"] for v in merged["spec"]["variables"]}
        merged["spec"]["variables"].extend(
            v for v in doc["spec"]["variables"] if v["spec"]["name"] not in have)
    return merged


def _preview_spec(spec, flat, only):
    """A preview artifact must not claim the real uid — pushing it would
    overwrite the shipped dashboard."""
    if only:
        slug = "-".join("".join(c if c.isalnum() else " " for c in only.lower()).split())
        return spec._replace(uid=spec.uid + "-prev-" + slug,
                             title=spec.title + " — " + only)
    if flat:
        return spec._replace(uid=spec.uid + "-flat", title=spec.title + " (flat)")
    return spec


def main_argv(argv=None):
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", help="write a single (preview) artifact here")
    ap.add_argument("--out-dir", help="write every dashboard to its spec.out, rooted here")
    ap.add_argument("--dashboard", choices=sorted(dashboards.BY_KEY),
                    help="which dashboard --flat/--tab apply to")
    ap.add_argument("--flat", action="store_true",
                    help="emit a rows-only variant (no tabs) for full-page snapshots")
    ap.add_argument("--tab", help="emit a rows-only dashboard for just this tab")
    ap.add_argument("--folder", default=None,
                    help="pin to a Grafana folder UID via metadata annotation "
                         "(omit for a portable, folder-agnostic artifact)")
    args = ap.parse_args(argv)

    if args.flat or args.tab:
        if not args.dashboard:
            ap.error("--flat/--tab require --dashboard: a tab title such as "
                     "\"Overview\" exists on more than one dashboard, so the "
                     "request is otherwise ambiguous")
        if not args.out:
            ap.error("--flat/--tab write a single preview artifact; pass --out")
        spec = dashboards.BY_KEY[args.dashboard]
        doc = build(spec, args.flat, only=args.tab, folder=args.folder)
        preview = _preview_spec(spec, args.flat, args.tab)
        doc["metadata"]["name"] = preview.uid
        doc["spec"]["title"] = preview.title
        with open(args.out, "w") as f:
            json.dump(doc, f, indent=2)
        print("wrote %s  (%d panels)" % (args.out, len(builder.ELEMENTS)))
        return

    if not args.out_dir:
        ap.error("pass --out-dir to write every dashboard, or --dashboard with "
                 "--flat/--tab and --out for a preview")
    for spec in dashboards.ALL:
        doc = build(spec, folder=args.folder)
        path = os.path.join(args.out_dir, spec.out)
        with open(path, "w") as f:
            json.dump(doc, f, indent=2)
        print("wrote %s  (%d panels)" % (path, len(builder.ELEMENTS)))


def main():
    main_argv()


if __name__ == "__main__":
    main()
