#!/usr/bin/env python3

import importlib.util
import json
from pathlib import Path
import unittest


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


dashboard = load_module("tailscale2otel_dashboard_coordination", Path(__file__).with_name("build.py"))


class CoordinationLeaderPanelTest(unittest.TestCase):
    @staticmethod
    def assert_leadership_panel(rendered):
        for expected in (
            "Lease leadership",
            "tailscale2otel_coordination_leader_ratio",
            "coordination_identity",
            "coordination_state",
        ):
            if expected not in rendered:
                raise AssertionError("coordination leadership panel missing %s" % expected)

    def test_overview_renders_current_lease_holder_and_state(self):
        doc = dashboard.build(dashboard.dashboards.HEALTH, True, only="Overview")
        self.assert_leadership_panel(json.dumps(doc))

    @staticmethod
    def assert_handover_panel(rendered):
        for expected in (
            "Leadership handovers (last 15m)",
            "tailscale2otel_coordination_handovers_total",
            "increase(",
        ):
            if expected not in rendered:
                raise AssertionError("coordination handover panel missing %s" % expected)

    def test_overview_renders_recent_leadership_handovers(self):
        doc = dashboard.build(dashboard.dashboards.HEALTH, True, only="Overview")
        self.assert_handover_panel(json.dumps(doc))

    def test_guard_rejects_a_title_without_the_metric(self):
        with self.assertRaisesRegex(AssertionError, "coordination leadership panel missing"):
            self.assert_leadership_panel('{"title":"Lease leadership"}')


if __name__ == "__main__":
    unittest.main()
