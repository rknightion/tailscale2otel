"""Pod-log investigations must describe their population and preserve PII gates."""
import unittest

import build
from test_specialist_panels import conditions, rows


class PodLogReadTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.doc = build.build_family()
        cls.panel = next(p['spec'] for p in cls.doc['spec']['elements'].values()
                         if any('tailscale_k8s_subresource=`log`' in q['spec']['query']['spec'].get('expr', '')
                                for q in p['spec'].get('data', {}).get('spec', {}).get('queries', [])))
        cls.expr = cls.panel['data']['spec']['queries'][0]['spec']['query']['spec']['expr']

    def test_population_is_generic_pod_log_attempts(self):
        # A namespace filter cannot establish the identity of the pod being read.
        self.assertIn('Pod log', self.panel['title'])
        self.assertIn('attempt', self.panel['title'].lower())
        self.assertNotIn('anti-forensics', self.panel['title'] + self.panel['description'])
        self.assertNotIn("recorder's own", self.panel['title'] + self.panel['description'])
        self.assertIn('any namespace', self.panel['description'])
        self.assertIn('does not identify recorder pods', self.panel['description'])

    def test_query_accepts_nondefault_namespaces_and_respects_real_controls(self):
        self.assertNotIn('tailscale_k8s_namespace=', self.expr)
        self.assertIn('tailscale_k8s_resource=`pods`', self.expr)
        self.assertIn('tailscale_k8s_verb=`get`', self.expr)
        self.assertIn('tailscale_tailnet=~"$tailnet"', self.expr)
        self.assertIn('tailscale2otel_provider=~"$provider"', self.expr)
        self.assertIn('|~ `$log_filter`', self.expr)

    def test_identity_redaction_hides_logs_but_command_redaction_does_not(self):
        investigation = rows(self.doc)['Kubernetes audit investigations']
        _, hidden = conditions(investigation)
        self.assertIn('pii_emails', hidden)
        self.assertNotIn('pii_command_text', hidden)
        # Test the row containing the actual panel, so moving it cannot leave a false pass.
        ref = next(key for key, value in self.doc['spec']['elements'].items()
                   if value['spec'] == self.panel)
        self.assertIn(ref, str(investigation['layout']))


if __name__ == '__main__':
    unittest.main()
