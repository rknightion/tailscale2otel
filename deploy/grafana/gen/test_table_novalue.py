"""Partial rows use short cell placeholders without dropping whole-panel explanations."""
import unittest
import build
import builder

CELL_PLACEHOLDER = {'matcher': {'id': 'byRegexp', 'options': '.*'}, 'properties': [{'id': 'noValue', 'value': '-'}]}

class TableNoValueTest(unittest.TestCase):
    def test_table_cells_have_a_short_low_precedence_placeholder(self):
        count = 0
        for element in build.build_family()['spec']['elements'].values():
            panel = element['spec']; viz = panel['vizConfig']
            fields = viz['spec']['fieldConfig']
            if viz['group'] == 'table' and fields['defaults'].get('noValue'):
                self.assertEqual(fields['overrides'][0], CELL_PLACEHOLDER, panel['title'])
                self.assertNotEqual(fields['defaults']['noValue'], '-')
                count += 1
            if panel['title'] == 'Credential scopes (top-N)':
                rename = panel['data']['spec']['transformations'][0]['spec']['options']['renameByName']
                self.assertEqual(rename['tailscale_key_owner'], 'Owner')
        self.assertGreater(count, 20)

    def test_explicit_column_placeholder_can_override_default(self):
        overrides = [{'matcher': {'id': 'byName', 'options': 'Optional'}, 'properties': [{'id': 'noValue', 'value': 'unset'}]}]
        name = builder.panel('synthetic partial table', 'table', [], novalue='No source observations', overrides=overrides)
        fields = builder.ELEMENTS[name]['spec']['vizConfig']['spec']['fieldConfig']
        self.assertEqual(fields['defaults']['noValue'], 'No source observations')
        self.assertEqual(fields['overrides'], [CELL_PLACEHOLDER] + overrides)
        self.assertEqual(len(overrides), 1)
