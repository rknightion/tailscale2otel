"""Freeze Overview's always-applicable summary tiles and their absence contract.

Query execution and Grafana rendering are separate retained local fixture proofs;
these portable generator tests intentionally do not depend on promtool.
"""
import unittest

import build


SUMMARY_TILES = (
    "Exporter up", "Collectors OK", "Goroutines (summary)",
    "GOMAXPROCS (summary)", "Config valid", "Config warnings", "Uptime",
)
MISSING = "No exporter telemetry"


class SummaryPresenceTest(unittest.TestCase):
    def setUp(self):
        self.doc = build.build_family()
        self.panels = {el["spec"]["title"]: el["spec"]
                       for el in self.doc["spec"]["elements"].values()}

    def expr(self, title, index=0):
        return self.panels[title]["data"]["spec"]["queries"][index]["spec"]["query"]["spec"]["expr"]

    def test_every_named_tile_has_explicit_neutral_absence(self):
        for title in SUMMARY_TILES:
            with self.subTest(title=title):
                defaults = self.panels[title]["vizConfig"]["spec"]["fieldConfig"]["defaults"]
                self.assertEqual(defaults.get("noValue"), MISSING)
                nulls = [m for m in defaults.get("mappings", [])
                         if m.get("type") == "special" and m["options"]["match"] == "null"]
                self.assertEqual(len(nulls), 1)
                self.assertEqual(nulls[0]["options"]["result"],
                                 {"text": MISSING, "color": "gray"})

    def test_source_absence_cannot_hide_the_summary_rows(self):
        doc = build.build(build.dashboards.HEALTH)
        overview = doc["spec"]["layout"]["spec"]["tabs"][0]
        rows = overview["spec"]["layout"]["spec"]["rows"]
        found = {r["spec"]["title"]: r["spec"] for r in rows}
        for title in ("Golden signals", "Application health", "API throttling"):
            self.assertNotIn("conditionalRendering", found[title])

    def test_collector_zero_requires_matching_scrape_samples(self):
        expr = self.expr("Collectors OK")
        self.assertNotIn("vector(0)", expr)
        self.assertIn("or (0 * count(last_over_time(tailscale2otel_scrape_success_ratio{", expr)
        # Both operands obey the two applicable Health dashboard controls.
        for matcher in ('tailscale_tailnet=~"$tailnet"',
                        'tailscale_collector=~"$collector"'):
            self.assertEqual(expr.count(matcher), 2)

    def test_warning_gauge_is_not_an_optional_event_counter(self):
        expr = self.expr("Config warnings")
        self.assertNotIn("or", expr)
        self.assertNotIn("tailscale_tailnet", expr)  # procEmitter, not runtime
        self.assertNotIn("tailscale_collector", expr)

    def test_api_event_zero_requires_scoped_request_activity(self):
        for index in (0, 1):
            expr = self.expr("API 429 / retries (summary)", index)
            self.assertIn("or (0 * sum(rate(tailscale2otel_api_requests_total{", expr)
            self.assertNotIn("vector(0)", expr)
            self.assertEqual(expr.count('tailscale_tailnet=~"$tailnet"'), 2)
            self.assertNotIn("tailscale_collector", expr)  # client emits no collector
        defaults = self.panels["API 429 / retries (summary)"]["vizConfig"]["spec"]["fieldConfig"]["defaults"]
        self.assertEqual(defaults["noValue"], MISSING)


if __name__ == "__main__":
    unittest.main()
