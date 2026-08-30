"""Tests for the Grafana annotation-store layer contract.

The dashboard layers must select Grafana's own annotation store by the same
root-plus-category tags that internal/annotations.Annotation.Tags emits. They
must remain hidden/off by default because each category layer is a subset of
the always-visible timeline layer.
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

    def test_root_timeline_layer_stays_visible(self):
        layer = next(
            layer
            for layer in BUILD.annotation_layers()
            if layer["spec"]["name"] == "Tailnet events"
        )
        spec = layer["spec"]
        self.assertTrue(spec["enable"])
        self.assertFalse(spec["hide"])
        self.assertEqual(spec["query"]["spec"]["tags"], ["tailscale2otel"])


if __name__ == "__main__":
    unittest.main()
