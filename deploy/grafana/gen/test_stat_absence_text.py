"""Short empty-state values preserve the full prerequisite in panel details."""
import unittest
import build

PREREQUISITES = {'Organization tailnets': 'No organization roster — configure tailscale.organization and a tailnets:read credential to enable alpha roster discovery.', 'Checkpoint disk': 'No checkpoint file — the store is not file-backed, or nothing has persisted yet.', 'Object-store age (cursor & newest object)': 'No object-store ingestion series. Requires collectors.flowlogs.objectstore (or collectors.auditlogs.objectstore) with the matching source: objectstore, and self_observability.enabled.'}

class StatAbsenceTextTest(unittest.TestCase):
    def test_empty_values_are_short_and_preserve_details(self):
        panels = {p['spec']['title']:p['spec'] for p in build.build_family()['spec']['elements'].values()}
        persist = panels["Checkpoint persist age"]["vizConfig"]["spec"]["fieldConfig"]["defaults"]
        self.assertEqual(persist["noValue"], PREREQUISITES["Checkpoint disk"])
        for title, prerequisite in PREREQUISITES.items():
            panel = panels[title]
            self.assertLessEqual(len(panel['vizConfig']['spec']['fieldConfig']['defaults']['noValue']), 24)
            self.assertIn('When empty: ' + prerequisite, panel['description'])

    def test_organization_card_fits_without_wrapping_inventory(self):
        doc = build.build(build.dashboards.TAILNET)
        positions = {}
        def visit(node):
            if isinstance(node, dict):
                if node.get('kind') == 'GridLayoutItem':
                    spec = node['spec']
                    title = doc['spec']['elements'][spec['element']['name']]['spec']['title']
                    positions[title] = spec
                for value in node.values(): visit(value)
            elif isinstance(node, list):
                for value in node: visit(value)
        visit(doc['spec']['layout'])
        self.assertGreaterEqual(positions['Organization tailnets']['width'], 4)
        for title in ('Online', 'Total', 'Offline', 'Updates available', 'Distinct users', 'Organization tailnets'):
            self.assertEqual(positions[title]['y'], 0)
