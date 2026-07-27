#!/usr/bin/env python3
"""Contract tests for the Grafana-managed rule generator.

What this file exists to pin down, none of which any other gate covers:

  * **The manifest format.** The generator emits `rules.alerting.grafana.app`
    manifests consumed by `gcx resources push`, NOT the `apiVersion: 1` file-
    provisioning document it used to emit. The two formats disagree on nearly
    every field name and on the CASING of the state enums, and a wrong value is
    accepted by every local check and then rejected by the API at push time.
  * **The evaluation-policy matrix (#388).** Every alert rule must declare one of
    four named policies, and the ``(noDataState, execErrState)`` pair it emits
    must be exactly that policy's pair. The old generator defaulted every rule to
    a fail-open error state, so a broken datasource read as "healthy" fleet-wide.
  * **The ``coverage_critical`` allowlist.** A rule in that class alerts on
    *absence*, which is the one policy that can page on a Grafana-side problem
    rather than a tailnet-side one. Membership is asserted as an exact set with a
    written reason per member, so it cannot drift in silently either way.
  * **Runbook and panel links (#387).** Every alert carries a ``runbook_url``
    whose anchor exists in ``docs/runbooks.md`` (and every anchored section in
    that file is referenced by at least one rule — the reverse direction, which is
    what catches a renamed rule leaving a dead section behind). Panel links are
    emitted as the paired ``__dashboardUid__``/``__panelId__`` ANNOTATIONS, which
    is the only form this API has, and always resolve in the generated dashboard.
  * **The test-only Prometheus rendering.** It is the only thing that can be
    EXECUTED (by ``promtool test rules``), so its alert names must be unique and
    its expressions must be renderable for every non-Loki rule.

Run: ``python3 -m unittest discover -s deploy/alerts/gen -t deploy/alerts/gen``
"""

import importlib.util
import json
from pathlib import Path
import re
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[3]
RUNBOOKS = ROOT / "docs" / "runbooks.md"
DASHBOARD = ROOT / "deploy" / "grafana" / "tailscale2otel.json"
MANIFEST_DIR = ROOT / "deploy" / "alerts" / "grafana-managed"


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


rules = load_module("tailscale2otel_rules", Path(__file__).with_name("build_rules.py"))
validate = load_module("tailscale2otel_validate", Path(__file__).with_name("validate_manifests.py"))


# The four documented policies, restated here rather than imported. A test that
# reads its expectations out of the code under test proves only self-consistency;
# this copy fails if the frozen seam is edited without a deliberate test change.
#
# CASING IS THE POINT. rules.alerting.grafana.app spells the no-data "OK" state
# "Ok", and the exec-error one "OK". The asymmetry is real, it is not a typo here,
# and getting it wrong makes every rule un-deployable via `gcx resources push`
# while every local check still passes.
EXPECTED_POLICY_PAIRS = {
    "coverage_critical": ("Alerting", "Alerting"),
    "core": ("NoData", "Error"),
    "optional": ("Ok", "Error"),
    "advisory": ("Ok", "OK"),
}

VALID_NODATA = {"NoData", "Alerting", "Ok", "KeepLast"}
VALID_EXECERR = {"Error", "Alerting", "OK", "KeepLast"}

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
    return [r for _, group in rules.groups() for r in group if not rules.is_recording(r)]


def recording_rules():
    return [r for _, group in rules.groups() for r in group if rules.is_recording(r)]


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

    def test_nodata_ok_is_spelled_Ok_not_OK(self):
        # The single most expensive mistake available in this file: "OK" is the
        # provisioning-format spelling and the API rejects it, but nothing local
        # catches it. Assert the exact byte sequence.
        self.assertEqual("Ok", rules.POLICY["optional"][0])
        self.assertEqual("Ok", rules.POLICY["advisory"][0])
        self.assertEqual("OK", rules.POLICY["advisory"][1])

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
        # The pre-#388 state: everything fail-open. Only the advisory class may be.
        declared = rules.POLICY_BY_UID
        for rule in alert_rules():
            if (rule["noDataState"], rule["execErrState"]) == ("Ok", "OK"):
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
        # rules.alerting.grafana.app/v0alpha1 RecordingRule declares
        # additionalProperties: false, and its property set is exactly
        # {title, trigger, metric, expressions, targetDatasourceUID, labels,
        # paused}. Anything else is rejected outright — including `annotations`,
        # which is why a recording rule's prose description is generator-internal
        # (`_desc`) and never emitted.
        for rule in recording_rules():
            with self.subTest(uid=rule["uid"]):
                for field in ("condition", "noDataState", "execErrState",
                              "notificationSettings", "annotations", "for"):
                    self.assertNotIn(field, rule)

    def test_recording_rules_carry_the_required_spec_fields(self):
        for rule in recording_rules():
            with self.subTest(uid=rule["uid"]):
                for field in ("title", "trigger", "metric", "expressions",
                              "targetDatasourceUID"):
                    self.assertIn(field, rule)

    def test_recording_rules_carry_no_runbook_or_panel_link(self):
        for rule in recording_rules():
            with self.subTest(uid=rule["uid"]):
                self.assertNotIn("dashboardUid", rule)
                self.assertNotIn("panelId", rule)
                self.assertTrue(rule["_desc"], "a recorded metric still needs a written reason")

    def test_recording_rule_source_node_is_marked(self):
        # Grafana records the value of the refId marked `source`. Without it the
        # rule imports but records nothing.
        for rule in recording_rules():
            with self.subTest(uid=rule["uid"]):
                sources = [k for k, v in rule["expressions"].items() if v.get("source")]
                self.assertEqual(1, len(sources), sources)


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
        for rule in alert_rules():
            for key, value in rule["annotations"].items():
                with self.subTest(uid=rule["uid"], annotation=key):
                    self.assertIsNone(banned.search(value), value)
        for rule in recording_rules():
            with self.subTest(uid=rule["uid"]):
                self.assertIsNone(banned.search(rule["_desc"]), rule["_desc"])


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

    def test_panel_link_annotations_are_emitted_as_a_pair(self):
        for rule in alert_rules():
            ann = rule["annotations"]
            with self.subTest(uid=rule["uid"]):
                self.assertEqual("__dashboardUid__" in ann, "__panelId__" in ann,
                                 "Grafana requires __dashboardUid__ and __panelId__ together")

    def test_panel_links_resolve_in_the_generated_dashboard(self):
        linked = 0
        for rule in alert_rules():
            ann = rule["annotations"]
            if "__panelId__" not in ann:
                continue
            linked += 1
            with self.subTest(uid=rule["uid"]):
                self.assertEqual(self.uid, ann["__dashboardUid__"])
                # Annotation values are strings; a bare int is silently dropped.
                self.assertIsInstance(ann["__panelId__"], str)
                self.assertIn(int(ann["__panelId__"]), self.ids)
        self.assertGreater(linked, 0, "no rule links a panel; the resolver is not wired up")

    def test_provisioning_only_top_level_panel_fields_are_not_used(self):
        # dashboardUid/panelId as TOP-LEVEL rule fields belong to the apiVersion: 1
        # provisioning format. rules.alerting.grafana.app declares
        # additionalProperties: false, so emitting them is a hard rejection.
        for rule in alert_rules():
            with self.subTest(uid=rule["uid"]):
                self.assertNotIn("dashboardUid", rule)
                self.assertNotIn("panelId", rule)

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
        self.assertEqual(97, len(alert_rules()))
        self.assertEqual(23, len(recording_rules()))

    def test_durations_are_go_style_strings(self):
        # `for` and relativeTimeRange are Go duration STRINGS in this API, not the
        # integer seconds the provisioning format used. "5m" alone is accepted by
        # nothing here; it has to be "5m0s".
        pattern = re.compile(r"^(?:0s|(?:\d+h)?(?:\d+m)?\d+s)$")
        for rule in alert_rules():
            with self.subTest(uid=rule["uid"]):
                self.assertRegex(rule["for"], pattern)
                for ref, node in rule["expressions"].items():
                    if "relativeTimeRange" in node:
                        self.assertRegex(node["relativeTimeRange"]["from"], pattern, ref)
                        self.assertRegex(node["relativeTimeRange"]["to"], pattern, ref)

    def test_expressions_are_a_map_keyed_by_refid_with_one_source(self):
        for rule in alert_rules():
            with self.subTest(uid=rule["uid"]):
                self.assertIsInstance(rule["expressions"], dict)
                self.assertEqual(["A", "B", "C"], sorted(rule["expressions"]))
                for ref, node in rule["expressions"].items():
                    self.assertEqual(ref, node["model"]["refId"])
                sources = [k for k, v in rule["expressions"].items() if v.get("source")]
                self.assertEqual(["C"], sources)

    def test_every_rule_has_a_trigger_interval(self):
        for _, group in rules.groups():
            for rule in group:
                with self.subTest(uid=rule["uid"]):
                    self.assertEqual({"interval": rules.INTERVAL}, rule["trigger"])


class AlertableSignalCoverageTest(unittest.TestCase):
    """Signals that must stay referenced by at least one alert expression.

    These five were the entire alerting coverage of the hand-maintained
    Prometheus-ruler file (`deploy/alerts/tailscale2otel.rules.yaml`) that the
    move to Grafana-managed manifests deleted. Deleting that file silently took
    five behaviours with it while the signals kept claiming `alertable` in
    `internal/catalog`'s disposition data — a coverage regression no format,
    link or policy check above could see, because every remaining rule stayed
    perfectly valid.

    The Go-side disposition gate catches the same thing from the other end; this
    is the generator-side half, so a rule deleted here fails in the file that
    deleted it rather than three packages away.
    """

    # metric -> the uid that is expected to carry it.
    REQUIRED = {
        "tailscale2otel_scrape_errors_total": "ts2o-collector-scrape-error-rate",
        "tailscale2otel_export_failures_total": "ts2o-otlp-export-failures",
        "tailscale_node_up_ratio": "ts2o-nodemetrics-target-down",
        "tailscale_network_flow_logs_dropped_total": "ts2o-flow-logs-dropped",
        "tailscale_webhook_events_total": "ts2o-node-ip-forwarding",
    }

    def test_each_signal_is_referenced_by_its_rule(self):
        by_uid = {r["uid"]: r for r in alert_rules()}
        for metric, uid in self.REQUIRED.items():
            with self.subTest(metric=metric, uid=uid):
                self.assertIn(uid, by_uid, "the rule restoring %s is gone" % metric)
                self.assertIn(metric, rules.rule_expr(by_uid[uid]))

    def test_each_signal_reaches_a_shipped_manifest(self):
        # The end-to-end form of the check the disposition gate performs:
        # `rg -l <metric> deploy/alerts/grafana-managed/` must be non-empty.
        blobs = [p.read_text() for p in MANIFEST_DIR.glob("*.json")]
        for metric in self.REQUIRED:
            with self.subTest(metric=metric):
                self.assertTrue(any(metric in b for b in blobs),
                                "%s is referenced by no shipped manifest" % metric)

    def test_the_webhook_rule_still_selects_both_ip_forwarding_types(self):
        # The rule's whole value is the two-type selector: these health events
        # arrive ONLY by webhook and are emitted at INFO, so nothing else
        # surfaces them. A widened selector would make it a generic webhook-rate
        # alert; a narrowed one would silently drop exit nodes or subnet routers.
        expr = rules.rule_expr(rules.rules_by_uid()["ts2o-node-ip-forwarding"])
        self.assertIn("exitNodeIPForwardingNotEnabled", expr)
        self.assertIn("subnetIPForwardingNotEnabled", expr)


class NewAlertRulesTest(unittest.TestCase):
    """Load-bearing properties of the 13 rules added for #399 #401 #402 #404 #405.

    Each of these guards something that would silently regress: a check that
    only looked at the rule's metric name or uid would not catch someone
    "simplifying" an expression away from the property that actually matters.
    """

    # uid -> policy, exactly as the frozen spec (ALERT_SPEC.md) declares it.
    NEW_RULE_POLICIES = {
        "ts2o-objectstore-undecodable": "optional",
        "ts2o-objectstore-gap-aging": "optional",
        "ts2o-objectstore-export-stale": "optional",
        "ts2o-objectstore-backlog-stuck": "optional",
        "ts2o-api-rate-limit-wait-high": "advisory",
        "ts2o-node-error-drops": "optional",
        "ts2o-peer-relay-stuck": "optional",
        "ts2o-rdns-cache-overflowing": "optional",
        "ts2o-stream-records-skipped": "optional",
        "ts2o-webhook-schema-drift": "optional",
        "ts2o-nodemetrics-name-budget": "optional",
        "ts2o-device-key-expiry-disabled-new": "optional",
        "ts2o-device-multiple-connections": "optional",
    }

    def test_every_new_uid_exists_with_a_panel_link_and_its_declared_policy(self):
        by_uid = rules.rules_by_uid()
        declared = rules.POLICY_BY_UID
        for uid, policy in self.NEW_RULE_POLICIES.items():
            with self.subTest(uid=uid):
                self.assertIn(uid, by_uid, "rule is missing from the catalogue")
                rule = by_uid[uid]
                ann = rule["annotations"]
                self.assertIn("__dashboardUid__", ann)
                self.assertIn("__panelId__", ann)
                self.assertEqual(policy, declared[uid])

    def test_objectstore_export_stale_keeps_both_the_clamp_and_sentinel_arms(self):
        # -1 is a no-discovery sentinel, not an age. A test that only checked
        # the metric name would not catch a "simplification" back to a plain
        # `> 3600`, which would silently exclude the worst case (nothing
        # discovered at all).
        expr = rules.rule_expr(rules.rules_by_uid()["ts2o-objectstore-export-stale"])
        self.assertIn("clamp_min(", expr)
        self.assertIn("== bool -1", expr)

    def test_node_error_drops_never_selects_acl(self):
        # #402: an ACL drop is the packet filter working, not a fault.
        expr = rules.rule_expr(rules.rules_by_uid()["ts2o-node-error-drops"])
        self.assertIn("error|unknown_protocol|other", expr)
        self.assertNotIn("acl", expr)

    def test_objectstore_undecodable_ships_enabled_the_other_three_paused(self):
        # Any non-zero value here means a broken feed (never acceptable), while
        # the object-store path is entirely absent unless configured — so this
        # one rule is safe to ship live and the rest ship paused.
        by_uid = rules.rules_by_uid()
        self.assertFalse(by_uid["ts2o-objectstore-undecodable"]["paused"])
        for uid in (
            "ts2o-objectstore-gap-aging",
            "ts2o-objectstore-export-stale",
            "ts2o-objectstore-backlog-stuck",
        ):
            with self.subTest(uid=uid):
                self.assertTrue(by_uid[uid]["paused"])

    def test_device_key_expiry_disabled_new_alerts_on_delta_not_level(self):
        # #401 forbids alerting on raw existence: a standing population of
        # never-expiring (tag-owned) keys is normal. Only a NEW one appearing
        # is actionable.
        expr = rules.rule_expr(rules.rules_by_uid()["ts2o-device-key-expiry-disabled-new"])
        self.assertIn("delta(tailscale_devices_key_expiry_disabled_ratio", expr)

    def test_rate_limit_wait_is_distinct_from_upstream_api_latency(self):
        # Client-side self-throttling vs upstream API/network slowness are
        # different faults with different fixes; they must not share a query.
        expr = rules.rule_expr(rules.rules_by_uid()["ts2o-api-rate-limit-wait-high"])
        self.assertIn("tailscale2otel_api_rate_limit_wait_seconds_bucket", expr)
        self.assertNotIn("api_duration_seconds", expr)

    def test_rate_limited_429_is_distinct_from_client_side_wait(self):
        # The server-side 429 rule must not have picked up the client-side
        # rate-limiter-wait signal either — they page on different faults.
        expr = rules.rule_expr(rules.rules_by_uid()["ts2o-api-rate-limited"])
        self.assertNotIn("rate_limit_wait", expr)

    def test_network_store_dropped_is_never_alerted(self):
        # The flow-view store drop only trims a local in-memory view; OTLP
        # emission is unaffected and nothing is lost, so it is deliberately
        # NOT alerted. Without this test a future pass could "fill the
        # coverage gap" and add an unactionable alert on it.
        for rule in alert_rules():
            with self.subTest(uid=rule["uid"]):
                self.assertNotIn("tailscale_network_store_dropped_total",
                                 rules.rule_expr(rule))

    def test_webhook_duplicates_is_never_alerted(self):
        # A counted duplicate is the dedup failsafe working, not a fault.
        for rule in alert_rules():
            with self.subTest(uid=rule["uid"]):
                self.assertNotIn("tailscale_webhook_duplicates_total",
                                 rules.rule_expr(rule))


class PausedRecordingGateTest(unittest.TestCase):
    """The load-bearing new gate for this wave (#406/#398/#410).

    ``record(...)`` defaults to ``paused=True``. An alert that queries a
    recorded metric whose recording rule is paused gets NO series at all — and
    under ``optional`` policy (``noDataState: Ok``) that alert is then
    silently dead forever while every offline check, including every OTHER
    test in this file, keeps passing. This is the same class of silent
    failure as the ``noDataState`` casing trap: nothing local can see it
    except a test that looks at both sides of the dependency.

    Derived entirely from the catalogue — never a hard-coded uid/metric list,
    because a hard-coded list is exactly the kind of thing that rots the
    moment a new alert starts consuming a new recording.
    """

    def test_every_recording_rule_consumed_by_an_alert_is_not_paused(self):
        alert_exprs = " ".join(rules.rule_expr(r) for r in alert_rules())
        for rec in recording_rules():
            if rec["metric"] in alert_exprs:
                with self.subTest(metric=rec["metric"], uid=rec["uid"]):
                    self.assertFalse(
                        rec["paused"],
                        "%s (%s) is paused but at least one alert expression queries it. "
                        "That alert would get NO series at all, and under `optional` "
                        "policy (noDataState: Ok) it would be silently dead forever while "
                        "every offline check still passes. Ship this recording rule "
                        "enabled (paused=False)." % (rec["metric"], rec["uid"]))


class NoRawRollupDoubleCountTest(unittest.TestCase):
    """#406: a recording rule must never ADD the rollup and raw flow-byte
    counters together.

    ``ts2o-rec-flow-throughput`` combines them with `or` (fall back to the raw
    counter only when the rollup series is entirely absent) — the correct
    construction, because a deployment that emits BOTH would have every flow
    byte counted twice under `+`. `or` picks one series set per label
    combination; `+` sums both unconditionally.
    """

    def test_recording_rules_combining_raw_and_rollup_flow_bytes_use_or_not_plus(self):
        rollup = "tailscale_network_io_rollup_bytes_total"
        raw = "tailscale_network_io_bytes_total"
        seen_both = False
        for rec in recording_rules():
            expr = rules.rule_expr(rec)
            if rollup in expr and raw in expr:
                seen_both = True
                with self.subTest(uid=rec["uid"]):
                    parts = expr.split(" or ")
                    self.assertEqual(
                        2, len(parts),
                        "expected exactly one ` or ` splitting the rollup fallback from "
                        "the raw fallback; got: %r" % expr)
                    self.assertIn(rollup, parts[0])
                    self.assertNotIn(raw, parts[0])
                    self.assertIn(raw, parts[1])
                    self.assertNotIn(rollup, parts[1])
        self.assertTrue(seen_both, "no recording rule references both raw and rollup "
                         "flow-byte counters, so this guard is untested")


class PathByteFractionConstructionTest(unittest.TestCase):
    """The DERP/direct byte-fraction pair share one construction (#406).

    ``_derp_byte_fraction`` was generalised into ``_path_byte_fraction``, and
    the DERP recording rule's shipped expression must come out byte-identical.
    Both directions must be unioned via `label_replace(...) or
    label_replace(...)`, never summed with `rate(in) + rate(out)` — a
    one-to-one join on that `+` silently drops any node present in only one
    direction (asymmetric relay traffic).
    """

    def test_derp_byte_fraction_delegates_to_path_byte_fraction(self):
        self.assertEqual(rules._derp_byte_fraction(), rules._path_byte_fraction("derp"))

    def test_derp_and_direct_recorded_expressions_use_label_replace_union(self):
        by_uid = rules.rules_by_uid()
        for uid, path in (("ts2o-rec-derp-byte-fraction", "derp"),
                          ("ts2o-rec-direct-byte-fraction", "direct")):
            with self.subTest(uid=uid):
                expr = rules.rule_expr(by_uid[uid])
                self.assertIn("label_replace(", expr)
                self.assertIn(") or label_replace(", expr)
                self.assertNotRegex(
                    expr, r"rate\([^)]*inbound_bytes_total[^)]*\)\s*\+\s*rate\(",
                    "a one-to-one `+` join silently drops any node present in only one "
                    "direction (asymmetric relay traffic); the union construction exists "
                    "precisely to avoid that")


class SLOBurnExpressionTest(unittest.TestCase):
    """#398: the four burn-rate alerts must use a PRODUCT of two `> bool`
    comparisons, never `and`.

    `a and b` (Prometheus vector matching) returns *a's value* wherever both
    sides have matching labels, so the alert's effective threshold would then
    depend on the magnitude of the short-window error rate instead of on both
    windows simply agreeing that they breached — which defeats the entire
    point of requiring two windows to suppress a single blip.
    """

    SLO_UIDS = (
        "ts2o-slo-availability-fast-burn",
        "ts2o-slo-availability-slow-burn",
        "ts2o-slo-freshness-fast-burn",
        "ts2o-slo-delivery-fast-burn",
    )
    SLI_METRICS = (
        "tailscale2otel:sli_availability:ratio",
        "tailscale2otel:sli_freshness:ratio",
        "tailscale2otel:sli_delivery:ratio",
    )

    def test_burn_expressions_are_a_bool_product_referencing_one_sli_over_two_windows(self):
        by_uid = rules.rules_by_uid()
        for uid in self.SLO_UIDS:
            expr = rules.rule_expr(by_uid[uid])
            with self.subTest(uid=uid):
                self.assertEqual(2, expr.count("> bool"), expr)
                self.assertNotIn(" and ", expr,
                                 "`and` returns the left operand's VALUE, making the "
                                 "threshold depend on the error rate's magnitude instead "
                                 "of on both windows agreeing")
                referenced = [m for m in self.SLI_METRICS if m in expr]
                self.assertEqual(1, len(referenced), expr)
                windows = re.findall(r"avg_over_time\([^\[]+\[(\w+)\]\)", expr)
                self.assertEqual(2, len(windows), expr)
                self.assertNotEqual(windows[0], windows[1],
                                    "the two windows must differ; a burn alert with "
                                    "identical short/long windows is not multi-window")


class SLISeparationTest(unittest.TestCase):
    """#398: the three SLIs must stay three separate recording rules.

    Blending availability/freshness/delivery into one number makes a backend
    outage read as an exporter failure — a Grafana Cloud incident would then
    page whoever owns the tailnet, not whoever owns the OTLP backend. This is
    the failure #398 exists to prevent.
    """

    SLI_METRICS = (
        "tailscale2otel:sli_availability:ratio",
        "tailscale2otel:sli_freshness:ratio",
        "tailscale2otel:sli_delivery:ratio",
    )

    def test_three_slis_are_distinct_recording_rules_with_distinct_expressions(self):
        by_metric = {r["metric"]: rules.rule_expr(r) for r in recording_rules()
                     if r["metric"] in self.SLI_METRICS}
        self.assertEqual(set(self.SLI_METRICS), set(by_metric))
        self.assertEqual(3, len(set(by_metric.values())),
                         "the three SLIs must not share an expression: %r" % by_metric)

    def test_delivery_burn_alert_references_only_the_delivery_sli(self):
        expr = rules.rule_expr(rules.rules_by_uid()["ts2o-slo-delivery-fast-burn"])
        self.assertIn("tailscale2otel:sli_delivery:ratio", expr)
        self.assertNotIn("tailscale2otel:sli_availability:ratio", expr)
        self.assertNotIn("tailscale2otel:sli_freshness:ratio", expr)


class ApprovalWorkflowTest(unittest.TestCase):
    """#410: unauthorized-device / stale-invite approval alerts."""

    def test_devices_unauthorized_recording_excludes_external_devices(self):
        # A shared-in device from another tailnet is not yours to approve;
        # counting it as "awaiting approval" would produce an alert nobody
        # can action.
        expr = rules.rule_expr(rules.rules_by_uid()["ts2o-rec-devices-unauthorized"])
        self.assertIn('tailscale_external="false"', expr)
        self.assertIn('tailscale_authorized="false"', expr)

    def test_approval_alerts_are_not_page_tier(self):
        # #410 explicitly forbids a default page-level severity: an unapproved
        # device is a queue to work through, not an incident.
        by_uid = rules.rules_by_uid()
        for uid in ("ts2o-devices-unauthorized", "ts2o-user-invites-stale"):
            with self.subTest(uid=uid):
                self.assertEqual("false", by_uid[uid]["labels"].get("page"))


class WaveBNewAlertsTableTest(unittest.TestCase):
    """The 6 new alert uids exist with the policy the frozen spec declares,
    and the panel-link split (4 SLO rules with none, 2 approval rules with
    one) is asserted on BOTH halves so "no panel" stays a recorded decision
    rather than looking like an omission."""

    NEW_ALERT_POLICIES = {
        "ts2o-slo-availability-fast-burn": "core",
        "ts2o-slo-availability-slow-burn": "core",
        "ts2o-slo-freshness-fast-burn": "core",
        "ts2o-slo-delivery-fast-burn": "optional",
        "ts2o-devices-unauthorized": "optional",
        "ts2o-user-invites-stale": "optional",
    }
    NO_PANEL_UIDS = (
        "ts2o-slo-availability-fast-burn",
        "ts2o-slo-availability-slow-burn",
        "ts2o-slo-freshness-fast-burn",
        "ts2o-slo-delivery-fast-burn",
    )
    HAS_PANEL_UIDS = (
        "ts2o-devices-unauthorized",
        "ts2o-user-invites-stale",
    )

    def test_new_uids_exist_with_their_declared_policy(self):
        by_uid = rules.rules_by_uid()
        declared = rules.POLICY_BY_UID
        for uid, policy in self.NEW_ALERT_POLICIES.items():
            with self.subTest(uid=uid):
                self.assertIn(uid, by_uid, "rule is missing from the catalogue")
                self.assertEqual(policy, declared[uid])

    def test_the_four_slo_rules_deliberately_carry_no_panel_link(self):
        by_uid = rules.rules_by_uid()
        for uid in self.NO_PANEL_UIDS:
            with self.subTest(uid=uid):
                ann = by_uid[uid]["annotations"]
                self.assertNotIn("__panelId__", ann)
                self.assertNotIn("__dashboardUid__", ann)

    def test_the_two_approval_rules_carry_a_panel_link(self):
        by_uid = rules.rules_by_uid()
        for uid in self.HAS_PANEL_UIDS:
            with self.subTest(uid=uid):
                ann = by_uid[uid]["annotations"]
                self.assertIn("__panelId__", ann)
                self.assertIn("__dashboardUid__", ann)


class ManifestEmitTest(unittest.TestCase):
    """The emit layer, exercised into a temp dir so it never touches the tree."""

    def test_emitted_manifests_validate(self):
        with tempfile.TemporaryDirectory() as tmp:
            rules.emit_grafana_managed(tmp)
            errors = validate.validate_dir(Path(tmp))
        self.assertEqual([], errors)

    def test_committed_manifests_validate(self):
        # The shipped directory, not a fresh render: a hand-edited or stale file
        # is exactly what this catches. Drift against the generator is the CI
        # dashboards-drift job's job, not this one's.
        self.assertTrue(MANIFEST_DIR.is_dir(), f"{MANIFEST_DIR} is missing")
        self.assertEqual([], validate.validate_dir(MANIFEST_DIR))

    def test_one_file_per_rule_plus_the_folder(self):
        with tempfile.TemporaryDirectory() as tmp:
            rules.emit_grafana_managed(tmp)
            names = sorted(p.name for p in Path(tmp).glob("*.json"))
        expected = sorted(
            ["_folder.json"]
            + [f"{r['uid']}.json" for _, g in rules.groups() for r in g]
        )
        self.assertEqual(expected, names)

    def test_stale_manifests_are_deleted_before_writing(self):
        # A renamed or removed rule must not linger as a file that still pushes.
        with tempfile.TemporaryDirectory() as tmp:
            stale = Path(tmp) / "ts2o-rule-that-no-longer-exists.json"
            stale.write_text("{}\n")
            rules.emit_grafana_managed(tmp)
            self.assertFalse(stale.exists())

    def test_output_is_deterministic(self):
        with tempfile.TemporaryDirectory() as a, tempfile.TemporaryDirectory() as b:
            rules.emit_grafana_managed(a)
            rules.emit_grafana_managed(b)
            for pa in sorted(Path(a).glob("*.json")):
                pb = Path(b) / pa.name
                with self.subTest(name=pa.name):
                    self.assertEqual(pa.read_bytes(), pb.read_bytes())

    def test_every_manifest_ends_with_a_newline(self):
        with tempfile.TemporaryDirectory() as tmp:
            rules.emit_grafana_managed(tmp)
            for p in Path(tmp).glob("*.json"):
                with self.subTest(name=p.name):
                    self.assertTrue(p.read_bytes().endswith(b"}\n"))

    def test_folder_manifest_shape(self):
        with tempfile.TemporaryDirectory() as tmp:
            rules.emit_grafana_managed(tmp)
            doc = json.loads((Path(tmp) / "_folder.json").read_text())
        self.assertEqual("folder.grafana.app/v1beta1", doc["apiVersion"])
        self.assertEqual("Folder", doc["kind"])
        self.assertEqual(rules.FOLDER, doc["metadata"]["name"])
        self.assertTrue(doc["spec"]["title"])

    def test_every_rule_manifest_declares_its_folder_both_ways(self):
        with tempfile.TemporaryDirectory() as tmp:
            rules.emit_grafana_managed(tmp)
            for p in sorted(Path(tmp).glob("*.json")):
                if p.name == "_folder.json":
                    continue
                doc = json.loads(p.read_text())
                with self.subTest(name=p.name):
                    self.assertEqual(rules.FOLDER, doc["metadata"]["annotations"]["grafana.app/folder"])
                    self.assertEqual(rules.FOLDER, doc["metadata"]["labels"]["grafana.app/folder"])

    def test_internal_keys_never_reach_a_manifest(self):
        # The catalogue dicts carry generator-internal keys (`uid`, `_prom`,
        # `_desc`) that the API's additionalProperties: false would reject.
        with tempfile.TemporaryDirectory() as tmp:
            rules.emit_grafana_managed(tmp)
            for p in Path(tmp).glob("*.json"):
                doc = json.loads(p.read_text())
                with self.subTest(name=p.name):
                    self.assertNotIn("uid", doc)
                    for key in doc.get("spec", {}):
                        self.assertFalse(key.startswith("_"), key)
                        self.assertNotEqual("uid", key)


class ValidatorTest(unittest.TestCase):
    """The validator must actually reject the things it claims to."""

    def _write(self, tmp, name, doc):
        p = Path(tmp) / name
        p.write_text(json.dumps(doc, indent=2, sort_keys=True) + "\n")
        return p

    def _good_alert(self):
        with tempfile.TemporaryDirectory() as tmp:
            rules.emit_grafana_managed(tmp)
            return json.loads((Path(tmp) / "ts2o-exporter-down.json").read_text())

    def _good_recording(self):
        with tempfile.TemporaryDirectory() as tmp:
            rules.emit_grafana_managed(tmp)
            return json.loads((Path(tmp) / "ts2o-rec-devices-online.json").read_text())

    def _errors_for(self, mutate, base=None, name="probe.json"):
        with tempfile.TemporaryDirectory() as tmp:
            rules.emit_grafana_managed(tmp)
            doc = base if base is not None else self._good_alert()
            mutate(doc)
            self._write(tmp, name, doc)
            return validate.validate_dir(Path(tmp))

    def test_rejects_uppercase_OK_nodatastate(self):
        errors = self._errors_for(lambda d: d["spec"].update(noDataState="OK"))
        self.assertTrue(any("noDataState" in e for e in errors), errors)

    def test_rejects_lowercase_ok_execerrstate(self):
        errors = self._errors_for(lambda d: d["spec"].update(execErrState="Ok"))
        self.assertTrue(any("execErrState" in e for e in errors), errors)

    def test_rejects_bad_apiversion(self):
        errors = self._errors_for(lambda d: d.update(apiVersion="rules.alerting.grafana.app/v1"))
        self.assertTrue(any("apiVersion" in e for e in errors), errors)

    def test_rejects_unknown_kind(self):
        errors = self._errors_for(lambda d: d.update(kind="RuleGroup"))
        self.assertTrue(any("kind" in e for e in errors), errors)

    def test_rejects_overlong_metadata_name(self):
        errors = self._errors_for(lambda d: d["metadata"].update(name="x" * 41))
        self.assertTrue(any("metadata.name" in e for e in errors), errors)

    def test_rejects_illegal_metadata_name_charset(self):
        errors = self._errors_for(lambda d: d["metadata"].update(name="not a uid!"))
        self.assertTrue(any("metadata.name" in e for e in errors), errors)

    def test_rejects_a_lone_panel_annotation(self):
        errors = self._errors_for(lambda d: d["spec"]["annotations"].pop("__panelId__", None)
                                  or d["spec"]["annotations"].update(__dashboardUid__="x"))
        self.assertTrue(any("__panelId__" in e or "__dashboardUid__" in e for e in errors), errors)

    def test_rejects_non_string_panel_id(self):
        def mutate(d):
            d["spec"]["annotations"]["__dashboardUid__"] = "x"
            d["spec"]["annotations"]["__panelId__"] = 12
        errors = self._errors_for(mutate)
        self.assertTrue(any("__panelId__" in e for e in errors), errors)

    def test_rejects_alert_only_fields_on_a_recording_rule(self):
        errors = self._errors_for(lambda d: d["spec"].update(noDataState="Ok"),
                                  base=self._good_recording())
        self.assertTrue(any("RecordingRule" in e for e in errors), errors)

    def test_rejects_a_missing_folder_manifest(self):
        with tempfile.TemporaryDirectory() as tmp:
            rules.emit_grafana_managed(tmp)
            (Path(tmp) / "_folder.json").unlink()
            errors = validate.validate_dir(Path(tmp))
        self.assertTrue(any("_folder.json" in e for e in errors), errors)

    def test_rejects_malformed_json(self):
        with tempfile.TemporaryDirectory() as tmp:
            rules.emit_grafana_managed(tmp)
            (Path(tmp) / "broken.json").write_text("{not json")
            errors = validate.validate_dir(Path(tmp))
        self.assertTrue(any("broken.json" in e for e in errors), errors)

    def test_empty_directory_is_an_error_not_a_pass(self):
        with tempfile.TemporaryDirectory() as tmp:
            self.assertNotEqual([], validate.validate_dir(Path(tmp)))


class PrometheusRenderingTest(unittest.TestCase):
    """The TEST-ONLY rendering. Never committed, never shipped — but promtool is
    the only thing in the repo that EXECUTES a rule, so it has to be renderable."""

    def test_alert_names_are_unique_and_identifier_shaped(self):
        names = [rules.prom_alertname(r["title"]) for r in alert_rules()]
        self.assertEqual(len(names), len(set(names)), "duplicate promtool alertname")
        for n in names:
            with self.subTest(name=n):
                self.assertRegex(n, r"^[A-Za-z][A-Za-z0-9]*$")

    def test_loki_rules_are_excluded(self):
        doc = rules.build_prom_rules()
        rendered = json.dumps(doc)
        self.assertNotIn("count_over_time({", rendered,
                         "a LogQL expression reached the Prometheus rendering; promtool "
                         "cannot parse it and the whole file fails to load")
        loki = [r for r in alert_rules() if r["_prom"]["ds"] == rules.LOKI]
        self.assertTrue(loki, "no Loki-backed rule left, so this exclusion is untested")

    def test_every_prometheus_backed_rule_is_rendered(self):
        doc = rules.build_prom_rules()
        rendered = {r.get("alert") for g in doc["groups"] for r in g["rules"]}
        for rule in alert_rules():
            if rule["_prom"]["ds"] != rules.PROM:
                continue
            with self.subTest(uid=rule["uid"]):
                self.assertIn(rules.prom_alertname(rule["title"]), rendered)

    def test_recording_rules_are_rendered(self):
        doc = rules.build_prom_rules()
        recorded = {r.get("record") for g in doc["groups"] for r in g["rules"]}
        for rule in recording_rules():
            with self.subTest(uid=rule["uid"]):
                self.assertIn(rule["metric"], recorded)

    def test_coverage_critical_rules_gain_an_absent_arm(self):
        # Grafana expresses "absence is the alert" with noDataState: Alerting.
        # Prometheus has no such concept, so the only faithful rendering is an
        # explicit `or absent(...)`. Without it the test-only file would quietly
        # drop the exact semantics the absent() fixture case exists to prove.
        doc = rules.build_prom_rules()
        by_name = {r.get("alert"): r for g in doc["groups"] for r in g["rules"]}
        for uid in COVERAGE_CRITICAL:
            rule = next(r for r in alert_rules() if r["uid"] == uid)
            with self.subTest(uid=uid):
                self.assertIn("absent(", by_name[rules.prom_alertname(rule["title"])]["expr"])

    def test_non_coverage_rules_do_not_gain_an_absent_arm(self):
        doc = rules.build_prom_rules()
        by_name = {r["alert"]: r for g in doc["groups"] for r in g["rules"] if "alert" in r}
        for rule in alert_rules():
            if rule["uid"] in COVERAGE_CRITICAL or rule["_prom"]["ds"] != rules.PROM:
                continue
            name = rules.prom_alertname(rule["title"])
            with self.subTest(uid=rule["uid"]):
                self.assertNotIn("absent(", by_name[name]["expr"])

    def test_rendering_is_deterministic(self):
        self.assertEqual(rules.yamlify(rules.build_prom_rules()),
                         rules.yamlify(rules.build_prom_rules()))


if __name__ == "__main__":
    unittest.main()
