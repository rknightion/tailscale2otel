"""Cost comparisons share a time basis and separate counts from rates."""
import unittest
import build

class CostPanelsTest(unittest.TestCase):
    def test_cost_series_share_per_minute_basis(self):
        panels = {p['spec']['title']:p['spec'] for p in build.build_family()['spec']['elements'].values()}
        queries = panels['Ingest vs export cost (per minute)']['data']['spec']['queries']
        for q in queries:
            spec = q['spec']['query']['spec']
            self.assertIn('sum', spec['expr'])
            self.assertTrue(spec['expr'].endswith('*60'))
            self.assertIn('/min', spec['legendFormat'])
        panel = panels['Forwarded metric-name drops/s by reason']
        fields = panel['vizConfig']['spec']['fieldConfig']
        self.assertEqual(fields['defaults']['unit'], 'cps')
        self.assertIn({'matcher': {'id': 'byName', 'options': 'forwarded/uncataloged series active'},
                       'properties': [{'id': 'unit', 'value': 'short'}, {'id': 'custom.axisPlacement', 'value': 'right'}]}, fields['overrides'])
        self.assertEqual(panel['data']['spec']['queries'][0]['spec']['query']['spec']['legendFormat'], 'dropped {{reason}}')
