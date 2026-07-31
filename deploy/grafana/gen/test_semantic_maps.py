#!/usr/bin/env python3
"""Regression tests for boolean value-map polarity and absent-state handling (#385).

Two defects are guarded here, both asserted against the BUILT dashboard document
rather than the generator source, so a refactor of the tab modules cannot make them
pass vacuously:

1. A single on/off value map used to colour every boolean flag 0=red/1=green, which
   is backwards for an inverse-risk flag (SSH wildcard, update available). Each
   boolean panel must now carry a map whose polarity matches its own thresholds.
2. `or vector(0)` on a signal that is only emitted under some configuration turns
   "we do not know" into a confident zero. Those sites must be gone, replaced by an
   empty-state string that names the prerequisite.
"""

import importlib.util
from pathlib import Path
import unittest


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


dashboard = load_module("tailscale2otel_dashboard", Path(__file__).with_name("build.py"))


# (title, panel type) -> {"0": (text, colour), "1": (text, colour)}
#
# This is an INVENTORY, not a sample: the test asserts the set of boolean-mapped
# panels in the built dashboard is exactly these keys, so a new inverse-risk flag
# added with the wrong map fails here instead of shipping.
HEALTHY_ON = {"0": ("off", "red"), "1": ("on", "green")}        # 1 is the healthy state
HEALTHY_OFF = {"0": ("off", "green"), "1": ("on", "red")}       # 0 is the healthy state
NEUTRAL = {"0": ("off", "text"), "1": ("on", "text")}           # neither value is good or bad
UP = {"0": ("DOWN", "red"), "1": ("UP", "green")}

BOOL_PANELS = {
    # --- 1 is healthy -------------------------------------------------------
    ("Flow logging", "stat"): HEALTHY_ON,
    ("MagicDNS", "stat"): HEALTHY_ON,
    ("Override local DNS", "stat"): HEALTHY_ON,
    ("Config valid", "stat"): HEALTHY_ON,
    # #393/#403 — audit-pipeline and inventory flags. All three are "1 is the
    # state you want": the poll scrape succeeded, the policy validated, the
    # desired webhook category has a subscriber.
    ("Audit poll collector", "stat"): HEALTHY_ON,
    ("ACL policy validated", "stat"): HEALTHY_ON,
    ("Desired-event coverage", "table"): HEALTHY_ON,
    # --- 0 is healthy (inverse risk) ---------------------------------------
    ("SSH wildcard", "stat"): HEALTHY_OFF,
    # --- neither is good or bad --------------------------------------------
    ("Tailnet settings", "table"): NEUTRAL,
    ("Tailnet features", "table"): NEUTRAL,
    # --- liveness -----------------------------------------------------------
    ("Exporter up", "stat"): UP,
    ("Discovery OK", "stat"): UP,
    # --- custom labels/colours ---------------------------------------------
    # inverse risk, but the panel's threshold rates it yellow rather than red
    ("SSH wildcard enabled", "stat"): {"0": ("off", "green"), "1": ("on", "yellow")},
    ("Exporter update available", "stat"): {"0": ("up to date", "green"),
                                            "1": ("update available", "yellow")},
    ("Last delivery error", "stat"): {"0": ("OK", "green"), "1": ("ERROR", "red")},
    ("PII filter status", "table"): {"0": ("REDACTED", "red"), "1": ("emitted", "green")},
    # object-store feed health: gap.healthy is 1-is-healthy, scan.truncated is inverse
    # risk, and they sit side by side — the pair is exactly the shape #385 got wrong.
    ("Object-store gaps clear", "stat"): {"0": ("GAPS", "red"), "1": ("CLEAN", "green")},
    # #526. Two panels that moved onto the health dashboard, with OPPOSITE polarity —
    # kept adjacent here on purpose, because a "simplify these onto one shared map"
    # refactor is exactly what #385 was.
    #   discovery OK      1 = healthy   (normal polarity: UP is good)
    #   delivery degraded 1 = DEGRADED  (inverse risk: 1 is the bad state)
    ("Node-metrics discovery OK", "stat"): {"0": ("DOWN", "red"), "1": ("UP", "green")},
    ("Annotation delivery degraded", "stat"): {"0": ("healthy", "green"),
                                               "1": ("degraded", "red")},
    ("Object listing complete", "stat"): {"0": ("complete", "green"),
                                          "1": ("truncated", "red")},
    ("OAuth scope preflight by capability", "table"): {"0": ("NOT SATISFIED", "red"),
                                                       "1": ("satisfied", "green")},
}

# Metrics whose absence means "we do not know", not "zero". No query anywhere in the
# dashboard may zero-fill these.
UNKNOWN_WHEN_ABSENT = [
    "tailscale_device_update_available_ratio",   # suppressed when the control plane omits it (Headscale)
    "tailscale2otel_update_available_ratio",     # only with version_checks.self, never on dev builds
    "tailscale_devices_outdated_ratio",          # only with version_checks.devices + a known upstream latest
    "tailscale_user_connected_ratio",            # per-user, gated by cardinality.per_entity.user
    "tailscale_user_last_seen_seconds",          # per-user, gated by cardinality.per_entity.user
    "tailscale_key_preauthorized_ratio",         # per-key, gated by cardinality.per_entity.key
    "tailscale2otel_series_limit",               # only when a positive metric_limit is configured
    "tailscale2otel_checkpoint_disk_size_bytes",  # only for a file-backed checkpoint store
    "tailscale2otel_checkpoint_persist_age_seconds",
    "tailscale_logstream_error_ratio",           # only for a configured stream
    "tailscale_posture_integration_last_sync_seconds",  # only once a sync has happened
    "tailscale_contact_needs_verification_ratio",  # only when the contacts collector runs
    "tailscale_webhook_endpoints_count_ratio",   # an under-scoped credential emits nothing
]

# (title, panel type, metric) for every panel whose zero-fill was removed: it must
# carry an empty-state string, and "0" is not an empty state.
EMPTY_STATE_PANELS = [
    ("Updates available", "stat", "tailscale_device_update_available_ratio"),
    ("Devices needing update", "stat", "tailscale_device_update_available_ratio"),
    ("Exporter update available", "stat", "tailscale2otel_update_available_ratio"),
    ("Outdated (≥N behind)", "stat", "tailscale_devices_outdated_ratio"),
    ("Distinct users", "stat", "tailscale_user_connected_ratio"),
    ("Stale users (>30d)", "stat", "tailscale_user_last_seen_seconds"),
    ("Preauthorized auth keys", "stat", "tailscale_key_preauthorized_ratio"),
    ("Series limit", "stat", "tailscale2otel_series_limit"),
    ("Series headroom", "stat", "tailscale2otel_series_limit"),
    ("Checkpoint disk", "stat", "tailscale2otel_checkpoint_disk_size_bytes"),
    ("Checkpoint persist age", "timeseries", "tailscale2otel_checkpoint_persist_age_seconds"),
    ("Last delivery error", "stat", "tailscale_logstream_error_ratio"),
    ("Oldest sync age", "stat", "tailscale_posture_integration_last_sync_seconds"),
    ("Contact needs verification", "stat", "tailscale_contact_needs_verification_ratio"),
    ("Webhook endpoints", "stat", "tailscale_webhook_endpoints_count_ratio"),
]


def panels(doc):
    """Yield (title, ptype, defaults, [query expressions]) for every panel."""
    for element in doc["spec"]["elements"].values():
        spec = element["spec"]
        viz = spec["vizConfig"]
        exprs = []
        for q in spec["data"]["spec"]["queries"]:
            qs = q["spec"]["query"]["spec"]
            exprs.append(qs.get("expr") or qs.get("query") or "")
        yield spec["title"], viz["group"], viz["spec"]["fieldConfig"]["defaults"], exprs


def bool_mapping(defaults):
    """The {value: {text, color}} options of a 0/1 value map, or None."""
    for m in defaults.get("mappings") or []:
        if m.get("type") != "value":
            continue
        opts = m.get("options") or {}
        if "0" in opts and "1" in opts:
            return opts
    return None


class BooleanPolarityTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build_family()

    def test_boolean_panels_are_exactly_the_expected_inventory(self):
        found = {(t, p) for (t, p, d, _e) in panels(self.doc) if bool_mapping(d)}
        # guards the guard: a shape change that stops the scan finding anything
        # must fail loudly rather than pass an empty comparison.
        self.assertGreaterEqual(len(found), 10, "found no boolean-mapped panels — scan is broken")
        self.assertEqual(found, set(BOOL_PANELS), "boolean-map inventory drifted")

    def test_each_boolean_panel_maps_0_and_1_the_right_way_round(self):
        # every expected panel must be reached, and a title duplicated across tabs
        # ("Exporter up") must map identically in both places.
        covered = set()
        for (title, ptype, defaults, _exprs) in panels(self.doc):
            opts = bool_mapping(defaults)
            if opts is None:
                continue
            want = BOOL_PANELS[(title, ptype)]
            for value, (text, color) in want.items():
                self.assertEqual(opts[value]["text"], text,
                                 "%s: value %s text" % (title, value))
                self.assertEqual(opts[value]["color"], color,
                                 "%s: value %s colour" % (title, value))
            covered.add((title, ptype))
        self.assertEqual(covered, set(BOOL_PANELS))

    def test_value_map_polarity_agrees_with_panel_thresholds(self):
        checked = 0
        for (title, _ptype, defaults, _exprs) in panels(self.doc):
            opts = bool_mapping(defaults)
            steps = (defaults.get("thresholds") or {}).get("steps") or []
            if opts is None or len(steps) != 2:
                continue
            if steps[0]["value"] is not None or steps[1]["value"] != 1:
                continue
            self.assertEqual(opts["0"]["color"], steps[0]["color"],
                             "%s: value-map 0 disagrees with its base threshold" % title)
            self.assertEqual(opts["1"]["color"], steps[1]["color"],
                             "%s: value-map 1 disagrees with its 1 threshold" % title)
            checked += 1
        self.assertGreaterEqual(checked, 8, "found no thresholded boolean panels — scan is broken")


class AbsentStateTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build_family()

    def test_unknown_when_absent_metrics_are_never_zero_filled(self):
        offenders = []
        for (title, _ptype, _defaults, exprs) in panels(self.doc):
            for expr in exprs:
                if "or vector(0)" not in expr:
                    continue
                for metric in UNKNOWN_WHEN_ABSENT:
                    if metric in expr:
                        offenders.append("%s: %s" % (title, metric))
        self.assertEqual(offenders, [], "zero-filled unknown-state signals")

    def test_panels_with_removed_zero_fill_explain_the_empty_state(self):
        for (title, ptype, metric) in EMPTY_STATE_PANELS:
            matches = [d for (t, p, d, exprs) in panels(self.doc)
                       if t == title and p == ptype and any(metric in e for e in exprs)]
            self.assertTrue(matches, "no panel %r (%s) queries %s" % (title, ptype, metric))
            for defaults in matches:
                novalue = defaults.get("noValue")
                self.assertTrue(novalue, "%s: no empty-state string" % title)
                self.assertNotEqual(novalue.strip(), "0",
                                    "%s: '0' is not an empty state" % title)
                self.assertGreater(len(novalue), 20, "%s: empty-state text says too little" % title)


if __name__ == "__main__":
    unittest.main()
