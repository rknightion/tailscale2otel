#!/usr/bin/env python3

import importlib.util
import json
from pathlib import Path
import unittest


ROOT = Path(__file__).resolve().parents[3]


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


dashboard = load_module("tailscale2otel_dashboard", Path(__file__).with_name("build.py"))
rules = load_module("tailscale2otel_rules", ROOT / "deploy" / "alerts" / "gen" / "build_rules.py")


class IngestFreshnessArtifactsTest(unittest.TestCase):
    def test_events_dashboard_carries_freshness_panels(self):
        doc = dashboard.build("test", "test", True, only="Events & Logs")
        rendered = json.dumps(doc)
        for expected in (
            "Accepted event freshness",
            "Accepted event age p95",
            "Capture delay p95",
            "Timestamp skew/s",
            "tailscale2otel_ingest_last_event_timestamp_seconds",
            "tailscale2otel_ingest_event_age_seconds_bucket",
            "tailscale2otel_ingest_capture_delay_seconds_bucket",
            "tailscale2otel_ingest_timestamp_skew_total",
        ):
            self.assertIn(expected, rendered)

    def test_rules_ship_paused_staleness_guidance_and_recording_rule(self):
        all_rules = {
            rule["uid"]: rule
            for group in rules.build()["groups"]
            for rule in group["rules"]
        }
        stale = all_rules["ts2o-ingest-data-stale"]
        self.assertTrue(stale["isPaused"])
        self.assertIn(
            "tailscale2otel_ingest_last_event_timestamp_seconds",
            stale["data"][0]["model"]["expr"],
        )
        recording = all_rules["ts2o-rec-ingest-freshness"]
        self.assertTrue(recording["isPaused"])
        self.assertEqual(
            recording["record"]["metric"],
            "tailscale2otel:ingest_event_freshness_seconds",
        )


if __name__ == "__main__":
    unittest.main()
