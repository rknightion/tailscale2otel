"""Missing telemetry cannot inherit a healthy or unhealthy threshold verdict."""
import unittest
import build
import builder


class AbsentStateNeutralTest(unittest.TestCase):
    def test_threshold_panels_have_a_neutral_null_state(self):
        checked = 0
        for element in build.build_family()['spec']['elements'].values():
            panel = element['spec']
            defaults = panel['vizConfig']['spec']['fieldConfig']['defaults']
            if panel['vizConfig']['group'] not in ('stat', 'gauge', 'bargauge') or not defaults.get('thresholds'):
                continue
            checked += 1
            nulls = [m['options']['result'] for m in defaults.get('mappings', [])
                     if m['type'] == 'special' and m['options'].get('match') == 'null']
            self.assertEqual(len(nulls), 1, panel['title'])
            self.assertEqual(nulls[0]['color'], 'gray', panel['title'])
            self.assertEqual(nulls[0]['text'], defaults.get('noValue', 'No data'), panel['title'])
        self.assertGreater(checked, 50)

    def test_does_not_mutate_callers_mapping_list_or_zero_verdict(self):
        mappings = [{'type': 'value', 'options': {'0': {'text': 'healthy', 'color': 'green'}}}]
        name = builder.panel('synthetic count', 'stat', [], mappings=mappings,
                             thresholds=builder.thr([(None, 'green'), (1, 'red')]))
        defaults = builder.ELEMENTS[name]['spec']['vizConfig']['spec']['fieldConfig']['defaults']
        self.assertEqual(len(mappings), 1)
        self.assertEqual(defaults['mappings'][0], mappings[0])
        self.assertEqual(defaults['thresholds']['steps'][0]['color'], 'green')
        self.assertEqual(len(defaults['mappings']), 2)
