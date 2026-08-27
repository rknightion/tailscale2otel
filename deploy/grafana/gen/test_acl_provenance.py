#!/usr/bin/env python3
"""ACL age panels must expose whether their timestamp is authoritative or observed."""

import importlib.util
from pathlib import Path
import unittest


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


dashboard = load_module("tailscale2otel_dashboard", Path(__file__).with_name("build.py"))


class ACLProvenanceTests(unittest.TestCase):
    def test_acl_age_panels_prefer_audit_and_label_fallback(self):
        doc = dashboard.build_family()
        titles = {"ACL change evidence age", "ACL change evidence age (summary)"}
        panels = [p for p in doc["spec"]["elements"].values()
                  if p["spec"].get("title") in titles]
        self.assertEqual(len(panels), 2)
        for panel in panels:
            expr = panel["spec"]["data"]["spec"]["queries"][0]["spec"]["query"]["spec"]["expr"]
            self.assertIn("tailscale_acl_last_audit_change_seconds", expr)
            self.assertIn("tailscale_acl_last_changed_seconds", expr)
            self.assertIn('"provenance", "audit event"', expr)
            self.assertIn('"provenance", "revision first observed"', expr)
            self.assertIn('tailscale_tailnet=~"$tailnet"', expr)
            self.assertIn('tailscale2otel_provider=~"$provider"', expr)
            description = panel["spec"]["description"].lower()
            self.assertIn("source timestamp", description)
            self.assertIn("first observed", description)
            self.assertIn("restart", description)


if __name__ == "__main__":
    unittest.main()
