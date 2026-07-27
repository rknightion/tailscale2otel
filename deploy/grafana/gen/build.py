#!/usr/bin/env python3
"""Generate the comprehensive tailscale2otel Grafana dashboard (schema v2).

This emits a single multi-tab dashboard using the Grafana v2 dashboard schema
(`dashboard.grafana.app/v2`, Grafana 13+). It is "dashboards-as-code": edit this
file, regenerate the JSON, and push with gcx. The committed artifact is the JSON
(see ../tailscale2otel.json); this generator is the source of truth.

See builder.py for the low-level builders and the schema/rendering rationale, and
the tabs/ package for each tab's panel definitions.

Usage:
    python3 build.py --out ../tailscale2otel.json
    python3 build.py --flat --out /tmp/ts2_flat.json          # rows-only (all tabs) for full-page snapshots
    python3 build.py --tab "Network & Flows" --out /tmp/x.json # rows-only single tab for focused snapshots

Only the Python standard library is required.
"""

import argparse
import json

import builder
from builder import tab
from variables import build_variables

from tabs.overview import tab_overview
from tabs.fleet import tab_fleet
from tabs.network import tab_network
from tabs.events import tab_events
from tabs.security import tab_security
from tabs.policy import tab_policy
from tabs.nodemetrics import tab_nodemetrics
from tabs.tailnets import tab_tailnets
from tabs.diagnostics import tab_diagnostics
from tabs.cardinality import tab_cardinality


# ---------------------------------------------------------------------------
# domain grouping (#495) — nested sub-tab navigation
# ---------------------------------------------------------------------------
#
# Ten flat top-level leaf tabs is too many for one row of tab labels at 405 panels, so
# the leaves are grouped under a smaller set of top-level DOMAINS, each carrying a
# second TabsLayout nested inside its TabsLayoutTab (builder.tab_group()). A domain
# with exactly one child gets no pointless single-item sub-tab bar — its one leaf is
# promoted straight to the top level instead (see STANDALONE_LEAVES below and
# "Events & Logs" in particular, which is the only domain candidate this applies to).
#
# Order here is the rendered top-level tab order. Within DOMAIN_GROUPS, leaf order is
# the rendered sub-tab order for that domain.
DOMAIN_GROUPS = [
    ("Fleet & Network", ("Fleet & Devices", "Network & Flows", "Node Metrics", "Tailnets")),
    ("Security & Policy", ("Security & Audit", "Policy & Config")),
    ("Exporter", ("Exporter Diagnostics", "Cardinality & Cost")),
]

# Leaves that stay top-level tabs in their own right: Overview never had siblings to
# begin with, and Events & Logs is the one otherwise-plausible domain ("Ingestion")
# that would have exactly one child — folded flat rather than nested.
STANDALONE_LEAVES = ("Overview", "Events & Logs")


def organize_tabs(leaf_tabs):
    """Arrange the flat dict of built leaf TabsLayoutTab dicts (keyed by title) into
    the top-level tab list: standalone leaves stay as-is, grouped leaves are wrapped
    in a domain via builder.tab_group(). Title-keyed on purpose (see builder.tab_group's
    docstring) — a renamed, duplicate, or unassigned leaf is a build failure here
    rather than a silently dropped or duplicated tab."""
    remaining = dict(leaf_tabs)
    # Fixed rendered order: Overview, Fleet & Network, Security & Policy, Events & Logs,
    # Exporter — domains interleaved with the remaining standalone leaf in that order.
    top = [remaining.pop("Overview")]
    top.append(builder.tab_group("Fleet & Network",
                                  [remaining.pop(t) for t in DOMAIN_GROUPS[0][1]]))
    top.append(builder.tab_group("Security & Policy",
                                  [remaining.pop(t) for t in DOMAIN_GROUPS[1][1]]))
    top.append(remaining.pop("Events & Logs"))
    top.append(builder.tab_group("Exporter",
                                  [remaining.pop(t) for t in DOMAIN_GROUPS[2][1]]))
    if remaining:
        raise ValueError("unassigned dashboard leaf tab(s): %r" % sorted(remaining))
    return top


# ---------------------------------------------------------------------------
# assembly
# ---------------------------------------------------------------------------

def build(uid, title, flat, only=None, folder=None):
    builder.ELEMENTS = {}
    builder._id = 0
    builder.reset_sentinels()

    tab_defs = [
        ("Overview", tab_overview, None),
        ("Fleet & Devices", tab_fleet, None),
        ("Network & Flows", tab_network, None),
        ("Events & Logs", tab_events, None),
        ("Security & Audit", tab_security, None),
        ("Policy & Config", tab_policy, None),
        ("Node Metrics", tab_nodemetrics, "has_nodemetrics"),
        ("Tailnets", tab_tailnets, "has_multitailnet"),
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
    # Sentinels are declared as a side effect of the fn() calls above (#495), so
    # build_variables() must run AFTER tabs are built, not before.
    variables = build_variables()

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
        leaf_tabs = {ttl: tab(ttl, rws, present) for (ttl, rws, present) in tabs}
        layout = {"kind": "TabsLayout", "spec": {"tabs": organize_tabs(leaf_tabs)}}

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
        "links": [], "variables": variables, "elements": builder.ELEMENTS, "layout": layout,
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
    print("wrote %s  (%d panels)" % (args.out, len(builder.ELEMENTS)))


if __name__ == "__main__":
    main()
