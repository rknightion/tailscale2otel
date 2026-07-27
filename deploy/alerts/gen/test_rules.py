#!/usr/bin/env python3
"""Contract tests for the Grafana-managed rule generator.

Three things this file exists to pin down, none of which any other gate covers:

  * **The evaluation-policy matrix (#388).** Every alert rule must declare one of
    four named policies, and the ``(noDataState, execErrState)`` pair it emits
    must be exactly that policy's pair. The old generator defaulted every rule to
    ``execErrState: OK``, so a broken datasource read as "healthy" fleet-wide.
  * **The ``coverage_critical`` allowlist.** A rule in that class alerts on
    *absence*, which is the one policy that can page on a Grafana-side problem
    rather than a tailnet-side one. Membership is asserted as an exact set with a
    written reason per member, so it cannot drift in silently either way.
  * **Runbook and panel links (#387).** Every alert carries a ``runbook_url``
    whose anchor exists in ``docs/runbooks.md`` (and every anchored section in
    that file is referenced by at least one rule — the reverse direction, which is
    what catches a renamed rule leaving a dead section behind). Panel links are
    emitted as the file-provisioning top-level ``dashboardUid``/``panelId`` pair,
    never one without the other, and always resolve in the generated dashboard.

Run: ``python3 -m unittest discover -s deploy/alerts/gen -t deploy/alerts/gen``
"""

import importlib.util
import json
from pathlib import Path
import re
import unittest


ROOT = Path(__file__).resolve().parents[3]
RUNBOOKS = ROOT / "docs" / "runbooks.md"
DASHBOARD = ROOT / "deploy" / "grafana" / "tailscale2otel.json"


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


rules = load_module("tailscale2otel_rules", Path(__file__).with_name("build_rules.py"))


# The four documented policies, restated here rather than imported. A test that
# reads its expectations out of the code under test proves only self-consistency;
# this copy fails if the frozen seam is edited without a deliberate test change.
EXPECTED_POLICY_PAIRS = {
    "coverage_critical": ("Alerting", "Alerting"),
    "core": ("NoData", "Error"),
    "optional": ("OK", "Error"),
    "advisory": ("OK", "OK"),
}

# Grafana file provisioning accepts exactly these values; KeepLast is an HTTP-API
# / UI concept and is NOT valid here.
VALID_NODATA = {"NoData", "Alerting", "OK"}
VALID_EXECERR = {"Error", "Alerting", "OK"}

# Rules allowed to alert on absence. Keep this SMALL: a coverage_critical rule
# treats "no data" as a fault, so anything whose series is legitimately absent in
# a valid deployment must not be here. The value is the reason, kept so a future
# reader can judge an addition rather than just accept it.
COVERAGE_CRITICAL = {
    "ts2o-exporter-down": (
        "tailscale2otel_up_ratio is emitted unconditionally by every running "
        "exporter, on every provider, with no collector or feature gate. Its "
        "absence therefore has exactly one meaning — nothing is running — which "
        "is the fault this rule exists to report. It is also the rule that must "
        "survive a query error, because a datasource error reading as OK here "
        "silences the only signal that the whole pipeline stopped."
    ),
}


def alert_rules():
    out = []
    for _, group in rules.groups():
        for rule in group:
            if "record" not in rule:
                out.append(rule)
    return out


def recording_rules():
    out = []
    for _, group in rules.groups():
        for rule in group:
            if "record" in rule:
                out.append(rule)
    return out


def runbook_anchors():
    """Explicit ``{#slug}`` anchors on level-2 headings in docs/runbooks.md.

    Only ``##`` headings carrying an explicit attr_list anchor count as a rule
    family. Prose sections (the intro, the datasource/ruler-health caveat) are
    deliberately left without one so they are not required to back a rule.
    """
    pattern = re.compile(r"^##\s+.*\{#([a-z0-9-]+)\}\s*$", re.MULTILINE)
    return pattern.findall(RUNBOOKS.read_text())


class PolicyMatrixTest(unittest.TestCase):
    def test_every_policy_pair_is_a_documented_pair(self):
        self.assertEqual(EXPECTED_POLICY_PAIRS, rules.POLICY)

    def test_emitted_states_are_valid_grafana_values(self):
        for rule in alert_rules():
            with self.subTest(uid=rule["uid"]):
                self.assertIn(rule["noDataState"], VALID_NODATA)
                self.assertIn(rule["execErrState"], VALID_EXECERR)

    def test_every_alert_declares_a_known_policy(self):
        declared = rules.POLICY_BY_UID
        for rule in alert_rules():
            with self.subTest(uid=rule["uid"]):
                self.assertIn(rule["uid"], declared)
                self.assertIn(declared[rule["uid"]], EXPECTED_POLICY_PAIRS)

    def test_emitted_pair_matches_the_declared_policy(self):
        declared = rules.POLICY_BY_UID
        for rule in alert_rules():
            policy = declared[rule["uid"]]
            with self.subTest(uid=rule["uid"], policy=policy):
                self.assertEqual(
                    EXPECTED_POLICY_PAIRS[policy],
                    (rule["noDataState"], rule["execErrState"]),
                )

    def test_no_rule_is_globally_fail_open_by_accident(self):
        # The pre-#388 state: everything OK/OK. Only the advisory class may be.
        declared = rules.POLICY_BY_UID
        for rule in alert_rules():
            if (rule["noDataState"], rule["execErrState"]) == ("OK", "OK"):
                with self.subTest(uid=rule["uid"]):
                    self.assertEqual("advisory", declared[rule["uid"]])

    def test_unknown_policy_name_is_rejected(self):
        with self.assertRaises(ValueError):
            rules.alert("ts2o-bogus", "Bogus", "up", "gt", 0, "5m", "info",
                        "s", "d", policy="not-a-policy", runbook="exporter-down")

    def test_coverage_critical_membership_is_exactly_the_allowlist(self):
        declared = rules.POLICY_BY_UID
        actual = {uid for uid, p in declared.items() if p == "coverage_critical"}
        self.assertEqual(
            set(COVERAGE_CRITICAL), actual,
            "coverage_critical is the only class that pages on absence and on a "
            "query error. Adding or removing a member is a deliberate act: update "
            "COVERAGE_CRITICAL above WITH a written reason.")

    def test_coverage_critical_allowlist_has_no_stale_entries(self):
        # The staleness half: an allowlisted uid that no longer exists at all
        # would otherwise sit here forever looking like live coverage.
        live = {rule["uid"] for rule in alert_rules()}
        for uid, reason in COVERAGE_CRITICAL.items():
            with self.subTest(uid=uid):
                self.assertIn(uid, live)
                self.assertGreater(len(reason), 80, "reason must be a real justification")


class RecordingRuleTest(unittest.TestCase):
    def test_recording_rules_carry_no_alerting_fields(self):
        # Grafana-managed recording rules do not support condition, noDataState,
        # execErrState or notification_settings; a provisioning file that sets
        # them is rejected or silently ignored depending on version.
        for rule in recording_rules():
            with self.subTest(uid=rule["uid"]):
                for field in ("condition", "noDataState", "execErrState", "notification_settings"):
                    self.assertNotIn(field, rule)

    def test_recording_rules_have_zero_for(self):
        for rule in recording_rules():
            with self.subTest(uid=rule["uid"]):
                self.assertEqual("0s", rule["for"])

    def test_recording_rules_carry_no_runbook_or_panel_link(self):
        for rule in recording_rules():
            with self.subTest(uid=rule["uid"]):
                self.assertNotIn("runbook_url", rule["annotations"])
                self.assertNotIn("dashboardUid", rule)
                self.assertNotIn("panelId", rule)


class RunbookLinkTest(unittest.TestCase):
    def test_every_alert_has_a_runbook_url(self):
        for rule in alert_rules():
            with self.subTest(uid=rule["uid"]):
                url = rule["annotations"].get("runbook_url", "")
                self.assertTrue(url.startswith(rules.RUNBOOK_BASE), url)
                self.assertGreater(len(url), len(rules.RUNBOOK_BASE))

    def test_every_runbook_anchor_resolves_to_a_section(self):
        anchors = set(runbook_anchors())
        self.assertTrue(anchors, "docs/runbooks.md declares no anchored sections")
        for rule in alert_rules():
            slug = rule["annotations"]["runbook_url"][len(rules.RUNBOOK_BASE):]
            with self.subTest(uid=rule["uid"], slug=slug):
                self.assertIn(slug, anchors)

    def test_every_runbook_section_backs_at_least_one_rule(self):
        used = {r["annotations"]["runbook_url"][len(rules.RUNBOOK_BASE):] for r in alert_rules()}
        orphans = sorted(set(runbook_anchors()) - used)
        self.assertEqual([], orphans,
                         "these runbook sections are referenced by no rule — either a rule was "
                         "renamed/removed or the section is dead")

    def test_runbook_anchors_are_unique(self):
        anchors = runbook_anchors()
        self.assertEqual(len(anchors), len(set(anchors)))

    def test_annotations_carry_no_tenant_specific_identifiers(self):
        # The runbook URL is the project's public docs site; nothing else in an
        # annotation may name a stack, org or tenant.
        banned = re.compile(r"grafana\.net|grafana\.com|orgId|/a/grafana-|\bstack[-_]?id\b", re.I)
        for rule in alert_rules() + recording_rules():
            for key, value in rule["annotations"].items():
                with self.subTest(uid=rule["uid"], annotation=key):
                    self.assertIsNone(banned.search(value), value)


class PanelLinkTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        doc = json.loads(DASHBOARD.read_text())
        cls.uid = doc["metadata"]["name"]
        cls.ids = {
            element["spec"]["id"]
            for element in doc["spec"]["elements"].values()
            if element.get("kind") == "Panel"
        }

    def test_panel_link_fields_are_emitted_as_a_pair(self):
        for rule in alert_rules():
            with self.subTest(uid=rule["uid"]):
                self.assertEqual("dashboardUid" in rule, "panelId" in rule,
                                 "Grafana requires dashboardUid and panelId together")

    def test_panel_links_resolve_in_the_generated_dashboard(self):
        linked = 0
        for rule in alert_rules():
            if "panelId" not in rule:
                continue
            linked += 1
            with self.subTest(uid=rule["uid"]):
                self.assertEqual(self.uid, rule["dashboardUid"])
                self.assertIn(rule["panelId"], self.ids)
        self.assertGreater(linked, 0, "no rule links a panel; the resolver is not wired up")

    def test_panel_annotations_are_not_used(self):
        # Top-level fields are the file-provisioning form. Grafana materializes
        # the __dashboardUid__/__panelId__ annotations itself; emitting both is a
        # conflict.
        for rule in alert_rules():
            with self.subTest(uid=rule["uid"]):
                self.assertNotIn("__dashboardUid__", rule["annotations"])
                self.assertNotIn("__panelId__", rule["annotations"])

    def test_ambiguous_panel_title_is_rejected(self):
        # "Updates available" is used by three panels across three tabs.
        with self.assertRaises(ValueError):
            rules.panel_ref("Updates available")

    def test_unknown_panel_title_is_rejected(self):
        with self.assertRaises(ValueError):
            rules.panel_ref("No such panel exists anywhere")


class RuleShapeTest(unittest.TestCase):
    def test_uids_are_unique_and_within_grafana_limits(self):
        seen = set()
        for _, group in rules.groups():
            for rule in group:
                uid = rule["uid"]
                with self.subTest(uid=uid):
                    self.assertNotIn(uid, seen)
                    self.assertLessEqual(len(uid), 40)
                    self.assertRegex(uid, r"^[A-Za-z0-9_-]+$")
                seen.add(uid)

    def test_rule_counts_are_as_documented(self):
        self.assertEqual(73, len(alert_rules()))
        self.assertEqual(12, len(recording_rules()))


if __name__ == "__main__":
    unittest.main()
