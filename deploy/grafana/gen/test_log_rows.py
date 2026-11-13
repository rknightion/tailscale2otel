"""Dense operational streams are single-line while detail remains available."""
import unittest
import build

class LogRowsTest(unittest.TestCase):
    def test_only_dense_streams_disable_wrapping(self):
        compact = {'Flow log stream', 'Recent configuration changes', 'Log stream — $log_event'}
        found = set()
        for element in build.build_family()['spec']['elements'].values():
            panel = element['spec']
            if panel['vizConfig']['group'] != 'logs':
                continue
            options = panel['vizConfig']['spec']['options']
            if panel["title"] == "Posture assertion changes":
                self.assertEqual(options, {})
                continue
            self.assertEqual(options['wrapLogMessage'], panel['title'] not in compact | {'Policy snapshots (chunked)'}, panel['title'])
            self.assertTrue(options['enableLogDetails'])
            if panel['title'] in compact:
                found.add(panel['title'])
        self.assertEqual(found, compact)
