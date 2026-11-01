"""Portable contracts over the real generated cardinality layout and queries."""
import json
from pathlib import Path
import unittest

import build
from test_query_budget import measure, _collect_leaf_tabs, _refs

ROOT = Path(__file__).resolve().parents[3]


class ConsolidatedCardinalityTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.doc = build.build(build.dashboards.HEALTH)
        cls.elements = cls.doc["spec"]["elements"]
        cls.panels = {e["spec"]["title"]: e["spec"] for e in cls.elements.values()}
        cls.tab = next(t for t in _collect_leaf_tabs(cls.doc)
                       if t["spec"]["title"] == "Cost & Cardinality")

    def query(self, title):
        queries = self.panels[title]["data"]["spec"]["queries"]
        self.assertEqual(len(queries), 1)
        return queries[0]["spec"]["query"]["spec"]

    def test_single_current_native_utilization_with_history(self):
        self.assertNotIn("Active series vs cap (top $topn)", self.panels)
        current = self.panels["Per-metric utilization (top-N)"]
        self.assertEqual(current["vizConfig"]["group"], "table")
        query = self.query(current["title"])
        self.assertTrue(query["instant"])
        self.assertFalse(query["range"])
        self.assertIn("max by (metric_name)", query["expr"])
        self.assertIn('tailscale2otel_series_limit{tailscale_tailnet=~"$tailnet"}) > 0', query["expr"])
        self.assertIn('"Utilization"', json.dumps(current["data"]["spec"]["transformations"]))
        same = [p for p in self.panels.values()
                if any(q["spec"]["query"]["spec"].get("instant") and
                       q["spec"]["query"]["spec"].get("expr") == query["expr"]
                       for q in p["data"]["spec"]["queries"])]
        self.assertEqual([p["title"] for p in same], [current["title"]])
        self.assertTrue(self.query("Active series over time (top $topn)")["range"])
        self.assertIn("Busiest metric — % of cap", self.panels)

    def test_overflow_sources_share_one_visible_diagnostic_row(self):
        titles = {"Metrics overflowing now", "Overflowing families"}
        rows = self.tab["spec"]["layout"]["spec"]["rows"]
        matching = [r for r in rows if titles.issubset(
            {self.elements[n]["spec"]["title"] for n in _refs(r)})]
        self.assertEqual(len(matching), 1)
        self.assertFalse(matching[0]["spec"]["collapse"])
        self.assertNotIn("conditionalRendering", matching[0]["spec"])
        self.assertIn("last_over_time(tailscale2otel_series_overflowing_ratio", self.query("Metrics overflowing now")["expr"])
        self.assertIn('otel_metric_overflow="true"', self.query("Overflowing families")["expr"])
        for title in titles:
            self.assertIn("disagree", self.panels[title]["description"])
        self.assertIn("Overflowing now", self.panels)
        self.assertIn("Metrics over cardinality cap", self.panels)

    def test_reduction_and_no_orphan_or_hidden_duplicate(self):
        tabs, totals = measure(self.doc)
        self.assertEqual(next(t for t in tabs if t[0] == "Cost & Cardinality")[1:], (20, 26))
        self.assertEqual((totals["panels"], totals["queries"]), (175, 244))
        refs = _refs(self.doc["spec"]["layout"])
        self.assertEqual(set(refs), set(self.elements))
        self.assertEqual(len(refs), len(set(refs)))
        self.assertEqual(len(self.panels), len(self.elements))
        self.assertIn("Active series by metric family", self.panels)
        self.assertIn("Metric families tracked", self.panels)
        self.assertIn("native table and time series remain available", self.panels["Active series by metric family"]["description"])

    def test_regenerated_alert_destinations(self):
        expected = {
            "ts2o-series-budget-high": "Per-metric utilization (top-N)",
            "ts2o-metric-cardinality-capped": "Metrics overflowing now",
            "ts2o-flow-logs-dropped": "Flow log records dropped/s",
            "ts2o-export-volume-high": "Ingest vs export cost (per minute)",
            "ts2o-nodemetrics-name-budget": "Forwarded metric-name drops/s by reason",
        }
        for name, title in expected.items():
            rule = json.loads((ROOT / "deploy/alerts/grafana-managed" / (name + ".json")).read_text())
            annotations = rule["spec"]["annotations"]
            self.assertEqual(annotations["__dashboardUid__"], self.doc["metadata"]["name"])
            self.assertEqual(str(annotations["__panelId__"]), str(self.panels[title]["id"]))


if __name__ == "__main__":
    unittest.main()
