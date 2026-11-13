"""Tag distributions retain bounded categories and compatible visualization options."""
import unittest
import build
import builder


class DenseCategoriesTest(unittest.TestCase):
    def test_tag_rollups_use_named_bounded_categories(self):
        panels = {p['spec']['title']: p['spec'] for p in build.build_family()['spec']['elements'].values()}
        service = panels['Service tags (rollup)']['vizConfig']
        self.assertEqual(service['group'], 'bargauge')
        fleet = panels['Fleet tags (rollup)']
        self.assertEqual(fleet['vizConfig']['spec']['options']['xField'], 'tailscale_tag')
        expr = fleet['data']['spec']['queries'][0]['spec']['query']['spec']['expr']
        self.assertIn('topk($topn,', expr)
        self.assertIn('top $topn', fleet['description'])
        for panel in panels.values():
            if panel['vizConfig']['group'] == 'barchart':
                self.assertNotIn('reduceOptions', panel['vizConfig']['spec']['options'])

    def test_bar_chart_rejects_reducer_options(self):
        with self.assertRaises(ValueError):
            builder.panel('synthetic tags', 'barchart', [], options=builder.bargauge_opts())
