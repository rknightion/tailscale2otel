#!/usr/bin/env python3
"""Regression tests for the fleet/security signal coverage added by #392 and #401.

Everything here asserts against the BUILT dashboard document, never the generator
source, so renaming a helper or reshuffling a row cannot make a check pass
vacuously. Four defects are guarded:

1. A signal the catalog emits is charted nowhere (#392/#401 were filed because the
   audit found 20 such signals in the fleet/security area). The inventories below
   are exhaustive, not samples: a panel deleted later fails here.
2. The three device populations — authorized-internal, unauthorized-internal and
   external/shared-in — are summed together, so an operator cannot see which of
   them grew. They must be split, and the split must partition the fleet exactly
   once (no series counted twice, none dropped).
3. An inverse-risk flag rendered with "1" as the good colour (#385). Panel-level
   0/1 value maps are inventoried by test_semantic_maps.py; the flags added here
   are table COLUMNS, whose maps live in field overrides and are checked below.
4. A signal the control plane never reports (Headscale omits Tailscale-SSH and
   key-expiry state entirely) zero-filled into a confident "0 problems".
"""

import importlib.util
from pathlib import Path
import re
import unittest


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


dashboard = load_module("tailscale2otel_dashboard_fleet", Path(__file__).with_name("build.py"))
builder = dashboard.builder

# #526 wave 3 split both leaves into sub-tabs — "Fleet & Devices" (59 panels) into
# three and "Security & Audit" (49) into four — so these scans walk the LEAVES, not
# the two parents. A parent is now a TabsLayout with no rows of its own, and a scan
# that kept pointing at it would find nothing while still passing every "did we look
# at enough?" floor below it.
DEVICE_TABS = ("Inventory & Hygiene", "Posture & Security", "Connectivity & Routing")
SECURITY_TABS = ("Audit Trail", "Risk & ACL", "Posture & Compliance", "Identity & Keys")
SCANNED_TABS = DEVICE_TABS + SECURITY_TABS

# Every signal #392/#401 assigned, mapped to the panel title that must query it.
# An inventory, not a sample — removing a panel breaks the build here.
NEWLY_COVERED = {
    # --- #392: fleet population, distribution and age ------------------------
    "tailscale_devices_age_seconds": "Device age (p50 / p90)",
    "tailscale_devices_by_distro_ratio": "Devices by distribution",
    "tailscale_devices_key_expiry_disabled_ratio": "Key expiry disabled (fleet)",
    "tailscale_devices_ssh_enabled_ratio": "Tailscale SSH enabled (fleet)",
    "tailscale_subnet_routes_enabled": "Enabled subnet routes",
    # --- #392/#401: credentials and identity ---------------------------------
    "tailscale_keys_age_seconds": "Key age (p50 / p90)",
    "tailscale_keys_by_owner_ratio": "Keys by owner and type",
    "tailscale_key_allowed_tags_ratio": "OAuth client tag restrictions",
    "tailscale_users_age_seconds": "User account age (p50 / p90)",
    "tailscale_device_invites_pending_age_seconds": "Pending device-invite age (p50 / p90)",
    "tailscale_user_invites_pending_age_seconds": "Pending user-invite age (p50 / p90)",
    # --- #401: per-device connectivity, aggregated ---------------------------
    "tailscale_device_connectivity_direct_capable_ratio": "Direct-capable coverage (per-device)",
    "tailscale_device_connectivity_endpoints_ratio": "Endpoint candidates per device",
    "tailscale_device_connectivity_ipv6_ratio": "IPv6 support coverage",
    "tailscale_device_connectivity_udp_ratio": "UDP blocked (DERP-forced)",
    # --- #401: per-device security flags, aggregated -------------------------
    "tailscale_device_blocks_incoming_connections_ratio": "Blocking incoming connections",
    "tailscale_device_ssh_enabled_ratio": "Tailscale SSH enabled (devices)",
    "tailscale_device_key_expiry_disabled_ratio": "Key expiry disabled (devices)",
    "tailscale_device_multiple_connections_ratio": "Multiple simultaneous connections",
    "tailscale_device_posture_identity_disabled_ratio": "Posture identity disabled",
}

# The three device populations, as the label matchers that select each one. They
# must partition {authorized, external}^2 — every combination selected by exactly
# one, so no device is summed twice and none is invisible.
POPULATION_PANELS = {
    "Authorized (internal)": {"tailscale_authorized": "true", "tailscale_external": "false"},
    "Unauthorized (internal)": {"tailscale_authorized": "false", "tailscale_external": "false"},
    "External (shared-in)": {"tailscale_external": "true"},
}

# Counts of a bad thing: the base threshold must be the healthy colour and the
# step at 1 must not be. A "0 devices with multiple connections" panel rendering
# red is the #385 defect with the operands swapped.
INVERSE_RISK_STATS = [
    "Unauthorized (internal)",
    "Key expiry disabled (fleet)",
    "Key expiry disabled (devices)",
    "Multiple simultaneous connections",
    "Posture identity disabled",
    "UDP blocked (DERP-forced)",
]

# Coverage ratios where more is better: the top threshold step must be green.
POSITIVE_COVERAGE_STATS = [
    "IPv6 support coverage",
    "Direct-capable coverage (per-device)",
]

# Per-device boolean columns in the drill-down tables, with the polarity each
# one's value map must carry. Table columns are mapped through field overrides,
# so test_semantic_maps.py's panel-level inventory never sees them.
FLAG_COLUMN_POLARITY = {
    # protective / capability: 1 is the healthy state
    "Blocks incoming": "green",
    "IPv6": "green",
    "UDP": "green",
    "Direct capable": "green",
    # inverse risk: 1 is the problem
    "Key expiry disabled": "red",
    "Multiple connections": "red",
    "Posture identity disabled": "red",
    # neither good nor bad — a deliberate configuration either way
    "SSH enabled": "text",
}

# Signals the control plane may not report AT ALL (Headscale has no Tailscale-SSH
# and no key-expiry concept), as opposed to reporting zero of them. No query may
# zero-fill these, and every panel reading one must say what it needs instead.
UNSUPPORTED_WHEN_ABSENT = [
    "tailscale_devices_ssh_enabled_ratio",
    "tailscale_devices_key_expiry_disabled_ratio",
    "tailscale_device_ssh_enabled_ratio",
    "tailscale_device_key_expiry_disabled_ratio",
    "tailscale_device_posture_identity_disabled_ratio",
]

# Config keys an empty state is allowed to name. An empty state names the config
# key and the prerequisite and stops there: presence cannot tell "disabled" from
# "unsupported" from "never deployed", so any text asserting one is a guess.
CONFIG_KEY_HINTS = [
    "cardinality.per_entity.device",
    "cardinality.per_entity.key",
    "cardinality.per_entity.user",
    "collect_connectivity",
    "collect_device_invites",
    "collectors.devices",
    "collectors.keys",
    "collectors.users",
    "control plane",
]


def panels(doc):
    """Yield (title, ptype, defaults, overrides, [query expressions]) per panel."""
    for element in doc["spec"]["elements"].values():
        spec = element["spec"]
        viz = spec["vizConfig"]
        exprs = []
        for q in spec["data"]["spec"]["queries"]:
            qs = q["spec"]["query"]["spec"]
            exprs.append(qs.get("expr") or qs.get("query") or "")
        fc = viz["spec"]["fieldConfig"]
        yield spec["title"], viz["group"], fc["defaults"], fc.get("overrides") or [], exprs


def panel_by_title(doc, title):
    """The one panel with this title, or a failure if it is missing/ambiguous."""
    found = [p for p in panels(doc) if p[0] == title]
    if len(found) != 1:
        raise AssertionError("expected exactly one panel titled %r, found %d" % (title, len(found)))
    return found[0]


def element_titles(doc):
    return {name: el["spec"]["title"] for name, el in doc["spec"]["elements"].items()}


def _find_tab(layout, tab_title):
    """Depth-first search for a TabsLayoutTab titled `tab_title`, at any nesting depth
    (#495 nested sub-tab navigation put leaf tabs like "Fleet & Devices" inside a
    domain wrapper's own TabsLayout rather than always at the document's top level)."""
    if layout["kind"] != "TabsLayout":
        return None
    for t in layout["spec"]["tabs"]:
        if t["spec"]["title"] == tab_title:
            return t
        found = _find_tab(t["spec"]["layout"], tab_title)
        if found is not None:
            return found
    return None


def rows_of_tab(doc, tab_title):
    """Yield (row_title, row_spec, [panel titles]) for one tab of the built doc."""
    titles = element_titles(doc)
    t = _find_tab(doc["spec"]["layout"], tab_title)
    if t is None:
        return
    for r in t["spec"]["layout"]["spec"]["rows"]:
        names = [i["spec"]["element"]["name"]
                 for i in r["spec"]["layout"]["spec"]["items"]]
        yield r["spec"]["title"], r["spec"], [titles[n] for n in names]


def transformation_excludes(defaults_panel_spec):
    """The organize transformation's excludeByName keys for one panel element."""
    out = set()
    for tr in defaults_panel_spec["data"]["spec"].get("transformations") or []:
        if tr.get("group") == "organize":
            out |= set(tr["spec"]["options"].get("excludeByName") or {})
    return out


def raw_panel(doc, title):
    for element in doc["spec"]["elements"].values():
        if element["spec"]["title"] == title:
            return element["spec"]
    raise AssertionError("no panel titled %r" % title)


def matchers_for(expr, metric):
    """All {label: value} equality matcher sets applied to `metric` in `expr`."""
    out = []
    for sel in re.findall(re.escape(metric) + r"\{([^}]*)\}", expr):
        pairs = dict(re.findall(r'(\w+)\s*=\s*"([^"]*)"', sel))
        out.append(pairs)
    return out


class NewSignalCoverageTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build_family()
        self.all_panels = list(panels(self.doc))
        # guards the guard: a shape change that makes the scan yield nothing must
        # fail loudly rather than pass every emptiness-tolerant assertion below.
        self.assertGreater(len(self.all_panels), 100,
                           "panel scan found almost nothing — the extraction is broken")

    def test_every_assigned_signal_is_queried_by_its_named_panel(self):
        for metric, title in sorted(NEWLY_COVERED.items()):
            title_, _ptype, _d, _o, exprs = panel_by_title(self.doc, title)
            self.assertTrue(any(metric in e for e in exprs),
                            "panel %r no longer queries %s" % (title_, metric))

    def test_no_assigned_signal_is_charted_only_by_a_variable(self):
        # A presence sentinel's label_values() query mentions the metric too, so
        # coverage proven only by a sentinel would be a false positive.
        exprs = [e for (_t, _p, _d, _o, es) in self.all_panels for e in es]
        for metric in sorted(NEWLY_COVERED):
            self.assertTrue(any(metric in e for e in exprs),
                            "%s appears in no PANEL query" % metric)


class AuthorizationSplitTest(unittest.TestCase):
    METRIC = "tailscale_devices_count_ratio"

    def setUp(self):
        self.doc = dashboard.build_family()

    def _selector(self, title):
        _t, _p, _d, _o, exprs = panel_by_title(self.doc, title)
        sets = [m for e in exprs for m in matchers_for(e, self.METRIC)]
        self.assertTrue(sets, "%r does not select %s at all" % (title, self.METRIC))
        return sets[0]

    def test_the_three_populations_exist_with_their_expected_matchers(self):
        for title, want in POPULATION_PANELS.items():
            got = self._selector(title)
            for label, value in want.items():
                self.assertEqual(got.get(label), value,
                                 "%s: expected %s=%r, got %r" % (title, label, value, got.get(label)))

    def test_the_populations_partition_the_fleet_exactly_once(self):
        selectors = {t: self._selector(t) for t in POPULATION_PANELS}
        for authorized in ("true", "false"):
            for external in ("true", "false"):
                series = {"tailscale_authorized": authorized, "tailscale_external": external}
                hits = [t for (t, sel) in selectors.items()
                        if all(series.get(k) == v for (k, v) in sel.items()
                               if k in ("tailscale_authorized", "tailscale_external"))]
                self.assertEqual(len(hits), 1,
                                 "a device with authorized=%s external=%s is counted by %d "
                                 "population panels (%s); the split must be a partition — "
                                 "two hits double-counts it, zero hits hides it"
                                 % (authorized, external, len(hits), hits))

    def test_os_breakdowns_keep_the_authorization_dimension(self):
        # #392: an OS total that sums authorization state away hides the thing an
        # operator needs — an unauthorized macOS device looks like any other.
        for title in ("Devices by OS", "Devices by OS over time"):
            _t, _p, _d, _o, exprs = panel_by_title(self.doc, title)
            joined = " ".join(exprs)
            for label in ("tailscale_authorized", "tailscale_external"):
                self.assertIn(label, joined,
                              "%r collapses %s out of its grouping" % (title, label))
            self.assertNotRegex(
                joined, r"sum by \(os_type\)",
                "%r sums the state labels away again" % title)


class InverseRiskPolarityTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build_family()

    def test_counts_of_a_bad_thing_are_green_at_zero(self):
        for title in INVERSE_RISK_STATS:
            _t, _p, defaults, _o, _e = panel_by_title(self.doc, title)
            steps = (defaults.get("thresholds") or {}).get("steps") or []
            self.assertGreaterEqual(len(steps), 2, "%s: no threshold steps" % title)
            self.assertEqual(steps[0]["color"], "green",
                             "%s counts a problem, so zero of them must be green" % title)
            self.assertNotEqual(steps[-1]["color"], "green",
                                "%s never leaves green, so a rising count reads as healthy" % title)

    def test_capability_coverage_reaches_green(self):
        for title in POSITIVE_COVERAGE_STATS:
            _t, _p, defaults, _o, _e = panel_by_title(self.doc, title)
            steps = (defaults.get("thresholds") or {}).get("steps") or []
            self.assertGreaterEqual(len(steps), 2, "%s: no threshold steps" % title)
            self.assertEqual(steps[-1]["color"], "green",
                             "%s measures a good thing, so its top step must be green" % title)

    def test_flag_table_columns_map_1_to_the_right_colour(self):
        seen = {}
        for (_t, _p, _d, overrides, _e) in panels(self.doc):
            for ov in overrides:
                name = ov.get("matcher", {}).get("options")
                if name not in FLAG_COLUMN_POLARITY:
                    continue
                for prop in ov["properties"]:
                    if prop["id"] != "mappings":
                        continue
                    opts = prop["value"][0]["options"]
                    seen[name] = opts["1"]["color"]
        self.assertEqual(set(seen), set(FLAG_COLUMN_POLARITY),
                         "flag-column value-map inventory drifted")
        for name, want in FLAG_COLUMN_POLARITY.items():
            self.assertEqual(seen[name], want,
                             "column %r maps 1 to %r, expected %r — an inverse-risk flag "
                             "coloured green at 1 is the #385 defect" % (name, seen[name], want))

    def test_the_polarity_guard_itself_still_fires(self):
        # guards the guard: every assertion above trusts check_bool_polarity to
        # reject a contradictory panel. Prove it still rejects one.
        from maps import BOOL_HEALTHY_OFF
        with self.assertRaises(ValueError):
            builder.check_bool_polarity(
                "deliberately inverted", BOOL_HEALTHY_OFF,
                builder.thr([(None, "green"), (1, "green")]))


class UnsupportedVersusZeroTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build_family()

    def test_unsupported_signals_are_never_zero_filled(self):
        offenders = []
        for (title, _p, _d, _o, exprs) in panels(self.doc):
            for expr in exprs:
                if "vector(0)" not in expr:
                    continue
                for metric in UNSUPPORTED_WHEN_ABSENT:
                    if metric in expr:
                        offenders.append("%s: %s" % (title, metric))
        self.assertEqual(offenders, [],
                         "a control plane that never reports these (Headscale) would render "
                         "a green zero instead of an absence")

    def test_panels_reading_them_name_a_config_key_or_prerequisite(self):
        checked = 0
        for (title, _p, defaults, _o, exprs) in panels(self.doc):
            if not any(m in e for m in UNSUPPORTED_WHEN_ABSENT for e in exprs):
                continue
            novalue = defaults.get("noValue") or ""
            self.assertGreater(len(novalue), 20,
                               "%s: no empty state, so absence reads as a value" % title)
            self.assertTrue(any(k in novalue for k in CONFIG_KEY_HINTS),
                            "%s: empty state %r names no config key or prerequisite" % (title, novalue))
            checked += 1
        self.assertGreaterEqual(checked, 5, "scan found no panels on these signals")


class RowGatingTest(unittest.TestCase):
    """Per-device rows added here must respect the existing gating contract."""

    # row title -> (presence sentinel or None, required hide_when sentinels)
    NEW_ROWS = {
        ("Inventory & Hygiene", "Authorization & sharing"): (None, set()),
        # Gated as of the #495 wiring pass: every panel in this row reads a
        # per-device flag gauge that cardinality.per_entity.device switches off
        # wholesale, so ungated it rendered seven empty-state panels — which reads
        # as "nothing to report" rather than "not collected".
        ("Posture & Security", "Device security flags"): ("has_device_flags", set()),
        ("Connectivity & Routing", "Connectivity detail (per device)"):
            ("has_connectivity", {"pii_perdevice"}),
        ("Identity & Keys", "Key inventory & age"): (None, set()),
        # Renamed from "OAuth client tag scope" in wave 3: the row now also carries
        # the API privilege class and the tag-authority class, which are different
        # fields from the tag COUNT it originally charted. Gates unchanged.
        ("Identity & Keys", "Credential scope & blast radius"):
            ("has_key_scopes", {"pii_actor"}),
        ("Identity & Keys", "Identity & invite hygiene"): (None, set()),
    }

    def setUp(self):
        self.doc = dashboard.build_family()
        self.rows = {}
        for tab in SCANNED_TABS:
            for (title, spec, ptitles) in rows_of_tab(self.doc, tab):
                self.rows[(tab, title)] = (spec, ptitles)
        self.assertGreater(len(self.rows), 15, "row scan found almost nothing")

    def _conditions(self, spec):
        present, hidden = None, set()
        group = spec.get("conditionalRendering")
        for item in ((group or {}).get("spec", {}).get("items") or []):
            if item["spec"]["operator"] == "notMatches":
                hidden.add(item["spec"]["variable"])
            else:
                present = item["spec"]["variable"]
        return present, hidden

    def test_new_rows_exist_and_carry_their_gates(self):
        for key, (want_present, want_hidden) in self.NEW_ROWS.items():
            self.assertIn(key, self.rows, "row %s is missing" % (key,))
            spec, _ptitles = self.rows[key]
            present, hidden = self._conditions(spec)
            self.assertEqual(present, want_present, "row %s presence gate" % (key,))
            self.assertEqual(hidden, want_hidden, "row %s PII gate" % (key,))

    def test_every_row_exposing_a_hostname_is_pii_gated(self):
        # Widening PII exposure is the failure this catches: a new per-device table
        # dropped into an ungated row leaks host names whenever redaction is on.
        for tab in SCANNED_TABS:
            for (title, spec, ptitles) in rows_of_tab(self.doc, tab):
                _present, hidden = self._conditions(spec)
                exposes = False
                for pt in ptitles:
                    ps = raw_panel(self.doc, pt)
                    for tr in ps["data"]["spec"].get("transformations") or []:
                        if tr.get("group") != "organize":
                            continue
                        if "host_name" in (tr["spec"]["options"].get("renameByName") or {}):
                            exposes = True
                if exposes:
                    self.assertTrue(hidden & {"pii_perdevice", "pii_host"},
                                    "row %r/%r renders a host-name column but is not PII-gated"
                                    % (tab, title))


class OperatorTableHygieneTest(unittest.TestCase):
    INFRA = {"__name__", "job", "instance", "service_instance_id",
             "service_name", "service_namespace"}

    def setUp(self):
        self.doc = dashboard.build_family()

    def test_operator_tables_drop_scrape_transport_columns(self):
        # #392: job/instance/service_* are scrape plumbing. They are never the
        # answer to an operator's question and they push the real columns off-screen.
        checked = 0
        for tab in SCANNED_TABS:
            for (_rtitle, _spec, ptitles) in rows_of_tab(self.doc, tab):
                for pt in ptitles:
                    ps = raw_panel(self.doc, pt)
                    if ps["vizConfig"]["group"] != "table":
                        continue
                    missing = self.INFRA - transformation_excludes(ps)
                    self.assertEqual(missing, set(),
                                     "table %r still renders infrastructure columns %s"
                                     % (pt, sorted(missing)))
                    checked += 1
        self.assertGreaterEqual(checked, 10, "found almost no tables — the scan is broken")


class SelectorAndDrilldownTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build_family()

    def test_fleet_device_queries_honour_the_tailnet_and_provider_selectors(self):
        # Both are real per-series labels (not resource attributes), so the fleet
        # panels have to carry the matcher; without it a multi-tailnet deployment
        # sums every tailnet together regardless of the dropdown.
        _t, _p, _d, _o, exprs = panel_by_title(self.doc, "Online")
        joined = " ".join(exprs)
        self.assertIn('tailscale_tailnet=~"$tailnet"', joined)
        self.assertIn('tailscale2otel_provider=~"$provider"', joined)

    def test_host_columns_offer_a_drilldown_link(self):
        linked = []
        for (title, _p, _d, overrides, _e) in panels(self.doc):
            for ov in overrides:
                for prop in ov["properties"]:
                    if prop["id"] == "links" and prop["value"]:
                        linked.append((title, prop["value"][0]["url"]))
        self.assertTrue(linked, "no panel offers a drill-down link")
        for (title, url) in linked:
            # A "d/<uid>" path would pin the link to one generated uid; a bare
            # query string resolves against whichever dashboard is open.
            self.assertTrue(url.startswith("?"),
                            "%s: drill-down %r hardcodes a dashboard path" % (title, url))
            self.assertIn("var-host_name=", url)


if __name__ == "__main__":
    unittest.main()
