#!/usr/bin/env python3
"""Topology relationships must remain distinct, native table columns."""
import unittest
import build


class TopologyTablesTest(unittest.TestCase):
    def test_topology_relationships_use_separate_columns(self):
        doc = build.build_family()
        panels = {p['spec']['title']: p['spec'] for p in doc['spec']['elements'].values()}
        for title, source, target in (
            ('Traffic topology - ROLLUP', 'tailscale_src_node', 'tailscale_dst_node'),
            ('VIP service topology', 'tailscale_service_display_name', 'host_name'),
        ):
            with self.subTest(title=title):
                spec = panels[title]
                self.assertEqual(spec['vizConfig']['group'], 'table')
                transforms = spec['data']['spec']['transformations']
                self.assertEqual([t['group'] for t in transforms], ['organize'])
                options = transforms[0]['spec']['options']
                self.assertIn(source, options['renameByName'])
                self.assertIn(target, options['renameByName'])
                self.assertNotEqual(options['renameByName'][source], options['renameByName'][target])

    def test_empty_and_unavailable_states_name_the_next_step(self):
        """The panel itself carries the guidance, so it shows for no data and for a failed query."""
        doc = build.build_family()
        panels = {p['spec']['title']: p['spec'] for p in doc['spec']['elements'].values()}
        for title, key in (('Traffic topology - ROLLUP', 'cardinality.flow.metrics_mode'),
                           ('VIP service topology', 'collectors.services.collect_hosts')):
            with self.subTest(title=title):
                no_value = panels[title]['vizConfig']['spec']['fieldConfig']['defaults']['noValue']
                self.assertIn('Prometheus datasource', no_value)
                self.assertIn(key, no_value)


if __name__ == '__main__':
    unittest.main()
