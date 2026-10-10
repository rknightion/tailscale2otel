"""Contracts on emitted Grafana panels for sampled Serve Service throughput."""

import unittest

import build as dashboard
from test_specialist_panels import conditions, panel_spec, rows


class ServiceThroughputContract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.doc = dashboard.build_family()

    def test_service_throughput_uses_only_curated_bytes_and_all_dimensions(self):
        spec = panel_spec(self.doc, "Service throughput by node")
        self.assertEqual(spec["vizConfig"]["group"], "timeseries")
        defaults = spec["vizConfig"]["spec"]["fieldConfig"]["defaults"]
        self.assertEqual(defaults["unit"], "Bps")
        queries = spec["data"]["spec"]["queries"]
        self.assertEqual(len(queries), 1)
        query = queries[0]["spec"]["query"]["spec"]
        self.assertEqual(query["expr"],
                         "sum by (tailscale_node, tailscale_service_name, network_io_direction) "
                         '(rate(tailscale_node_service_io_bytes_total'
                         '{tailscale_tailnet=~"$tailnet", tailscale2otel_provider=~"$provider"}'
                         '[$__rate_interval]))')
        self.assertEqual(query["legendFormat"],
                         "{{tailscale_service_name}} {{tailscale_node}} {{network_io_direction}}")
        self.assertIn("__other__", spec["description"])
        self.assertIn("service_addrs", spec["description"])

    def test_service_throughput_scopes_both_deployment_controls_before_sum(self):
        # Same node/Service identities can occur in different deployments. Scope
        # both labels before sum removes them; otherwise selected rates inflate.
        spec = panel_spec(self.doc, "Service throughput by node")
        expr = spec["data"]["spec"]["queries"][0]["spec"]["query"]["spec"]["expr"]
        self.assertIn('tailscale_node_service_io_bytes_total{'
                      'tailscale_tailnet=~"$tailnet", tailscale2otel_provider=~"$provider"}', expr)

    def test_service_row_is_gated_on_its_own_metric_not_general_node_io(self):
        row = rows(self.doc)["Service throughput (curated)"]
        self.assertEqual(conditions(row), ("has_node_service_io", set()))
        matches = []

        def walk(node):
            if isinstance(node, dict):
                if node.get("kind") == "QueryVariable" and node.get("spec", {}).get("name") == "has_node_service_io":
                    matches.append(node["spec"])
                for value in node.values():
                    walk(value)
            elif isinstance(node, list):
                for value in node:
                    walk(value)

        walk(self.doc)
        self.assertEqual(len(matches), 1)
        self.assertIn("tailscale_node_service_io_bytes_total", str(matches[0]["query"]))

    def test_existing_raw_byte_panels_remain_available(self):
        for title, family in (("Inbound bytes/s", "tailscaled_inbound_bytes_total"),
                              ("Outbound bytes/s", "tailscaled_outbound_bytes_total")):
            spec = panel_spec(self.doc, title)
            expr = spec["data"]["spec"]["queries"][0]["spec"]["query"]["spec"]["expr"]
            self.assertIn(family, expr)


if __name__ == "__main__":
    unittest.main()
