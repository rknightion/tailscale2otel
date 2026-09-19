#!/usr/bin/env python3
"""Contracts for specialist dashboard visualisations.

These tests exercise the emitted Grafana schema rather than implementation text.  A
wrong panel group, plugin version, location field, value field, or column order makes
the generated dashboard unusable even though the JSON still parses.
"""

import importlib.util
from pathlib import Path
import unittest


def load_builder():
    path = Path(__file__).with_name("builder.py")
    spec = importlib.util.spec_from_file_location("specialist_builder", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


builder = load_builder()


class SpecialistOptionContracts(unittest.TestCase):
    def test_specialist_options_encode_the_field_contracts(self):
        self.assertEqual(
            builder.pie_opts(),
            {"displayLabels": ["name", "percent"], "legend": {
                "displayMode": "table", "placement": "right", "showLegend": True}},
        )
        self.assertEqual(
            builder.geomap_opts(),
            {"view": {"id": "fit", "lat": 0, "lon": 0, "zoom": 1},
             "controls": {"showZoom": True, "showAttribution": True},
             "layers": [{"type": "markers", "name": "Devices", "config": {
                 "showLegend": True, "style": {"size": {"field": "Devices", "fixed": 5}},
                 "location": {"mode": "lookup", "lookup": "lookup"}}}]},
        )
        self.assertEqual(
            builder.state_timeline_opts(),
            {"mergeValues": True, "showValue": "auto", "alignValue": "left",
             "legend": {"displayMode": "list", "placement": "bottom", "showLegend": True},
             "tooltip": {"mode": "single", "sort": "none"}},
        )
        self.assertEqual(
            builder.heatmap_opts(),
            {"calculate": False, "yAxis": {"axisPlacement": "left", "unit": "s"},
             "legend": {"show": True}, "tooltip": {"show": True, "yHistogram": False}},
        )
        self.assertEqual(builder.sankey_opts("Bytes/s"), {
            "monochrome": False, "nodeColor": "grey", "nodeWidth": 30,
            "nodePadding": 24, "labelSize": 12, "iteration": 7,
            "valueField": "Bytes/s",
        })
        self.assertEqual(builder.treemap_opts("Metric", "Series", "Group"), {
            "labelBy": "Metric", "sizeBy": "Series", "colorBy": "Series",
            "groupBy": "Group", "tiling": "squarify", "separator": "",
        })

    def test_ordered_organize_keeps_legacy_defaults(self):
        legacy = builder.organize(exclude=["Time"], rename={"Value": "Count"})
        self.assertEqual(legacy["spec"]["options"]["indexByName"], {})

        ordered = builder.organize(
            exclude=["Time"], rename={"src": "Source", "dst": "Destination", "Value": "Bytes/s"},
            index={"src": 0, "dst": 1, "Value": 2},
        )
        self.assertEqual(ordered["spec"]["options"]["indexByName"], {
            "src": 0, "dst": 1, "Value": 2,
        })

    def test_panel_preserves_optional_plugin_identity(self):
        name = builder.panel(
            "Traffic topology", "netsage-sankey-panel", [builder.prom_t("vector(1)")],
            options=builder.sankey_opts("Bytes/s"), version="1.1.4",
        )
        viz = builder.ELEMENTS[name]["spec"]["vizConfig"]
        self.assertEqual(viz["group"], "netsage-sankey-panel")
        self.assertEqual(viz["version"], "1.1.4")
        self.assertEqual(viz["spec"]["options"]["valueField"], "Bytes/s")


if __name__ == "__main__":
    unittest.main()
