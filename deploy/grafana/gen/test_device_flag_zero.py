"""Observed all-off flags count as zero; an absent source remains absent."""
import re
import unittest
import build


class DeviceFlagZeroTest(unittest.TestCase):
    def test_counts_are_anchored_to_the_same_scoped_source(self):
        titles = {'Tailscale SSH enabled (devices)', 'Key expiry disabled (devices)',
                  'Blocking incoming connections', 'Multiple simultaneous connections',
                  'Posture identity disabled'}
        panels = {p['spec']['title']: p['spec'] for p in build.build_family()['spec']['elements'].values()}
        for title in titles:
            expr = panels[title]['data']['spec']['queries'][0]['spec']['query']['spec']['expr']
            match = re.fullmatch(r'count\((.+) == 1\) or \(0 \* count\(\1\)\)', expr)
            self.assertIsNotNone(match, title + ': ' + expr)
            for label in ('os_type', 'host_name', 'tailscale_tailnet', 'tailscale2otel_provider'):
                self.assertIn(label + '=~', match[1])
