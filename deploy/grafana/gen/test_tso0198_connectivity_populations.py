"""Bind assembled connectivity queries to the real-promtool mixed-OS receipt.

Aggregate connectivity counts have no OS label. With two Linux and eight Windows
nodes, eight direct-capable and two hard-NAT nodes, the fleet ratios must stay
80% and 20% when Linux is selected (the old queries returned 400% and 100%).
Portable CI binds queries to the fixture executed with promtool during this fix;
it does not emulate PromQL. No OS labels are invented for aggregate numerators.
"""
import json
from pathlib import Path
import unittest

import build
import dashboards


TITLES = ('Direct-capable %', 'Hard-NAT %', 'Hard-NAT fraction')
FIXTURE = Path(__file__).parent / 'testdata' / 'tso0198' / 'connectivity-rules.json'


class ConnectivityPopulationTest(unittest.TestCase):
    def test_assembled_queries_match_executed_mixed_os_fixture(self):
        doc = build.build(dashboards.TAILNET)
        panels = {e['spec']['title']: e['spec'] for e in doc['spec']['elements'].values()}
        fixture = json.loads(FIXTURE.read_text())
        self.assertEqual([case['name'] for case in fixture['tests']],
                         ['OS=.*', 'OS=linux', 'OS=windows', 'OS=absent', 'missing telemetry'])
        for case in fixture['tests']:
            for title, receipt, expected in zip(TITLES, case['promql_expr_test'], (.8, .2, .2), strict=True):
                with self.subTest(scenario=case['name'], panel=title):
                    panel = panels[title]
                    expr = panel['data']['spec']['queries'][0]['spec']['query']['spec']['expr']
                    self.assertNotIn('$os_type', expr)
                    self.assertNotIn('$host_name', expr)
                    self.assertIn('Fleet-wide', panel['description'])
                    # Ownership selectors apply to BOTH ratio operands.
                    self.assertEqual(expr.count('tailscale_tailnet=~"$tailnet"'), 2)
                    self.assertEqual(expr.count('tailscale2otel_provider=~"$provider"'), 2)
                    expr = expr.replace('$tailnet', 'fixture').replace('$provider', 'tailscale')
                    self.assertEqual(expr, receipt['expr'])
                    samples = receipt['exp_samples']
                    if case['name'] == 'missing telemetry':
                        self.assertEqual(samples, [])
                    else:
                        self.assertEqual(len(samples), 1)
                        self.assertEqual(samples[0]['value'], expected)
            for series in case['input_series']:
                if series['series'].startswith(('tailscale_devices_direct_capable_ratio',
                                                'tailscale_devices_hard_nat_ratio')):
                    self.assertNotIn('os_type', series['series'])
        # Generator-only scope instructions must never escape into Grafana schema.
        self.assertNotIn('_population', json.dumps(doc))


if __name__ == '__main__':
    unittest.main()
