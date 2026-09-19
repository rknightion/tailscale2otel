#!/usr/bin/env python3
"""Contracts for specialist dashboard visualisations.

These tests exercise the emitted Grafana schema rather than implementation text.  A
wrong panel group, plugin version, location field, value field, or column order makes
the generated dashboard unusable even though the JSON still parses.
"""

import importlib.util
from pathlib import Path
import unittest

import build as dashboard


def load_builder():
    path = Path(__file__).with_name("builder.py")
    spec = importlib.util.spec_from_file_location("specialist_builder", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


builder = load_builder()


def panel_spec(doc, title):
    found = [element["spec"] for element in doc["spec"]["elements"].values()
             if element["spec"]["title"] == title]
    if len(found) != 1:
        raise AssertionError("expected one panel titled %r, found %d" % (title, len(found)))
    return found[0]


def rows(doc):
    found = {}

    def walk(node):
        if isinstance(node, dict):
            if node.get("kind") == "RowsLayoutRow":
                found[node["spec"]["title"]] = node["spec"]
            for value in node.values():
                walk(value)
        elif isinstance(node, list):
            for value in node:
                walk(value)

    walk(doc["spec"]["layout"])
    return found


def conditions(row_spec):
    present, hidden = None, set()
    group = row_spec.get("conditionalRendering") or {}
    for item in group.get("spec", {}).get("items", []):
        if item["spec"]["operator"] == "notMatches":
            hidden.add(item["spec"]["variable"])
        else:
            present = item["spec"]["variable"]
    return present, hidden


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


class FleetAndRelationshipPanelContracts(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.doc = dashboard.build_family()
        cls.rows = rows(cls.doc)

    def test_country_geomap_uses_iso_lookup_and_keeps_empty_state(self):
        spec = panel_spec(self.doc, "Devices by country")
        self.assertEqual(spec["vizConfig"]["group"], "geomap")
        self.assertEqual(spec["vizConfig"]["spec"]["options"], builder.geomap_opts())
        self.assertIn("tailscale_devices_by_country_ratio",
                      spec["data"]["spec"]["queries"][0]["spec"]["query"]["spec"]["expr"])
        transform = spec["data"]["spec"]["transformations"][0]["spec"]["options"]
        self.assertEqual(transform["renameByName"], {
            "geo_country_iso_code": "lookup", "Value": "Devices",
        })
        self.assertEqual(transform["indexByName"], {
            "geo_country_iso_code": 0, "Value": 1,
        })
        self.assertIn("No device geolocation series",
                      spec["vizConfig"]["spec"]["fieldConfig"]["defaults"]["noValue"])

    def test_fleet_composition_panels_are_native_pies(self):
        cases = {
            "Devices by OS": "tailscale_devices_count_ratio",
            "Compliance distribution": "tailscale_device_attribute_info_ratio",
        }
        for title, metric in cases.items():
            spec = panel_spec(self.doc, title)
            self.assertEqual(spec["vizConfig"]["group"], "piechart")
            self.assertEqual(spec["vizConfig"]["spec"]["options"], builder.pie_opts())
            self.assertIn(metric,
                          spec["data"]["spec"]["queries"][0]["spec"]["query"]["spec"]["expr"])

    def test_device_online_history_is_a_pii_gated_state_timeline(self):
        spec = panel_spec(self.doc, "Device online state by node")
        self.assertEqual(spec["vizConfig"]["group"], "state-timeline")
        self.assertIn("tailscale_device_online_ratio",
                      spec["data"]["spec"]["queries"][0]["spec"]["query"]["spec"]["expr"])
        self.assertIn("Device online state by node", self.rows)
        self.assertEqual(conditions(self.rows["Device online state by node"]),
                         (None, {"pii_perdevice"}))

    def test_kubernetes_request_paths_are_bounded_and_gated(self):
        spec = panel_spec(self.doc, "Kubernetes request paths")
        self.assertEqual(spec["vizConfig"]["group"], "netsage-sankey-panel")
        self.assertEqual(spec["vizConfig"]["version"], "1.1.4")
        self.assertIn("netsage-sankey-panel", spec["description"])
        self.assertIn("https://grafana.com/grafana/plugins/netsage-sankey-panel/",
                      spec["description"])
        query = spec["data"]["spec"]["queries"][0]["spec"]["query"]["spec"]["expr"]
        self.assertIn("topk($topn,", query)
        self.assertIn("tailscale_k8s_api_requests_total", query)
        order = spec["data"]["spec"]["transformations"][0]["spec"]["options"]["indexByName"]
        self.assertEqual(order, {"tailscale_k8s_user": 0, "tailscale_k8s_verb": 1,
                                 "tailscale_k8s_resource": 2, "Value": 3})
        self.assertEqual(conditions(self.rows["Kubernetes request paths"]),
                         ("has_k8s_audit", {"pii_emails"}))

    def test_vip_service_topology_is_bounded_and_gated(self):
        spec = panel_spec(self.doc, "VIP service topology")
        self.assertEqual(spec["vizConfig"]["group"], "netsage-sankey-panel")
        self.assertEqual(spec["vizConfig"]["version"], "1.1.4")
        self.assertIn("netsage-sankey-panel", spec["description"])
        query = spec["data"]["spec"]["queries"][0]["spec"]["query"]["spec"]["expr"]
        self.assertIn("topk($topn,", query)
        self.assertIn("tailscale_service_host_info_ratio", query)
        order = spec["data"]["spec"]["transformations"][0]["spec"]["options"]["indexByName"]
        self.assertEqual(order, {"tailscale_service_display_name": 0, "host_name": 1,
                                 "Value": 2})
        excluded = spec["data"]["spec"]["transformations"][0]["spec"]["options"]["excludeByName"]
        self.assertIn("tailscale_tailnet", excluded)
        self.assertIn("tailscale2otel_provider", excluded)
        self.assertEqual(conditions(self.rows["VIP service topology"]),
                         ("has_svc", {"pii_perdevice"}))


if __name__ == "__main__":
    unittest.main()
