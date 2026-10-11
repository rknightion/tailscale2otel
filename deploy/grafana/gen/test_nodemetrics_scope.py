"""Deployment isolation contracts over actual rendered Node Metrics queries."""
import json
from pathlib import Path
import re
import unittest

import build
from test_query_budget import _collect_leaf_tabs, _refs


def node_queries(doc):
    tab, = [t for t in _collect_leaf_tabs(doc) if t['spec']['title'] == 'Node Metrics']
    for ref in sorted(_refs(tab['spec']['layout'])):
        panel = doc['spec']['elements'][ref]['spec']
        for query in panel['data']['spec']['queries']:
            yield panel['title'], query['spec']['query']['spec']['expr']


# These are vector selectors, not grouping labels or function names. Match EACH
# occurrence, including both directions and every denominator in ratio helpers.
SELECTOR = re.compile(r'\b((?:tailscaled_|tailscale_node_|tailscale_derp_region_|tailscale2otel_nodemetrics_)[a-z_]+)(\{[^}]*\})?')


class NodeMetricsScopeTest(unittest.TestCase):
    def test_every_vector_selector_scopes_both_deployment_controls(self):
        queries = list(node_queries(build.build_family()))
        self.assertGreater(len(queries), 30)
        for title, expr in queries:
            selectors = list(SELECTOR.finditer(expr))
            self.assertTrue(selectors, title)
            for match in selectors:
                with self.subTest(panel=title, selector=match.group(0)):
                    labels = match.group(2) or ''
                    self.assertIn('tailscale_tailnet=~"$tailnet"', labels)
                    self.assertIn('tailscale2otel_provider=~"$provider"', labels)

    def test_collector_label_contract_covers_exactly_the_queried_families(self):
        path = Path(__file__).resolve().parents[3] / 'internal/collector/nodemetrics/testdata/dashboard_metrics.json'
        queried = {m.group(1) for _, expr in node_queries(build.build_family()) for m in SELECTOR.finditer(expr)}
        self.assertEqual(queried, set(json.loads(path.read_text())))


if __name__ == '__main__':
    unittest.main()
