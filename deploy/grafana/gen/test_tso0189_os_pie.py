"""Presentation contract for the native OS pie; queries remain state-separated."""
import json
import pathlib
import unittest


class OSPiePresentationTest(unittest.TestCase):
    def test_categories_and_quantities_live_in_full_width_legend(self):
        doc = json.loads((pathlib.Path(__file__).parents[1] / 'tailscale2otel-tailnet.json').read_text())
        panel = next(e['spec'] for e in doc['spec']['elements'].values()
                     if e['spec']['title'] == 'Devices by OS')
        self.assertEqual(panel['vizConfig']['group'], 'piechart')
        self.assertEqual(panel['vizConfig']['spec']['fieldConfig']['defaults']['decimals'], 1,
                         'A one-device slice must retain a nonzero percentage in the fixture')
        options = panel['vizConfig']['spec']['options']
        self.assertEqual(options['displayLabels'], [], 'Small slices must not carry overlapping annotations')
        legend = options['legend']
        self.assertTrue(legend['showLegend'])
        self.assertEqual(legend['placement'], 'bottom', 'Category names need the full panel width')
        self.assertEqual(legend['displayMode'], 'table')
        self.assertEqual(set(legend['values']), {'value', 'percent'})
        self.assertIn('device counts', panel['description'])
        self.assertIn('percentages', panel['description'])
        query = panel['data']['spec']['queries'][0]['spec']['query']['spec']
        self.assertIn('sum by (os_type, tailscale_authorized, tailscale_external)', query['expr'])
        self.assertEqual(query['legendFormat'], '{{os_type}} · authorized={{tailscale_authorized}} · external={{tailscale_external}}')

    def test_native_inventory_tile_has_room_for_long_labels_and_seven_rows(self):
        doc = json.loads((pathlib.Path(__file__).parents[1] / 'tailscale2otel-tailnet.json').read_text())
        key = next(k for k, e in doc['spec']['elements'].items()
                   if e['spec']['title'] == 'Devices by OS')

        def tile(node):
            if isinstance(node, dict):
                if node.get('kind') == 'GridLayoutItem' and node['spec']['element']['name'] == key:
                    return node['spec']
                for value in node.values():
                    found = tile(value)
                    if found:
                        return found
            elif isinstance(node, list):
                for value in node:
                    found = tile(value)
                    if found:
                        return found
            return None

        geometry = tile(doc['spec']['layout'])
        self.assertIsNotNone(geometry)
        # Expanded navigation left a nine-column tile with only 339 px for
        # category text needing up to 454 px. At that native-grid viewport,
        # at least half the grid is needed; the precise placement is incidental.
        self.assertGreaterEqual(geometry['width'], 12,
                                'Long state-separated labels need more than nine columns')
        self.assertGreaterEqual(geometry['height'], 15,
                                'Native bottom legend needs room for seven rows without scrolling')


if __name__ == '__main__':
    unittest.main()
