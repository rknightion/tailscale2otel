"""Distribution charts preserve category identity and name absent-population anchors."""
import unittest
import build

class CategoryNamesTest(unittest.TestCase):
    def test_pies_and_loki_tables_keep_category_names(self):
        for element in build.build_family()['spec']['elements'].values():
            panel = element['spec']; kind = panel['vizConfig']['group']
            queries = panel['data']['spec']['queries']
            for query in queries:
                q = query['spec']['query']['spec']
                if kind == 'piechart':
                    self.assertNotEqual(q.get('format'), 'table', panel['title'])
                    self.assertTrue(q.get('legendFormat'), panel['title'])
                if kind == 'table' and q.get('queryType') == 'instant' and query['spec']['query'].get('datasource', {}).get('name') == '${ds_loki}':
                    rename = {k:v for t in panel['data']['spec']['transformations'] if t['group']=='organize'
                              for k,v in t['spec']['options']['renameByName'].items()}
                    self.assertIn('Value #A', rename, panel['title'])
                    self.assertNotIn('Value', rename, panel['title'])

    def test_fallback_categories_only_exist_when_real_categories_are_absent(self):
        labels = {'Auto-approvers by kind': 'tailscale_acl_autoapprover_kind',
                  'Auto-approvers (inventory)': 'tailscale_acl_autoapprover_kind',
                  'Rejected flow records/s': 'source', 'Dedup counter conflicts/s': 'scope',
                  'Flow-view store drops/s': 'reason'}
        panels = {p['spec']['title']:p['spec'] for p in build.build_family()['spec']['elements'].values()}
        for title,label in labels.items():
            q = panels[title]['data']['spec']['queries'][0]['spec']['query']['spec']
            self.assertIn('or on() label_replace(0 * ', q['expr'])
            self.assertIn('"%s", "none", "", ""' % label, q['expr'])
            self.assertIn('{{'+label+'}}', q['legendFormat'])
        q = panels['rDNS cache fill']['data']['spec']['queries'][0]['spec']['query']['spec']['expr']
        self.assertTrue(q.startswith('max('))
        self.assertIn('/ clamp_min(max(', q)
        q = panels['Devices matched by integration']['data']['spec']['queries'][0]['spec']['query']['spec']
        self.assertIn('{{tailscale_posture_provider}}', q['legendFormat'])
        self.assertIn('{{tailscale_posture_integration}}', q['legendFormat'])
