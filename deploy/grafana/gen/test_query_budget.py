#!/usr/bin/env python3
"""Explicit query-load budget for the flagship dashboard (#397).

#397 asks for a benchmark and a budget. The benchmark half that can be measured
honestly OFFLINE is the *static* query load: how many panels and datasource
queries each tab carries, and how many presence variables run before any tab is
even chosen. This module turns those measurements into a gate, so growth past the
budget is a deliberate decision someone edits a number for, rather than something
that accretes a panel at a time until the dashboard is slow.

What this module does NOT claim. It does not measure browser render time, memory,
or wall-clock query duration, and it cannot tell you whether a HIDDEN tab issues
its queries — that is a live-Grafana behaviour question (target: Grafana 13.2.0)
that needs devtools against a real stack, and the answer changes the real cost by
roughly 5x. Nothing here should be read as evidence that the dashboard is fast.
It is evidence that its static footprint is bounded and observed.

THE SENTINEL COST IS THE INTERESTING NUMBER. Every presence variable is a query
variable, so it runs on EVERY dashboard load regardless of which tab is open —
unlike panel queries, none of it is avoidable by tab lazy-loading. That makes the
sentinel count the dashboard's fixed admission cost, and the one number worth
watching most closely.

Budgets are deliberately set a little above today's measurements, not exactly at
them: a gate that fails on the next single panel added is a gate people delete.
When a budget is genuinely outgrown, RAISE IT IN A COMMIT that says why — that
commit is the record #397 asks for.
"""

import json
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[3]
DASHBOARD = ROOT / "deploy" / "grafana" / "tailscale2otel.json"


# --- the budget ------------------------------------------------------------
# Measured 2026-07-27: 405 panels, 484 queries, 58 sentinels, worst tab
# "Exporter Diagnostics" at 83 panels / 108 queries.

MAX_PANELS_TOTAL = 460
MAX_QUERIES_TOTAL = 560
MAX_SENTINELS = 70          # the fixed per-load cost — see the module docstring
MAX_PANELS_PER_TAB = 100
MAX_QUERIES_PER_TAB = 130


def _refs(spec, acc=None):
    acc = [] if acc is None else acc
    if isinstance(spec, dict):
        if spec.get("kind") == "ElementReference":
            acc.append(spec["name"])
        for v in spec.values():
            _refs(v, acc)
    elif isinstance(spec, list):
        for v in spec:
            _refs(v, acc)
    return acc


def _collect(doc, kind):
    out = []

    def walk(o):
        if isinstance(o, dict):
            if o.get("kind") == kind:
                out.append(o)
            for v in o.values():
                walk(v)
        elif isinstance(o, list):
            for v in o:
                walk(v)

    walk(doc["spec"])
    return out


def measure(doc):
    """(per-tab rows, totals) — the static query footprint."""
    els = doc["spec"]["elements"]
    tabs = []
    for tab in _collect(doc, "TabsLayoutTab"):
        panels = queries = 0
        for name in _refs(tab["spec"].get("layout", {})):
            el = els.get(name)
            if not el or el.get("kind") != "Panel":
                continue
            panels += 1
            queries += json.dumps(el).count('"expr"')
        tabs.append((tab["spec"].get("title", "?"), panels, queries))
    sentinels = sum(
        1 for v in doc["spec"].get("variables", [])
        if v["spec"].get("name", "").startswith(("has_", "pii_"))
    )
    return tabs, {
        "panels": sum(t[1] for t in tabs),
        "queries": sum(t[2] for t in tabs),
        "sentinels": sentinels,
    }


class QueryBudgetTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with open(DASHBOARD, encoding="utf-8") as f:
            cls.doc = json.load(f)
        cls.tabs, cls.totals = measure(cls.doc)

    def test_the_measurement_found_a_real_dashboard(self):
        # Guards the guard: a walker that silently returns nothing would make every
        # budget below pass vacuously, which is the failure mode of this whole idea.
        self.assertGreater(len(self.tabs), 5, "found suspiciously few tabs")
        self.assertGreater(self.totals["panels"], 300, "found suspiciously few panels")
        self.assertGreater(self.totals["queries"], self.totals["panels"],
                           "every panel should carry at least one query on average")

    def test_total_panel_budget(self):
        self.assertLessEqual(
            self.totals["panels"], MAX_PANELS_TOTAL,
            "flagship is over its panel budget (%d > %d). Raise MAX_PANELS_TOTAL in a commit "
            "that says why, or split a tab." % (self.totals["panels"], MAX_PANELS_TOTAL))

    def test_total_query_budget(self):
        self.assertLessEqual(
            self.totals["queries"], MAX_QUERIES_TOTAL,
            "flagship is over its query budget (%d > %d)."
            % (self.totals["queries"], MAX_QUERIES_TOTAL))

    def test_sentinel_budget_the_fixed_per_load_cost(self):
        # Sentinels are query variables: they run on EVERY load whatever tab is
        # open, so tab lazy-loading cannot amortise them away.
        self.assertLessEqual(
            self.totals["sentinels"], MAX_SENTINELS,
            "presence-variable count (%d) exceeds the budget (%d). Each one is a Prometheus "
            "query on every dashboard load regardless of which tab is open, so this is the "
            "dashboard's fixed admission cost — the number to be most careful with."
            % (self.totals["sentinels"], MAX_SENTINELS))

    def test_per_tab_budgets(self):
        for title, panels, queries in self.tabs:
            with self.subTest(tab=title):
                self.assertLessEqual(
                    panels, MAX_PANELS_PER_TAB,
                    "tab %r carries %d panels (budget %d)" % (title, panels, MAX_PANELS_PER_TAB))
                self.assertLessEqual(
                    queries, MAX_QUERIES_PER_TAB,
                    "tab %r issues %d queries (budget %d)" % (title, queries, MAX_QUERIES_PER_TAB))


if __name__ == "__main__":
    unittest.main()
