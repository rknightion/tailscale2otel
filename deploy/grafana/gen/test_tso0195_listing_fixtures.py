"""Lock real-query behavior receipts into portable CI discovery.

The JSON is a promtool rule-test fixture, executed locally when this regression
was added. Discovery needs only the stdlib: it binds every generated Overview
query to those executed expressions and freezes their expected outcomes. It
intentionally does not pretend to implement or execute PromQL in Python.
"""
import json
from pathlib import Path
import unittest

import test_tso0178_summary_presence as presence


class ListingBehaviorFixtureTest(unittest.TestCase):
    def test_generated_queries_match_executed_behavior_fixture(self):
        overview = presence.FleetSummaryPresenceTest()
        overview.setUp()
        queries = [q["spec"]["query"]["spec"]["expr"]
                   .replace("$tailnet", "fixture").replace("$provider", "tailscale")
                   for title in presence.SUMMARY_TILES
                   for q in overview.panels[title]["data"]["spec"]["queries"]]
        fixture = json.loads((Path(__file__).parent / "testdata" / "tso0195" /
                              "listing-rules.json").read_text())
        # Order is online, total, offline, users, keys. None means no sample,
        # not a reassuring zero. Wrong scope and per-entity-off stay missing.
        outcomes = {
            "missing": [None] * 5,
            "heartbeat-only": [None] * 5,
            "empty": [0] * 5,
            "failed-listing": [None] * 5,
            "populated": [2, 3, 1, 4, 1],
            "other-tailnet": [None] * 5,
            "other-provider": [None] * 5,
            "per-entity-off": [None, None, None, 0, None],
            "no-matching-predicate": [0, 1, 1, 0, 0],
        }
        self.assertEqual([case["name"] for case in fixture["tests"]], list(outcomes))
        self.assertEqual(len(queries), 5)
        for case in fixture["tests"]:
            with self.subTest(scenario=case["name"]):
                tests = case["promql_expr_test"]
                self.assertEqual([test["expr"] for test in tests], queries,
                                 "generated query changed from the executed behavior fixture")
                self.assertEqual([test["eval_time"] for test in tests], ["10m"] * 5)
                self.assertEqual([test["exp_samples"] for test in tests],
                                 [[] if value is None else [{"labels": "{}", "value": value}]
                                  for value in outcomes[case["name"]]])


if __name__ == "__main__":
    unittest.main()
