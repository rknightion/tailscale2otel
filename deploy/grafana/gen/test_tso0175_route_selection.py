#!/usr/bin/env python3
"""Route-selection mode is visible through a scoped, absence-preserving panel."""

import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("route_selection_dashboard", Path(__file__).with_name("build.py"))
dashboard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(dashboard)


class RouteSelectionPanelTests(unittest.TestCase):
    def test_mode_panel_is_scoped_and_does_not_zero_fill(self):
        doc = dashboard.build_family()
        panels = [p for p in doc["spec"]["elements"].values()
                  if p["spec"].get("title") == "Route-selection mode"]
        self.assertEqual(len(panels), 1)
        panel = panels[0]["spec"]
        expr = panel["data"]["spec"]["queries"][0]["spec"]["query"]["spec"]["expr"]
        self.assertIn("tailscale_setting_route_selection_ratio", expr)
        self.assertIn('tailscale_tailnet=~"$tailnet"', expr)
        self.assertIn('tailscale2otel_provider=~"$provider"', expr)
        self.assertNotIn(" or ", expr)
        for mode in ("active-passive-failover", "regional-routing", "regional-routing-failover", "magicroute", "unknown"):
            self.assertIn(mode, panel["description"])
        self.assertIn("tailscale_setting_route_selection", str(panel["data"]))


if __name__ == "__main__":
    unittest.main()
