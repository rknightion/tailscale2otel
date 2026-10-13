#!/usr/bin/env python3
"""Route-mode table labels the retained two-hour observations, not current state."""

import copy
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("route_history_dashboard", Path(__file__).with_name("build.py"))
dashboard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(dashboard)


class RouteHistoryPanelTests(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build_family()
        self.panel = next(p["spec"] for p in self.doc["spec"]["elements"].values()
                          if p["spec"].get("title") == "Route-selection mode")

    def test_explicit_historical_meaning(self):
        description = self.panel["description"]
        for phrase in ("Historical observations", "last two hours", "not a current-state inventory",
                       "prior and current modes together", "only a prior mode",
                       "clears obsolete runtime series", "stored history"):
            self.assertIn(phrase, description)
        self.assertIn("No route-selection observations in the last two hours", str(self.panel))

    def test_query_window_scope_and_table_contract_unchanged(self):
        query = self.panel["data"]["spec"]["queries"][0]["spec"]["query"]["spec"]
        self.assertEqual(query["expr"], 'last_over_time(tailscale_setting_route_selection_ratio{tailscale_tailnet=~"$tailnet", tailscale2otel_provider=~"$provider"}[2h])')
        self.assertTrue(query["instant"])
        self.assertEqual(query["format"], "table")
        self.assertEqual(self.panel["title"], "Route-selection mode")

    def test_only_history_copy_differs_from_published_panel(self):
        # A generated baseline guards all options, layout, scope and predecessor OS pie settings.
        import json
        baseline = json.loads(Path(__file__).parents[1].joinpath("tailscale2otel-tailnet.json").read_text())
        actual = copy.deepcopy(dashboard.build(next(
            s for s in dashboard.dashboards.ALL if s.uid == baseline["metadata"]["name"])))
        for doc in (actual, baseline):
            for panel in doc["spec"]["elements"].values():
                if panel["spec"].get("title") == "Route-selection mode":
                    panel["spec"].pop("description")
                    panel["spec"]["vizConfig"]["spec"]["fieldConfig"]["defaults"].pop("noValue")
        self.assertEqual(actual, baseline)


if __name__ == "__main__":
    unittest.main()
