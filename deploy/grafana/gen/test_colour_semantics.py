"""Inventory values stay neutral and categorical health colors remain deliberate."""
import unittest
import build


def verdict_colors(value):
    if isinstance(value, dict):
        return ({value['color']} if 'color' in value else set()).union(*(verdict_colors(v) for v in value.values()))
    if isinstance(value, list):
        return set().union(*(verdict_colors(v) for v in value))
    return set()


class ColourSemanticsTest(unittest.TestCase):
    def test_color_requires_a_declared_verdict(self):
        for element in build.build_family()['spec']['elements'].values():
            panel = element['spec']
            viz = panel['vizConfig']
            options = viz['spec']['options']
            defaults = viz['spec']['fieldConfig']['defaults']
            if viz['group'] == 'stat' and options.get('colorMode') in ('value', 'background'):
                colors = verdict_colors(defaults.get('mappings', [])) - {'gray', 'text'}
                self.assertTrue(defaults.get('thresholds') or colors, panel['title'])
            if viz['group'] == 'bargauge' and defaults.get('thresholds'):
                self.assertNotEqual(options.get('displayMode'), 'gradient', panel['title'])
            if panel['title'] == 'Capability availability history':
                self.assertNotIn('min', defaults)
                self.assertNotIn('max', defaults)
                value = defaults['mappings'][0]['options']['1']
                self.assertEqual(value['text'], 'active')
                self.assertEqual(value['color'], 'text')
