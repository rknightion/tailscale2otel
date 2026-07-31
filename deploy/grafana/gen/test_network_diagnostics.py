#!/usr/bin/env python3
"""Regression tests for the Network & Flows / Node Metrics diagnostics work (#391, #402).

Everything here asserts against the BUILT dashboard document, never the generator
source text, so a refactor of the tab modules cannot make a check pass vacuously.

Four defects are guarded:

1. **Raw and rollup flow metrics summed together.** The rollup family is the bounded
   top-N *fold of the same traffic* the raw family reports, so adding them
   double-counts. The only legitimate way to name both in one panel is the
   rollup-first fallback `<rollup expr> or <raw expr>`, where PromQL `or` returns the
   left operand whole and evaluates the right only when the left has no series. This
   is asserted structurally (the two halves must be identical apart from the metric
   name) and across targets (one panel may not have a raw target and a rollup target
   it would stack).
2. **Top-talker panels rendering with no device label and a time-like category.** A
   barchart over an instant query picks the first string field as its category axis,
   so an un-excluded `Time`/`instance` column becomes the category. Each top-talker
   panel must pin `xField` to its device dimension and must exclude the noise columns.
3. **ACL drops presented as a fault.** An ACL drop is the packet filter doing its job.
   A panel that colours it red teaches operators to ignore the panel, so the
   policy-drop panel must carry no error colouring and the error-drop panel must
   exclude `acl` and carry one.
4. **Silent removal.** An inventory assertion so deleting a panel these issues added
   fails here rather than quietly shrinking coverage.
"""

import importlib.util
import json
from pathlib import Path
import unittest


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


dashboard = load_module("tailscale2otel_dashboard_netdiag", Path(__file__).with_name("build.py"))


# raw flow metric -> its bounded rollup counterpart. Neither name is a substring of
# the other, so plain `in` containment is an exact enough test here.
RAW_TO_ROLLUP = {
    "tailscale_network_io_bytes_total": "tailscale_network_io_rollup_bytes_total",
    "tailscale_network_packets_total": "tailscale_network_packets_rollup_total",
}

# Prometheus (normalized) names for the signals #391/#402 brought onto a panel. Taken
# from docs/metrics.md — internal/catalog/dashboardrefs_test.go fails the Go build on
# a name the in-code catalog does not have, so these are not guesses.
NEWLY_COVERED = {
    "tailscale_network_data_quality_total",
    "tailscale_network_dedup_conflicts_total",
    "tailscale_network_store_dropped_total",
    "tailscale_node_derp_home_region_ratio",
    "tailscale_node_packets_total",
    "tailscale_node_peer_relay_packets_total",
}

# Panels added or reshaped by #391/#402. An inventory, not a sample: removing one
# must fail here. Titles are also alert-link anchors (deploy/alerts/gen/build_rules.py
# resolves `panel=` by title and rejects an ambiguous one), so they stay unique.
EXPECTED_PANELS = {
    # #391 — Network & Flows
    "Rejected flow records/s",
    "Dedup counter conflicts/s",
    "Flow-view store drops/s",
    "Throughput by path — ROLLUP",
    "Throughput by DERP region ID — ROLLUP",
    "DERP-relayed share of rollup bytes",
    # #402 — Node Metrics
    "Packets/s by path (curated)",
    "Home DERP region (curated)",
    "Policy drops (ACL)",
    "Error & malformed drops by reason",
    "Drop share by reason",
    "Peer-relay packets/s by transport",
    "Peer-relay endpoint share by state",
}


def panels(doc):
    """Yield (title, ptype, defaults, options, transformations, [exprs], desc) per panel."""
    for element in doc["spec"]["elements"].values():
        spec = element["spec"]
        viz = spec["vizConfig"]
        exprs = []
        for q in spec["data"]["spec"]["queries"]:
            qs = q["spec"]["query"]["spec"]
            exprs.append(qs.get("expr") or qs.get("query") or "")
        yield (spec["title"], viz["group"],
               viz["spec"]["fieldConfig"]["defaults"], viz["spec"]["options"],
               spec["data"]["spec"]["transformations"], exprs, spec["description"])


# --- the raw/rollup detector, factored out so the guards-the-guard test can drive it


def expr_mixes_raw_and_rollup(expr):
    """True when one expression names a raw metric AND its rollup counterpart in any
    shape other than the exact `<rollup> or <raw>` fallback.

    The fallback is recognised structurally: split on ` or `, require exactly two
    halves, require the left to name only the rollup and the right only the raw, and
    require the two to be character-identical once the metric name is blanked out.
    That admits nothing a `+`, a `sum(a or b)` or an unrelated second clause could
    sneak through.
    """
    for raw, rollup in RAW_TO_ROLLUP.items():
        if raw not in expr or rollup not in expr:
            continue
        halves = expr.split(" or ")
        if len(halves) != 2:
            return True
        lhs, rhs = halves
        if rollup not in lhs or raw in lhs:
            return True
        if raw not in rhs or rollup in rhs:
            return True
        if lhs.replace(rollup, "\x00") != rhs.replace(raw, "\x00"):
            return True
    return False


def panel_mixes_raw_and_rollup_across_targets(exprs):
    """True when one panel has a raw-only target and a rollup-only target.

    Separate targets are not summed by PromQL, but a stacked timeseries adds them on
    screen, which is the same double count with a nicer picture.
    """
    for raw, rollup in RAW_TO_ROLLUP.items():
        has_raw = any(raw in e and rollup not in e for e in exprs)
        has_rollup = any(rollup in e and raw not in e for e in exprs)
        if has_raw and has_rollup:
            return True
    return False


class RawRollupSeparationTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build_family()

    def test_no_expression_sums_a_raw_and_a_rollup_flow_metric(self):
        offenders = [title for (title, _p, _d, _o, _t, exprs, _desc) in panels(self.doc)
                     for e in exprs if expr_mixes_raw_and_rollup(e)]
        self.assertEqual(offenders, [],
                         "panel(s) %s name a raw flow metric and its rollup counterpart in "
                         "one expression outside the `<rollup> or <raw>` fallback — the two "
                         "measure the same traffic, so combining them double-counts" % offenders)

    def test_no_panel_stacks_a_raw_target_against_a_rollup_target(self):
        offenders = [title for (title, _p, _d, _o, _t, exprs, _desc) in panels(self.doc)
                     if panel_mixes_raw_and_rollup_across_targets(exprs)]
        self.assertEqual(offenders, [],
                         "panel(s) %s carry a raw target and a rollup target; a stacked "
                         "render adds them" % offenders)

    def test_the_rollup_first_fallback_is_actually_used(self):
        # Guards the two assertions above against passing because nothing anywhere
        # mentions both families: the rollup-first stats must exist.
        fallbacks = [title for (title, _p, _d, _o, _t, exprs, _desc) in panels(self.doc)
                     for e in exprs
                     if any(raw in e and rollup in e for raw, rollup in RAW_TO_ROLLUP.items())]
        self.assertGreaterEqual(len(fallbacks), 2,
                                "no rollup-first fallback panels found — the separation "
                                "tests would pass vacuously")

    def test_detector_catches_a_fabricated_double_count(self):
        # Guards the guard. Every shape below really does double-count, or really is
        # the safe fallback; the detector must agree.
        raw, rollup = "tailscale_network_io_bytes_total", "tailscale_network_io_rollup_bytes_total"
        self.assertTrue(expr_mixes_raw_and_rollup(
            "sum(rate(%s[5m])) + sum(rate(%s[5m]))" % (rollup, raw)))
        self.assertTrue(expr_mixes_raw_and_rollup(
            "sum(rate(%s[5m]) or rate(%s[5m]))" % (rollup, raw)))
        self.assertTrue(expr_mixes_raw_and_rollup(
            "sum(rate(%s[5m])) or sum by (job) (rate(%s[5m]))" % (rollup, raw)))
        self.assertFalse(expr_mixes_raw_and_rollup(
            "sum(rate(%s[5m])) or sum(rate(%s[5m]))" % (rollup, raw)))
        self.assertFalse(expr_mixes_raw_and_rollup("sum(rate(%s[5m]))" % rollup))
        self.assertTrue(panel_mixes_raw_and_rollup_across_targets(
            ["sum(rate(%s[5m]))" % rollup, "sum(rate(%s[5m]))" % raw]))
        self.assertFalse(panel_mixes_raw_and_rollup_across_targets(
            ["sum(rate(%s[5m]))" % rollup, "sum(rate(%s[5m]))" % rollup]))


class TopTalkerCategoryTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build_family()

    def talkers(self):
        for (title, ptype, _d, options, transforms, exprs, _desc) in panels(self.doc):
            if ptype == "barchart" and title.startswith("Top "):
                yield title, options, transforms, exprs

    def test_scan_finds_the_top_talker_panels(self):
        self.assertGreaterEqual(len(list(self.talkers())), 6,
                                "found no top-talker barcharts — the scan is broken")

    def test_each_top_talker_pins_a_device_category_axis(self):
        for (title, options, transforms, _exprs) in self.talkers():
            xfield = options.get("xField")
            self.assertTrue(xfield,
                            "%s: no xField — the barchart falls back to the first string "
                            "column, which is how a time-like category shipped" % title)
            renames = {}
            excludes = set()
            for t in transforms:
                if t["spec"]["options"].get("renameByName"):
                    renames.update(t["spec"]["options"]["renameByName"])
                excludes |= set(t["spec"]["options"].get("excludeByName") or {})
            self.assertIn(xfield, renames.values(),
                          "%s: xField %r is not produced by any rename — the category "
                          "would resolve to a raw label name or nothing" % (title, xfield))
            self.assertIn("Time", excludes, "%s: Time column not excluded" % title)
            self.assertIn("instance", excludes, "%s: instance column not excluded" % title)
            self.assertIn("Value", renames,
                          "%s: the value column keeps its generic 'Value' name" % title)

    def test_top_talker_categories_are_device_dimensions(self):
        # The category must be the device/service dimension the query groups by, not a
        # time or infrastructure column.
        for (title, options, _t, exprs) in self.talkers():
            label = {"Source node": "tailscale_src_node",
                     "Destination node": "tailscale_dst_node",
                     "Destination service": "tailscale_dst_service"}.get(options["xField"])
            self.assertIsNotNone(label, "%s: unexpected category %r" % (title, options["xField"]))
            self.assertTrue(any("sum by (%s)" % label in e for e in exprs),
                            "%s: category %r does not match the query's group-by"
                            % (title, options["xField"]))


class AclDropsAreNotAFaultTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build_family()
        self.by_title = {t: (d, o, tr, e) for (t, _p, d, o, tr, e, _desc) in panels(self.doc)}

    def test_policy_drop_panel_isolates_acl_and_carries_no_error_colouring(self):
        defaults, _o, _t, exprs = self.by_title["Policy drops (ACL)"]
        self.assertTrue(any('tailscale_drop_reason="acl"' in e for e in exprs),
                        "the policy-drop panel does not restrict to the acl reason")
        self.assertNotIn("red", json.dumps(defaults),
                         "the ACL packet filter doing its job must not render as an error")

    def test_error_drop_panel_excludes_acl_and_does_carry_error_colouring(self):
        defaults, _o, _t, exprs = self.by_title["Error & malformed drops by reason"]
        self.assertTrue(any('tailscale_drop_reason!="acl"' in e for e in exprs),
                        "the error-drop panel does not exclude the acl reason")
        colours = [s["color"] for s in defaults["thresholds"]["steps"]]
        self.assertIn("red", colours,
                      "the error-drop panel carries no escalation threshold, so nothing "
                      "distinguishes it from the benign policy-drop panel")

    def test_no_acl_only_panel_anywhere_renders_red(self):
        for (title, _p, defaults, _o, _t, exprs, _desc) in panels(self.doc):
            acl_only = any('tailscale_drop_reason="acl"' in e for e in exprs)
            if acl_only and not any('tailscale_drop_reason!="acl"' in e for e in exprs):
                self.assertNotIn("red", json.dumps(defaults),
                                 "%s: an acl-only panel must not colour red" % title)


class SignalCoverageInventoryTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build_family()

    def test_every_newly_covered_signal_appears_in_a_panel_query(self):
        seen = set()
        for (_t, _p, _d, _o, _tr, exprs, _desc) in panels(self.doc):
            for e in exprs:
                seen |= {m for m in NEWLY_COVERED if m in e}
        self.assertEqual(NEWLY_COVERED - seen, set(),
                         "signal(s) %s are no longer queried by any panel"
                         % sorted(NEWLY_COVERED - seen))

    def test_expected_panels_still_exist(self):
        titles = {t for (t, _p, _d, _o, _tr, _e, _desc) in panels(self.doc)}
        self.assertEqual(EXPECTED_PANELS - titles, set(),
                         "panel(s) %s were removed" % sorted(EXPECTED_PANELS - titles))

    def test_panel_titles_stay_unique_where_alerts_link_them(self):
        # deploy/alerts/gen/build_rules.py resolves `panel=` by title and rejects an
        # ambiguous match, so a new panel must not collide with an existing title.
        titles = [t for (t, _p, _d, _o, _tr, _e, _desc) in panels(self.doc)]
        for title in EXPECTED_PANELS:
            self.assertEqual(titles.count(title), 1,
                             "%r appears %d times; an alert panel link cannot resolve it"
                             % (title, titles.count(title)))

    def test_raw_flow_rows_name_their_prerequisite_config_key(self):
        # Presence cannot distinguish disabled from unsupported from never-deployed, so
        # a raw-only panel states the config key it needs and nothing about a cause.
        raw_panels = [(t, desc) for (t, _p, _d, _o, _tr, exprs, desc) in panels(self.doc)
                      if any("tailscale_network_io_bytes_total" in e
                             and "tailscale_network_io_rollup_bytes_total" not in e
                             for e in exprs)]
        self.assertGreaterEqual(len(raw_panels), 3, "found no raw-only flow panels")
        for (title, desc) in raw_panels:
            self.assertIn("cardinality.flow.metrics_mode", desc,
                          "%s: does not name the config key that emits it" % title)


if __name__ == "__main__":
    unittest.main()
