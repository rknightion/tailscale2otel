"""Check datasource macros before any offline audit interpolation can mask them."""

import re
import unittest

import build
import dashboards


PROMETHEUS_RATE_INTERVAL = re.compile(
    r"\$(?:__rate_interval(?:_ms)?\b|\{__rate_interval(?:_ms)?\})"
)


def panel_queries(doc):
    for element in doc["spec"]["elements"].values():
        if element["kind"] != "Panel":
            continue
        panel = element["spec"]
        for wrapped in panel["data"]["spec"]["queries"]:
            yield panel["title"], wrapped["spec"]["query"]


class LokiMacroTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.documents = [build.build(spec) for spec in dashboards.ALL]

    def test_assembled_loki_queries_reject_prometheus_rate_interval(self):
        loki = [(title, query["spec"]["expr"])
                for doc in self.documents
                for title, query in panel_queries(doc)
                if query["datasource"]["name"] == "${ds_loki}"]
        self.assertTrue(loki, "assembled dashboards yielded no Loki panel queries")
        unsupported = [(title, expr) for title, expr in loki
                       if PROMETHEUS_RATE_INTERVAL.search(expr)]
        self.assertEqual(
            unsupported, [],
            "Loki does not interpolate Prometheus $__rate_interval; check the "
            "assembled query before replacing macros for offline execution",
        )
        self.assertTrue(any("$__auto" in expr for _, expr in loki),
                        "legitimate Loki $__auto intervals must remain available")

    def test_mixed_audit_panel_keeps_datasource_specific_intervals(self):
        queries = [query for doc in self.documents
                   for title, query in panel_queries(doc)
                   if title == "Audit: metric vs log"]
        self.assertEqual(len(queries), 2)
        by_datasource = {q["datasource"]["name"]: q["spec"]["expr"] for q in queries}
        self.assertIn("[$__rate_interval]", by_datasource["${ds_prometheus}"])
        self.assertIn("[$__auto]", by_datasource["${ds_loki}"])
        self.assertIn("rate(", by_datasource["${ds_loki}"])


if __name__ == "__main__":
    unittest.main()

class AuditRateUnitsTest(unittest.TestCase):
    def test_audit_rates_do_not_label_step_counts_as_per_second(self):
        names = {'Audit actions over time', 'Audit events by target type', 'Audit action breakdown (logs)',
                 'Top $topn actors over time', 'Top $topn targets over time', 'Log volume by event'}
        seen = set()
        for element in build.build_family()['spec']['elements'].values():
            panel = element['spec']
            unit = panel['vizConfig']['spec']['fieldConfig']['defaults'].get('unit')
            for query in panel['data']['spec']['queries']:
                spec = query['spec']['query']['spec']
                expr = spec.get('expr', '')
                if unit == 'cps' and '$__auto' in expr:
                    self.assertNotIn('count_over_time(', expr, panel['title'])
                if panel['title'] in names:
                    self.assertIn('rate(', expr)
                    self.assertIn('$__auto', expr)
                    self.assertEqual(unit, 'cps')
                    seen.add(panel['title'])
        self.assertEqual(seen, names)
