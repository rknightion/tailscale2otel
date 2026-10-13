"""Freeze the fleet/user/key Overview tiles; query/render proofs run separately.

Portable generator contracts deliberately require no promtool or browser install.
"""
import re
import unittest

import build


SUMMARY_TILES = ("Devices online / total / offline", "Users", "Device keys ≤7d")
MISSING = ("No fleet source — needs device inventory",
           "No user source — needs users collector",
           "No device key source — needs per-device inventory")


class FleetSummaryPresenceTest(unittest.TestCase):
    def setUp(self):
        self.doc = build.build(build.dashboards.TAILNET)
        overview = self.doc["spec"]["layout"]["spec"]["tabs"][0]
        names = set()

        def visit(node):
            if isinstance(node, dict):
                if node.get("kind") == "ElementReference":
                    names.add(node["name"])
                for value in node.values():
                    visit(value)
            elif isinstance(node, list):
                for value in node:
                    visit(value)

        visit(overview)
        self.panels = {e["spec"]["title"]: e["spec"]
                       for name, e in self.doc["spec"]["elements"].items()
                       if name in names}

    def test_frozen_named_tiles_have_neutral_explicit_absence(self):
        for title, missing in zip(SUMMARY_TILES, MISSING):
            defaults = self.panels[title]["vizConfig"]["spec"]["fieldConfig"]["defaults"]
            self.assertEqual(defaults.get("noValue"), missing)
            self.assertIn({"type": "special", "options": {"match": "null", "result":
                          {"text": missing, "color": "gray"}}}, defaults.get("mappings", []))

    def test_all_operands_obey_tailnet_and_provider(self):
        for title in SUMMARY_TILES:
            for q in self.panels[title]["data"]["spec"]["queries"]:
                expr = q["spec"]["query"]["spec"]["expr"]
                self.assertNotIn("vector(0)", expr)
                self.assertNotIn("tailscale2otel_up", expr)
                self.assertEqual(expr.count("last_over_time("),
                                 expr.count('tailscale_tailnet=~"$tailnet"'))
                self.assertEqual(expr.count("last_over_time("),
                                 expr.count('tailscale2otel_provider=~"$provider"'))

    def test_empty_population_requires_successful_actual_listing(self):
        for title in SUMMARY_TILES:
            for q in self.panels[title]["data"]["spec"]["queries"]:
                expr = q["spec"]["query"]["spec"]["expr"]
                operation = "listUsers" if title == "Users" else "listTailnetDevices"
                self.assertIn('tailscale_api_operation="%s"' % operation, expr)
                self.assertIn('tailscale_api_state="supported"', expr)
                collector = "users" if title == "Users" else "devices"
                # Bind the predicate to the actual listing operand, not the
                # online-device predicate elsewhere in this same expression.
                source = (
                    'tailscale2otel_api_availability_ratio{'
                    'tailscale_tailnet=~"$tailnet", tailscale2otel_provider=~"$provider", '
                    'tailscale_collector="%s", tailscale_api_operation="%s", '
                    'tailscale_api_state="supported"}' % (collector, operation))
                window = "2h" if title in ("Users", "Device keys ≤7d") else "10m"
                success = "0 * count(last_over_time(%s[%s]) == 1)" % (source, window)
                with self.subTest(title=title, expr=expr):
                    self.assertRegex(
                        expr, re.escape(success),
                        "listing-specific supported-state success filter is required; "
                        "supported=0 must not manufacture an empty-population zero")
                if title != "Users":
                    self.assertIn("unless count(last_over_time(tailscale_devices_count_ratio", expr)
                    # Nonempty fleet with per-device metrics off must NOT become zero.

    def test_normal_user_dedup_and_key_window_are_preserved(self):
        users = self.panels["Users"]["data"]["spec"]["queries"][0]["spec"]["query"]["spec"]["expr"]
        self.assertIn("sum(max by (tailscale_user_role, tailscale_user_status, tailscale_user_type)", users)
        keys = self.panels["Device keys ≤7d"]["data"]["spec"]["queries"][0]["spec"]["query"]["spec"]["expr"]
        self.assertIn("- time() < 7*86400", keys)
        self.assertIn("- time() > 0", keys)


if __name__ == "__main__":
    unittest.main()
