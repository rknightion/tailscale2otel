"""Tests for the Grafana annotation-store layer contract.

The dashboard layers must select Grafana's own annotation store by the same
tags that internal/annotations.Annotation.Tags emits. Category overlays stay
off by default and can also expose rollup regions.
"""

import importlib.util
from pathlib import Path
import unittest


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


BUILD = load_module(
    "tailscale2otel_build_annotations",
    Path(__file__).with_name("build.py"),
)


class AnnotationLayerContractTest(unittest.TestCase):
    def test_every_category_layer_ands_root_and_category_tags(self):
        layers = BUILD.annotation_layers()
        by_name = {layer["spec"]["name"]: layer for layer in layers}
        expected = {
            "— config changes only": "config_change",
            "— key expiry only": "expiry",
            "— policy changes only": "policy_change",
            "— inventory changes only": "inventory",
            "— risk findings only": "risk",
        }
        self.assertEqual(
            set(by_name),
            {"Annotations & Alerts", "Tailnet events", *expected},
        )

        for name, category in expected.items():
            with self.subTest(name=name):
                layer = by_name[name]
                spec = layer["spec"]
                query = spec["query"]["spec"]
                self.assertFalse(spec["enable"])
                self.assertFalse(spec["hide"])
                self.assertEqual(query["queryType"], "tags")
                self.assertFalse(query["matchAny"])
                self.assertEqual(
                    query["tags"], ["tailscale2otel", "category:" + category]
                )

    def test_point_timeline_layer_stays_visible(self):
        layer = next(
            layer
            for layer in BUILD.annotation_layers()
            if layer["spec"]["name"] == "Tailnet events"
        )
        spec = layer["spec"]
        self.assertTrue(spec["enable"])
        self.assertFalse(spec["hide"])
        query = spec["query"]["spec"]
        self.assertTrue(query["matchAny"])
        self.assertNotIn("tailscale2otel", query["tags"])
        self.assertTrue(all(tag.startswith("rule:") for tag in query["tags"]))


class DenseAnnotationLegibilityTest(unittest.TestCase):
    """#1211: dense stored point annotations must not paint over the series.

    Grafana 13.2's own "Hide lines and areas" panel option
    (`annotations: {lines: {width: 0}, regions: {opacity: 0}}`) keeps every marker on the
    time axis, and its hover details, while the dashed vertical lines and shaded regions stop
    covering the plot. The browser replay in the pull request is the proof of the rendering;
    this pins the generated shape that the replay exercised. Clustering is deliberately not
    used: it turns points into regions.
    """

    HIDE_CANVAS = {"lines": {"width": 0}, "regions": {"opacity": 0}}
    DRAWS_ANNOTATIONS = {"timeseries", "state-timeline"}

    @classmethod
    def setUpClass(cls):
        cls.docs = {
            spec.uid: BUILD.build(spec) for spec in BUILD.dashboards.ALL
        }

    def panels(self):
        for uid, doc in self.docs.items():
            for name, element in doc["spec"]["elements"].items():
                yield uid, name, element["spec"]

    def test_every_annotation_drawing_panel_hides_canvas_lines_and_regions(self):
        seen = 0
        for uid, name, spec in self.panels():
            viz = spec["vizConfig"]
            if viz["group"] not in self.DRAWS_ANNOTATIONS:
                continue
            seen += 1
            with self.subTest(dashboard=uid, panel=spec["title"]):
                self.assertEqual(
                    viz["spec"]["options"].get("annotations"), self.HIDE_CANVAS
                )
        self.assertGreater(seen, 100)

    def test_panels_that_never_draw_annotations_carry_no_option(self):
        for uid, name, spec in self.panels():
            viz = spec["vizConfig"]
            if viz["group"] in self.DRAWS_ANNOTATIONS:
                continue
            with self.subTest(dashboard=uid, panel=spec["title"]):
                self.assertNotIn("annotations", viz["spec"]["options"])

    def test_no_layer_clusters_points_into_regions(self):
        for layer in BUILD.annotation_layers():
            spec = layer["spec"]
            with self.subTest(layer=spec["name"]):
                self.assertNotIn("clustering", spec)
                self.assertNotIn("filter", spec)

    def test_health_dashboard_lists_every_stored_event_outside_the_plots(self):
        lists = [
            spec
            for uid, name, spec in self.panels()
            if spec["vizConfig"]["group"] == "annolist"
        ]
        self.assertEqual([spec["title"] for spec in lists], ["Stored annotation events"])
        panel = lists[0]
        self.assertEqual(panel["data"]["spec"]["queries"], [])
        options = panel["vizConfig"]["spec"]["options"]
        # The root tag selects every stored event, including rollup regions and rules
        # the default point layer does not name, so nothing is drawn by default and
        # still discoverable. The time range and org-wide scope match the layers.
        self.assertEqual(options["tags"], ["tailscale2otel"])
        self.assertTrue(options["onlyInTimeRange"])
        self.assertFalse(options["onlyFromThisDashboard"])
        self.assertFalse(options["navigateToPanel"])
        self.assertEqual(options["limit"], 100)
        self.assertTrue(options["showTime"])
        self.assertTrue(options["showTags"])
        self.assertTrue(panel["description"].strip())

    def test_the_list_does_not_renumber_panels_that_alerts_link(self):
        # Alert manifests address a panel by its positional numeric id, so the new panel
        # takes an id from the reserved late block and every other id keeps its place.
        health = self.docs["tailscale2otel-health"]["spec"]["elements"]
        late = [e["spec"]["id"] for e in health.values() if e["spec"]["vizConfig"]["group"] == "annolist"]
        rest = [e["spec"]["id"] for e in health.values() if e["spec"]["vizConfig"]["group"] != "annolist"]
        self.assertEqual(late, [BUILD.builder.LATE_ID_BASE + 1])
        self.assertEqual(sorted(rest), list(range(1, len(rest) + 1)))

    def test_only_the_health_dashboard_adds_the_list(self):
        for uid, doc in self.docs.items():
            groups = [
                element["spec"]["vizConfig"]["group"]
                for element in doc["spec"]["elements"].values()
            ]
            with self.subTest(dashboard=uid):
                self.assertEqual(groups.count("annolist"), 1 if uid.endswith("health") else 0)


if __name__ == "__main__":
    unittest.main()
