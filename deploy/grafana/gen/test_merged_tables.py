"""Merged measurements share entity and tenant keys without metric/replica labels."""
import re
import unittest
import build
import builder

class MergedTablesTest(unittest.TestCase):
    def test_merged_tables_have_identical_name_free_grouping(self):
        count = 0
        for element in build.build_family()['spec']['elements'].values():
            p = element['spec']
            if p['vizConfig']['group'] != 'table' or not any(t['group']=='merge' for t in p['data']['spec']['transformations']):
                continue
            count += 1
            groups=[]
            for q in p['data']['spec']['queries']:
                expr=q['spec']['query']['spec']['expr']
                self.assertIsNone(re.match(r'(?:time\(\) - )?last_over_time\(',expr),p['title'])
                match=re.match(r'(?:time\(\) - )?max by \(([^)]+)\)',expr)
                if match:
                    labels=tuple(x.strip() for x in match[1].split(','))
                    self.assertIn('tailscale_tailnet',labels)
                    self.assertIn('tailscale2otel_provider',labels)
                    groups.append(labels)
                else:
                    self.assertNotRegex(expr,r'\bby\s*\(')
                    groups.append(None)
            self.assertEqual(len(set(groups)),1,p['title'])
        self.assertGreater(count,6)

    def test_builder_rejects_mismatched_groups_and_unaggregated_frames(self):
        for exprs in [
            ['max by (host_name, tailscale_tailnet, tailscale2otel_provider) (a)', 'max by (user_id, tailscale_tailnet, tailscale2otel_provider) (b)'],
            ['last_over_time(a[5m])','last_over_time(b[5m])'],
        ]:
            with self.assertRaises(ValueError):
                builder.panel('synthetic join','table',[builder.prom_t(e) for e in exprs],transformations=[builder.merge()])

    def test_assembled_queries_match_executed_sparse_tenant_fixture(self):
        import json
        from pathlib import Path
        fixture = json.loads((Path(__file__).parent / 'testdata/merged_tables/promql.json').read_text())
        panels = {e['spec']['title']:e['spec'] for e in build.build_family()['spec']['elements'].values()}
        self.assertEqual({case['name'] for case in fixture['tests']}, {'Per-user detail', 'VIP service health', 'Tailnet scorecard'})
        for case in fixture['tests']:
            panel = panels[case['name']]
            self.assertEqual(len(panel['data']['spec']['queries']), len(case['promql_expr_test']))
            for query, receipt in zip(panel['data']['spec']['queries'], case['promql_expr_test'], strict=True):
                expr = query['spec']['query']['spec']['expr'].replace('$tailnet', 'fixture').replace('$provider', '.*').replace('$__rate_interval', '1m')
                self.assertEqual(expr, receipt['expr'])
            self.assertGreater(len(case['input_series']), 3)
