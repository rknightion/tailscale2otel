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
    def test_health_dashboard_carries_freshness_panels(self):
        # #526 moved accepted-data freshness off the product "Events & Logs" tab to
        # the health dashboard's Ingestion stage. It always described whether the
        # EXPORTER was keeping up, not what happened on the tailnet, so it belongs
        # beside the rest of the ingest pipeline. The panels were also merged, so
        # this asserts on the METRIC names rather than the panel titles — those are
        # what the alert rules and the staleness guidance below actually depend on,
        # and they survive a consolidation that a title does not.
        doc = dashboard.build(dashboard.dashboards.HEALTH, True, only="Ingestion")
        rendered = json.dumps(doc)
        for expected in (
            # Both readings now share one two-axis panel, "Accepted event freshness &
            # age p95, timestamp skew/s" — which is why these assert on SUBSTRINGS and
            # on the metric names rather than on whole titles.
            "Accepted event freshness",
            "timestamp skew/s",
            "tailscale2otel_ingest_last_event_timestamp_seconds",
            "tailscale2otel_ingest_event_age_seconds_bucket",
            "tailscale2otel_ingest_capture_delay_seconds_bucket",
            "tailscale2otel_ingest_timestamp_skew_total",
        ):
            self.assertIn(expected, rendered)

    def test_rules_ship_paused_staleness_guidance_and_recording_rule(self):
        all_rules = rules.rules_by_uid()
        stale = all_rules["ts2o-ingest-data-stale"]
        self.assertTrue(stale["paused"])
        self.assertIn(
            "tailscale2otel_ingest_last_event_timestamp_seconds",
            rules.rule_expr(stale),
        )
        recording = all_rules["ts2o-rec-ingest-freshness"]
        self.assertTrue(recording["paused"])
        self.assertEqual(
            recording["metric"],
            "tailscale2otel:ingest_event_freshness_seconds",
        )

    def test_ingest_integrity_metrics_are_operationally_visible(self):
        network = json.dumps(
            dashboard.build(dashboard.dashboards.TAILNET, True, only="Network & Flows")
        )
        for expected in (
            "Reporter trust & consistency",
            "Observed field omissions",
            "tailscale_network_reporter_observations_total",
            "tailscale_network_field_observations_total",
        ):
            self.assertIn(expected, network)

        # Audit schema drift moved to tailscale2otel-health's Ingestion tab in #526
        # (decision 9): it measures whether the EXPORTER decoded the audit feed's
        # fields, not what happened on the tailnet, and it sat on a tab an operator
        # opened to read the latter.
        ingestion = json.dumps(
            dashboard.build(dashboard.dashboards.HEALTH, True, only="Ingestion")
        )
        self.assertIn("Audit schema drift", ingestion)
        self.assertIn("tailscale_config_audit_schema_drift_total", ingestion)

    def test_integrity_alerts_are_paused_and_bounded(self):
        all_rules = rules.rules_by_uid()
        for uid, metric in (
            ("ts2o-flow-reporter-mismatch", "tailscale_network_reporter_observations_total"),
            ("ts2o-audit-schema-drift", "tailscale_config_audit_schema_drift_total"),
        ):
            rule = all_rules[uid]
            self.assertTrue(rule["paused"])
            self.assertIn(metric, rules.rule_expr(rule))


if __name__ == "__main__":
    unittest.main()
