"""Freeze Runtime's core process summary tiles; browser proof is retained separately.

Portable structural regression: no promtool or browser installation is required.
Opt-in profiling/TLS stats have their own feature-specific empty-state contracts.
"""
import unittest

import build
import dashboards


RUNTIME_TILES = {
    "Goroutines": ("short", "max(tailscale2otel_runtime_goroutines_ratio)", "area"),
    "GOMAXPROCS": ("short", "max(tailscale2otel_runtime_gomaxprocs_ratio)", "none"),
    "Process uptime": ("s", "max(process_uptime_seconds)", "none"),
}
MISSING = "No process telemetry in range"


class RuntimePresenceTest(unittest.TestCase):
    def setUp(self):
        self.doc = build.build(dashboards.HEALTH)
        self.panels = {el["spec"]["title"]: el["spec"]
                       for el in self.doc["spec"]["elements"].values()}

    def test_exact_core_runtime_summary_tile_set(self):
        runtime = next(t for t in self.doc["spec"]["layout"]["spec"]["tabs"]
                       if t["spec"]["title"] == "Runtime & capacity")
        runtime = next(t for t in runtime["spec"]["layout"]["spec"]["tabs"]
                       if t["spec"]["title"] == "Runtime")
        row = next(r for r in runtime["spec"]["layout"]["spec"]["rows"]
                   if r["spec"]["title"] == "Go runtime")
        specs = [self.doc["spec"]["elements"][i["spec"]["element"]["name"]]["spec"]
                 for i in row["spec"]["layout"]["spec"]["items"]]
        self.assertEqual({p["title"] for p in specs if p["vizConfig"]["group"] == "stat"},
                         set(RUNTIME_TILES))
        self.assertNotIn("conditionalRendering", row["spec"])

    def test_missing_source_has_explicit_neutral_mapping(self):
        for title in RUNTIME_TILES:
            with self.subTest(title=title):
                defaults = self.panels[title]["vizConfig"]["spec"]["fieldConfig"]["defaults"]
                self.assertEqual(defaults.get("noValue"), MISSING)
                self.assertEqual(defaults.get("mappings"), [{"type": "special", "options": {
                    "match": "null", "result": {"text": MISSING, "color": "gray"}}}])

    def test_present_numeric_units_process_scope_and_polarity_are_unchanged(self):
        for title, (unit, expr, graph) in RUNTIME_TILES.items():
            with self.subTest(title=title):
                p = self.panels[title]
                queries = p["data"]["spec"]["queries"]
                self.assertEqual(len(queries), 1)
                self.assertEqual(queries[0]["spec"]["query"]["spec"]["expr"], expr)
                viz = p["vizConfig"]["spec"]
                self.assertEqual(viz["fieldConfig"]["defaults"]["unit"], unit)
                self.assertNotIn("thresholds", viz["fieldConfig"]["defaults"])
                self.assertEqual(viz["fieldConfig"]["overrides"], [])
                self.assertEqual(viz["options"]["colorMode"], "value")
                self.assertEqual(viz["options"]["graphMode"], graph)
                self.assertEqual(viz["options"]["reduceOptions"],
                                 {"calcs": ["lastNotNull"], "fields": "", "values": False})


if __name__ == "__main__":
    unittest.main()
