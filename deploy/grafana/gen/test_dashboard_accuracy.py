#!/usr/bin/env python3
"""Regression contracts for the live-dashboard accuracy repairs in TSO-0022."""

import importlib.util
import json
from pathlib import Path
import unittest


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


dashboard = load_module("tailscale2otel_dashboard_accuracy", Path(__file__).with_name("build.py"))


def panels(doc):
    for element in doc["spec"]["elements"].values():
        yield element["spec"]


def panel(doc, title):
    found = [p for p in panels(doc) if p["title"] == title]
    if len(found) != 1:
        raise AssertionError("expected one panel titled %r, found %d" % (title, len(found)))
    return found[0]


def queries(p):
    return [q["spec"]["query"]["spec"].get("expr") or
            q["spec"]["query"]["spec"].get("query", "")
            for q in p["data"]["spec"]["queries"]]


class CardinalityAccuracyTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build_family()

    def test_cap_panels_use_the_exported_limit_not_a_10k_literal(self):
        titles = ["Busiest metric — % of cap", "Active series vs cap (top $topn)",
                  "Active series over time (top $topn)",
                  "Per-metric utilization (top-N)"]
        rendered = json.dumps([panel(self.doc, title) for title in titles]).lower()
        self.assertNotIn("10000", rendered)
        self.assertNotIn("10k", rendered)
        for title in titles:
            p = panel(self.doc, title)
            expr = " ".join(queries(p))
            self.assertIn("tailscale2otel_series_limit", expr)
            self.assertIn("tailscale2otel_series_limit) > 0", expr)
            defaults = p["vizConfig"]["spec"]["fieldConfig"]["defaults"]
            self.assertIn("unlimited", defaults["noValue"].lower())

    def test_utilization_is_not_labelled_as_headroom(self):
        p = panel(self.doc, "Per-metric utilization (top-N)")
        self.assertNotIn("headroom", json.dumps(p).lower())
        self.assertIn("utilization", json.dumps(p).lower())


class NodeMetricsPresentationTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build_family()

    def test_tailnet_summary_exposes_up_over_total(self):
        p = panel(self.doc, "Targets up / total")
        self.assertEqual(p["vizConfig"]["spec"]["fieldConfig"]["defaults"]["unit"],
                         "percentunit")
        expr = " ".join(queries(p))
        self.assertGreaterEqual(expr.count("tailscale_node_up_ratio"), 2)

    def test_raw_node_tables_remove_transport_and_resource_noise(self):
        titles = ["Advertised routes", "Approved routes", "Health messages",
                  "Home DERP region", "Peer relay endpoints"]
        noise = {"Time", "__name__", "job", "instance", "service_instance_id",
                 "service_name", "service_namespace", "deployment_environment_name",
                 "otel_scope_name", "otel_scope_version"}
        for title in titles:
            p = panel(self.doc, title)
            excluded = set()
            for transform in p["data"]["spec"]["transformations"]:
                excluded.update(transform["spec"]["options"].get("excludeByName", {}))
            self.assertTrue(noise.issubset(excluded), "%s leaves resource noise visible" % title)


class NatRelaySemanticsTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build_family()

    def test_hard_nat_and_peer_relay_are_separate_single_unit_panels(self):
        titles = {p["title"] for p in panels(self.doc)}
        self.assertNotIn("NAT → relay pressure", titles)
        hard_nat = panel(self.doc, "Hard-NAT fraction")
        relays = panel(self.doc, "Configured peer-relay endpoints")
        self.assertEqual(hard_nat["vizConfig"]["spec"]["fieldConfig"]["defaults"]["unit"],
                         "percentunit")
        self.assertEqual(relays["vizConfig"]["spec"]["fieldConfig"]["defaults"]["unit"],
                         "short")
        self.assertNotIn("pressure", (hard_nat["description"] + relays["description"]).lower())
        for p in (hard_nat, relays):
            expr = " ".join(queries(p))
            self.assertIn('tailscale_tailnet=~"$tailnet"', expr)
            self.assertIn('tailscale2otel_provider=~"$provider"', expr)


class PrerequisiteAwareZeroTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build_family()

    def test_empty_populations_zero_only_when_the_source_family_is_present(self):
        prerequisites = {
            "Unauthorized (internal)": "tailscale_devices_count_ratio",
            "External (shared-in)": "tailscale_devices_count_ratio",
            "UDP blocked (DERP-forced)": "tailscale_device_connectivity_udp_ratio",
            "Auto-approvers (inventory)": "tailscale_acl_size_bytes",
            "Auto-approvers by kind": "tailscale_acl_size_bytes",
            "Profile upload failures/s by type": "tailscale2otel_profiling_upload_attempts_total",
        }
        for title, prerequisite in prerequisites.items():
            expr = " ".join(queries(panel(self.doc, title)))
            self.assertIn(" or ", expr, "%s has no present-source zero fallback" % title)
            self.assertIn(prerequisite, expr, "%s does not anchor zero to its source" % title)
            self.assertNotIn("vector(0)", expr, "%s turns absent telemetry into zero" % title)


class FlowLogReconciliationTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build_family()

    def test_raw_log_aggregates_sum_the_selected_range(self):
        for title in ("Observed bytes (flow logs)", "Top node-pair talkers (flow logs)"):
            p = panel(self.doc, title)
            expr = " ".join(queries(p))
            self.assertIn("sum_over_time", expr)
            self.assertIn("[$__range]", expr)
            self.assertNotIn('"noValue": "0"', json.dumps(p))

    def test_healthy_hygiene_counters_zero_only_with_flow_source_present(self):
        for title in ("Rejected flow records/s", "Dedup counter conflicts/s",
                      "Flow-view store drops/s"):
            expr = " ".join(queries(panel(self.doc, title)))
            self.assertIn(" or ", expr)
            self.assertIn("tailscale_network_flows_total", expr)
            self.assertNotIn("vector(0)", expr)


class PostureSemanticsTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build_family()

    def test_distribution_retains_attribute_and_value_dimensions(self):
        expr = " ".join(queries(panel(self.doc, "Compliance distribution")))
        self.assertIn("count by (attribute, value)", expr)

    def test_arbitrary_numeric_zero_is_not_called_a_failure(self):
        titles = {p["title"] for p in panels(self.doc)}
        self.assertNotIn("Devices failing posture attr", titles)
        p = panel(self.doc, "Posture attribute values by device")
        self.assertNotIn("== 0", " ".join(queries(p)))
        self.assertIn("attribute", json.dumps(p))
        self.assertIn("Value", json.dumps(p))

    def test_coverage_surfaces_report_numerator_denominator_and_unknown(self):
        for title in ("Encryption posture population", "Client posture population"):
            p = panel(self.doc, title)
            rendered = json.dumps(p).lower()
            for label in ("passing", "reporting", "unknown / unsupported"):
                self.assertIn(label, rendered)
            expr = " ".join(queries(p))
            self.assertIn("tailscale_devices_count_ratio", expr)

    def test_posture_numerators_zero_only_when_the_reporting_family_is_present(self):
        cases = {
            "Encryption posture population": ("tailscale_device_attribute_ratio", 1),
            "Client posture population": ("tailscale_device_posture_ratio", 2),
        }
        for title, (anchor, numerator_count) in cases.items():
            exprs = queries(panel(self.doc, title))
            for expr in exprs[:numerator_count]:
                self.assertIn(" or ", expr)
                self.assertIn(anchor, expr)
                self.assertNotIn("vector(0)", expr)

    def test_attribute_key_drops_have_a_visible_panel(self):
        p = panel(self.doc, "Posture attribute keys dropped")
        expr = " ".join(queries(p))
        self.assertIn("tailscale_device_attributes_dropped_ratio", expr)
        self.assertIn("attribute_key_limit", p["description"])


class TraceSpanCoverageTest(unittest.TestCase):
    SPAN_SOURCES = {
        "scrape ": "internal/collector/scheduler.go",
        "tailscale.api ": "internal/tsapi/transport.go",
        "headscale.api ": "internal/hsapi/client.go",
        "nodemetrics.scrape": "internal/collector/nodemetrics/nodemetrics.go",
        "stream.receive": "internal/stream/stream.go",
        "webhook.receive": "internal/webhook/webhook.go",
        "release.check ": "internal/release/release.go",
    }

    def setUp(self):
        self.doc = dashboard.build_family()

    def test_every_emitted_span_class_is_named_by_the_dashboard(self):
        repo = Path(__file__).resolve().parents[3]
        all_queries = " ".join(q for p in panels(self.doc) for q in queries(p))
        for span_prefix, source in self.SPAN_SOURCES.items():
            self.assertIn(span_prefix, (repo / source).read_text(),
                          "%s no longer proves an emitted span class" % source)
            self.assertIn(span_prefix, all_queries,
                          "%s spans have no dashboard discovery/query route" % span_prefix)


class PyroscopeRouteTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build_family()

    def test_health_dashboard_exposes_a_pyroscope_datasource(self):
        variables = {v["spec"]["name"]: v for v in self.doc["spec"]["variables"]}
        self.assertEqual(variables["ds_pyroscope"]["spec"]["pluginId"],
                         "grafana-pyroscope-datasource")

    def test_bounded_profile_panels_query_the_exported_profile_set(self):
        expected = {
            "CPU profile activity": "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
            "In-use heap profile activity": "memory:inuse_space:bytes:space:bytes",
            "CPU flame graph": "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
            "In-use heap flame graph": "memory:inuse_space:bytes:space:bytes",
        }
        for title, profile_type in expected.items():
            p = panel(self.doc, title)
            rendered = json.dumps(p)
            self.assertIn("${ds_pyroscope}", rendered)
            self.assertIn(profile_type, rendered)
            query_spec = p["data"]["spec"]["queries"][0]["spec"]["query"]["spec"]
            self.assertEqual(query_spec["labelSelector"],
                             '{service_name="tailscale2otel"}')
            if "flame graph" in title.lower():
                self.assertIn('"maxNodes": 8192', rendered)


class NeutralConfigurationPresentationTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build_family()

    def test_configuration_booleans_are_neutral_facts(self):
        for title in ("MagicDNS", "Override local DNS"):
            defaults = panel(self.doc, title)["vizConfig"]["spec"]["fieldConfig"]["defaults"]
            semantic = {k: defaults[k] for k in ("mappings", "thresholds") if k in defaults}
            self.assertNotIn("red", json.dumps(semantic).lower())

    def test_external_tailnets_role_is_a_labelled_value(self):
        p = panel(self.doc, "External-tailnets role")
        self.assertEqual(p["vizConfig"]["group"], "table")
        rendered = json.dumps(p)
        self.assertIn('"tailscale_setting_role": "Role"', rendered)
        self.assertIn('"Value": true', rendered)

    def test_narrow_configuration_tables_hide_resource_noise(self):
        noise = {"Time", "__name__", "job", "instance", "service_instance_id",
                 "service_name", "service_namespace", "deployment_environment_name",
                 "otel_scope_name", "otel_scope_version"}
        for title in ("Resolvers", "Per-user detail", "Backing hosts by service",
                      "Integration sync detail"):
            p = panel(self.doc, title)
            excluded = set()
            for transform in p["data"]["spec"]["transformations"]:
                excluded.update(transform["spec"]["options"].get("excludeByName", {}))
            self.assertTrue(noise.issubset(excluded), "%s leaves resource noise visible" % title)


if __name__ == "__main__":
    unittest.main()
