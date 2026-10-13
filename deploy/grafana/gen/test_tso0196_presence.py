"""Portable generated-surface contracts: absence is not configuration evidence."""
import unittest

import build


class CardinalityPresenceTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        doc = build.build(build.dashboards.HEALTH)
        cls.panels = {e['spec']['title']: e['spec'] for e in doc['spec']['elements'].values()}

    def expr(self, title):
        return self.panels[title]['data']['spec']['queries'][0]['spec']['query']['spec']['expr']

    def no_value(self, title):
        return self.panels[title]['vizConfig']['spec']['fieldConfig']['defaults']['noValue']

    def test_missing_overflow_does_not_become_observed_zero(self):
        for title in ['Overflowing now', 'Metrics over cardinality cap']:
            self.assertNotIn('vector(0)', self.expr(title))
            self.assertIn('unknown', self.no_value(title).lower())
        self.assertIn('sum(tailscale2otel_series_overflowing_ratio', self.expr('Overflowing now'))

    def test_absent_capacity_is_uncertain_including_unlimited_configuration(self):
        for title in ['Series limit', 'Busiest metric — % of cap',
                      'Per-metric utilization (top-N)', 'Active series over time (top $topn)']:
            self.assertIn('uncertain', self.no_value(title).lower())
            self.assertIn('cannot establish unlimited', self.no_value(title).lower())
            expr = self.expr(title)
            self.assertIn('tailscale2otel_series_limit', expr)
            self.assertIn('unless ignoring(metric_name)', expr)
            self.assertIn('unless on()', expr)
            self.assertNotIn('vector(0)', expr)
            self.assertNotIn('10000', expr)

    def test_inventory_empty_states_do_not_assert_health(self):
        for title in ['Metrics overflowing now', 'Overflowing families']:
            self.assertIn('unknown', self.no_value(title).lower())
            self.assertNotIn('fully resolved', self.no_value(title).lower())
        self.assertIn('last_over_time', self.expr('Metrics overflowing now'))
        self.assertIn('otel_metric_overflow="true"', self.expr('Overflowing families'))

    def test_scope_applies_to_every_presence_operand(self):
        for title in ['Series limit', 'Busiest metric — % of cap',
                      'Per-metric utilization (top-N)', 'Active series over time (top $topn)',
                      'Overflowing now', 'Metrics over cardinality cap']:
            expr = self.expr(title)
            self.assertIn('tailscale_tailnet=~"$tailnet"', expr)
            # Provider ad-hoc filtering is applied by Grafana's real datasource path;
            # it is not an extra literal selector in this health dashboard.
            self.assertNotIn('absent(', expr)


if __name__ == '__main__':
    unittest.main()
