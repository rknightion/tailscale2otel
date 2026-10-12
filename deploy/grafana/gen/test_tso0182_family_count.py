"""Distinct-family contract; actual PromQL execution is a retained scratch proof.

Portable generator checks intentionally need no promtool. The selected-tailnet
matcher is real runtime emitter scope; All also includes process-only reporters.
"""
import unittest

import build


class FamilyCountTest(unittest.TestCase):
    def setUp(self):
        doc = build.build(build.dashboards.HEALTH)
        self.panels = {el["spec"]["title"]: el["spec"]
                       for el in doc["spec"]["elements"].values()}

    def query(self, title):
        queries = self.panels[title]["data"]["spec"]["queries"]
        self.assertEqual(len(queries), 1)
        return queries[0]["spec"]["query"]["spec"]

    def test_family_count_groups_names_before_counting_reporters(self):
        query = self.query("Metric families tracked")
        self.assertEqual(query["expr"],
                         'count(count by (metric_name) '
                         '(tailscale2otel_series_active{tailscale_tailnet=~"$tailnet"}))')
        self.assertTrue(query["instant"])
        self.assertFalse(query["range"])

    def test_series_cost_remains_a_separate_sum(self):
        query = self.query("Total active series")
        self.assertEqual(query["expr"],
                         'sum(tailscale2otel_series_active{tailscale_tailnet=~"$tailnet"})')
        self.assertTrue(query["instant"])

    def test_description_discloses_mixed_runtime_process_scope(self):
        desc = self.panels["Metric families tracked"]["description"]
        for fact in ("metric_name", "selected tailnets", "not per runtime",
                     "All", "process reporters", "without a tailnet label"):
            with self.subTest(fact=fact):
                self.assertIn(fact, desc)
        self.assertIn("not a latest-state inventory", desc)


if __name__ == "__main__":
    unittest.main()
