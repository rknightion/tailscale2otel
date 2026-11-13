"""Measurement units never coerce a table's identity or metadata columns."""
import unittest
import build
import builder


class TableUnitsTest(unittest.TestCase):
    def test_table_units_are_scoped_to_measurements(self):
        panels = {p['spec']['title']:p['spec'] for p in build.build_family()['spec']['elements'].values()}
        for panel in panels.values():
            if panel['vizConfig']['group'] == 'table':
                self.assertNotIn('unit', panel['vizConfig']['spec']['fieldConfig']['defaults'], panel['title'])
        overrides = panels['Per-metric utilization (top-N)']['vizConfig']['spec']['fieldConfig']['overrides']
        self.assertIn({'matcher': {'id': 'byName', 'options': 'Utilization'},
                       'properties': [{'id': 'unit', 'value': 'percentunit'}]}, overrides)
        for title in ('Span class activity', 'Scrape cadence by collector (traces)'):
            self.assertEqual(panels[title]['vizConfig']['spec']['fieldConfig']['defaults']['unit'], 'cps')

    def test_each_named_value_gets_unit_without_changing_existing_overrides(self):
        original = [{'matcher': {'id': 'byName', 'options': 'Host'}, 'properties': []}]
        name = builder.panel('synthetic measurements', 'table', [], unit='s', overrides=original,
                             transformations=[builder.organize(rename={'host': 'Host', 'Value #A': 'Age', 'Value #B': 'Latency'})])
        fields = builder.ELEMENTS[name]['spec']['vizConfig']['spec']['fieldConfig']
        self.assertEqual(fields['defaults'], {})
        self.assertEqual(fields['overrides'][0], original[0])
        self.assertEqual(len(original), 1)
        self.assertEqual([o['matcher']['options'] for o in fields['overrides'][1:]], ['Age', 'Latency'])
        for override in fields['overrides'][1:]:
            self.assertEqual(override['properties'], [{'id': 'unit', 'value': 's'}])
