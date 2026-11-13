"""Table measurements remain visible and deliberate column order precedes metadata."""
import unittest
import build
import builder


class TableColumnsTest(unittest.TestCase):
    def test_tables_keep_renamed_columns_visible_and_ordered(self):
        found = set()
        for element in build.build_family()['spec']['elements'].values():
            panel = element['spec']
            if panel['vizConfig']['group'] != 'table':
                continue
            for transform in panel['data']['spec']['transformations']:
                if transform['group'] != 'organize':
                    continue
                opts = transform['spec']['options']
                rename = opts['renameByName']
                excluded = {k for k, v in opts['excludeByName'].items() if v}
                self.assertFalse(excluded.intersection(rename), panel['title'])
                if rename:
                    self.assertEqual(opts['indexByName'], {k: i for i, k in enumerate(rename)}, panel['title'])
                if panel['title'] in ('Last scrape age (summary)', 'PII filter status', 'OAuth scope preflight by capability'):
                    self.assertIn('Value', rename)
                    self.assertNotIn('Value', excluded)
                    found.add(panel['title'])
        self.assertEqual(len(found), 3)

    def test_contradictory_column_configuration_is_rejected(self):
        with self.assertRaises(ValueError):
            builder.organize(exclude=['Value'], rename={'Value': 'Count'})

    def test_explicit_order_takes_precedence(self):
        opts = builder.organize(rename={'Value': 'Count'}, index={'Value': 2})['spec']['options']
        self.assertEqual(opts['indexByName'], {'Value': 2})
