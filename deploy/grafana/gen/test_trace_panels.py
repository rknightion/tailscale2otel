"""Trace searches render as selectable result tables rather than single traces."""
import unittest
import build

class TracePanelsTest(unittest.TestCase):
    def test_trace_search_results_are_actionable(self):
        panels = {p['spec']['title']:p['spec'] for p in build.build_family()['spec']['elements'].values()}
        self.assertIn('Scrape → API traces', panels)
        panel = panels['Scrape → API traces']
        self.assertEqual(panel['vizConfig']['group'], 'table')
        self.assertEqual(panel['data']['spec']['transformations'], [])
        self.assertIn('Select a trace ID to open its waterfall.', panel['description'])
        for panel in panels.values():
            for query in panel['data']['spec']['queries']:
                spec = query['spec']['query']['spec']
                if spec.get('queryType') == 'traceql':
                    self.assertNotEqual(panel['vizConfig']['group'], 'traces', panel['title'])
