#!/usr/bin/env python3
"""Regression tests for nested sub-tab navigation (#495 remaining scope).

Before this change the flagship carried 10 flat top-level tabs for 405 panels. This
module asserts three things about the domain-grouped replacement:

1. The nesting is REAL — a domain tab's layout is an actual `TabsLayout` containing
   real `TabsLayoutTab` children, not a flattened row list wearing a domain's name (the
   `--flat` preview mode already does exactly that kind of flattening for screenshots,
   so it would be an easy mistake to reuse it here by accident).
2. Nothing was lost or duplicated in the regrouping: every original leaf tab still
   exists, under its original title, with its panels intact, and the total panel count
   is unchanged.
3. Two-level conditional rendering is where it should be: a leaf tab whose entire
   content is feature-gated stays gated once nested (Node Metrics / Tailnets), and a
   domain that contains any always-present leaf is NOT gated at the domain level (that
   would hide core content behind an unrelated feature flag). Each gate has a negative
   test proving the check can actually fail.
"""

import importlib.util
import unittest
from pathlib import Path


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


HERE = Path(__file__).resolve().parent
dashboard = load_module("tailscale2otel_dashboard_nested", HERE / "build.py")
builder = dashboard.builder

# The pre-#495 flat leaf set, minus the two that became a dashboard of their own.
# Every one of these must still exist on the TAILNET dashboard post-regrouping,
# with no rename, drop, or duplicate.
#
# "Exporter Diagnostics" and "Cardinality & Cost" left this set in #526: they are
# exporter self-observability, not tailnet telemetry, and now make up the
# tailscale2otel-health dashboard. They are still asserted — see
# HEALTH_LEAF_TABS — so the split cannot quietly drop one.
ORIGINAL_LEAF_TABS = {
    "Overview", "Fleet & Devices", "Network & Flows", "Events & Logs",
    "Security & Audit", "Policy & Config", "Kubernetes Audit", "Node Metrics", "Tailnets",
}

HEALTH_LEAF_TABS = {"Exporter Diagnostics", "Cardinality & Cost"}

EXPECTED_TOP_LEVEL = ["Overview", "Fleet & Network", "Security & Policy",
                      "Events & Logs"]

# Leaves that carry conditional rendering ONLY because their entire content is
# feature-gated (present= on the tab() call in build.py's tab_defs) — the case #495
# calls out: "a tab containing only conditional rows still renders unless the tab
# itself is conditional."
EXPECTED_GATED_LEAVES = {
    "Node Metrics": ("Fleet & Network", "has_nodemetrics"),
    "Tailnets": ("Fleet & Network", "has_multitailnet"),
    "Kubernetes Audit": ("Security & Policy", "has_k8s_audit"),
}

# Domains that must stay UNGATED because every one carries at least one always-present
# leaf — gating the domain would hide that core content whenever the domain's optional
# sibling's feature happens to be absent.
UNGATED_DOMAINS = {"Fleet & Network", "Security & Policy"}


def _matching_gate(conditional_rendering):
    """The single sentinel name a ConditionalRenderingGroup gates on via `matches`, or
    None. Test fixtures here only ever build single-condition groups."""
    if not conditional_rendering:
        return None
    items = [i["spec"]["variable"] for i in conditional_rendering["spec"]["items"]
             if i["kind"] == "ConditionalRenderingVariable" and i["spec"]["operator"] == "matches"]
    return items[0] if len(items) == 1 else tuple(items)


def _leaf_panel_titles(tab_spec, elements):
    """Every panel TITLE reachable from one leaf TabsLayoutTab's RowsLayout, via its
    GridLayoutItem ElementReferences."""
    titles = []

    def walk(o):
        if isinstance(o, dict):
            if o.get("kind") == "ElementReference":
                titles.append(elements[o["name"]]["spec"]["title"])
            for v in o.values():
                walk(v)
        elif isinstance(o, list):
            for v in o:
                walk(v)

    walk(tab_spec["layout"])
    return titles


class NestedStructureIsRealTest(unittest.TestCase):
    """Requirement 1: the nesting is an actual second TabsLayout, not a flattened
    row list dressed up with a domain title."""

    def setUp(self):
        self.doc = dashboard.build(dashboard.dashboards.TAILNET)
        self.top_tabs = self.doc["spec"]["layout"]["spec"]["tabs"]

    def test_top_level_tab_titles_and_order(self):
        self.assertEqual([t["spec"]["title"] for t in self.top_tabs], EXPECTED_TOP_LEVEL)

    def test_grouped_domains_carry_a_real_nested_tabslayout(self):
        for title in UNGATED_DOMAINS:
            domain = next(t for t in self.top_tabs if t["spec"]["title"] == title)
            layout = domain["spec"]["layout"]
            self.assertEqual(layout["kind"], "TabsLayout",
                              "domain %r must nest a TabsLayout, not flatten its "
                              "children into rows" % title)
            children = layout["spec"]["tabs"]
            self.assertGreater(len(children), 1,
                                "domain %r has too few children to justify nesting "
                                "at all" % title)
            for child in children:
                self.assertEqual(child["kind"], "TabsLayoutTab")
                # Each child is itself a genuine leaf: a RowsLayout of real rows, not
                # another layer of nesting and not a bare panel list.
                self.assertEqual(child["spec"]["layout"]["kind"], "RowsLayout")

    def test_standalone_leaves_are_not_wrapped_in_a_pointless_single_item_domain(self):
        # Overview and Events & Logs each had no sibling to group with, so they must
        # render as plain leaf tabs (RowsLayout) directly at the top level — not
        # promoted into a one-child TabsLayout, which would add a dead sub-tab bar.
        for title in ("Overview", "Events & Logs"):
            leaf = next(t for t in self.top_tabs if t["spec"]["title"] == title)
            self.assertEqual(leaf["spec"]["layout"]["kind"], "RowsLayout",
                              "%r must stay a plain leaf tab, not a one-child domain" % title)

    def test_negative_a_flattened_fake_would_be_caught(self):
        """Proves test_grouped_domains_carry_a_real_nested_tabslayout is not vacuous:
        a domain that flattened its children's rows into one RowsLayout (exactly what
        --flat does for the whole dashboard) must fail the same assertion."""
        fake_domain = {"spec": {"title": "Fleet & Network",
                                 "layout": {"kind": "RowsLayout", "spec": {"rows": []}}}}
        with self.assertRaises(AssertionError):
            self.assertEqual(fake_domain["spec"]["layout"]["kind"], "TabsLayout")


class NoTabLostOrDuplicatedTest(unittest.TestCase):
    """Requirement 2: every original leaf survives, with its panels, and the total
    panel count is unchanged by the regrouping."""

    def setUp(self):
        self.doc = dashboard.build(dashboard.dashboards.TAILNET)
        self.elements = self.doc["spec"]["elements"]

    def _all_leaf_tabs(self):
        found = {}

        def walk(layout):
            if layout["kind"] != "TabsLayout":
                return
            for t in layout["spec"]["tabs"]:
                child_layout = t["spec"]["layout"]
                if child_layout["kind"] == "TabsLayout":
                    walk(child_layout)
                else:
                    title = t["spec"]["title"]
                    self.assertNotIn(title, found, "duplicate leaf tab title %r" % title)
                    found[title] = t

        walk(self.doc["spec"]["layout"])
        return found

    def test_every_original_leaf_tab_still_exists_exactly_once(self):
        leaves = self._all_leaf_tabs()
        self.assertEqual(set(leaves), ORIGINAL_LEAF_TABS)

    def test_every_leaf_still_carries_its_panels(self):
        # A leaf with zero panels post-regroup would mean its rows were dropped while
        # the tab title survived — worse than losing the tab outright, since nothing
        # would look wrong in a title-only diff.
        leaves = self._all_leaf_tabs()
        for title, tab_spec in leaves.items():
            panel_titles = _leaf_panel_titles(tab_spec["spec"], self.elements)
            self.assertGreater(len(panel_titles), 0, "leaf %r lost all its panels" % title)

    def test_total_panel_count_is_preserved_across_the_family(self):
        # 437 measured 2026-07-29 after adding the Kubernetes Audit tab (#462, 32 new
        # panels on top of the 405 measured 2026-07-27). Regrouping tabs must not add
        # or drop a single panel element beyond a deliberate content change.
        #
        # Counted over the FAMILY since #526: the split moved 103 panels to
        # tailscale2otel-health, so a per-dashboard count would have to be lowered to
        # 334 and would then stop noticing a panel that fell off the health side
        # entirely. The union is the invariant the split had to preserve.
        total = sum(len(dashboard.build(s)["spec"]["elements"])
                    for s in dashboard.dashboards.ALL)
        self.assertEqual(total, 437)
        self.assertEqual(len(self.elements), 334, "tailnet dashboard")

    def test_the_health_dashboard_carries_the_exporter_leaves(self):
        # The other half of the count above: #526 moved these two leaves rather than
        # deleting them, and a split that silently dropped one would still satisfy a
        # tailnet-only leaf assertion.
        doc = dashboard.build(dashboard.dashboards.HEALTH)
        titles = {t["spec"]["title"] for t in doc["spec"]["layout"]["spec"]["tabs"]}
        self.assertEqual(titles, HEALTH_LEAF_TABS)

    def test_flat_and_nested_builds_carry_the_same_panel_count(self):
        # --flat renders every original row (prefixed with its owning tab's title) with
        # no tabs at all; nested renders the same rows under a two-level tab tree. Both
        # walk the exact same tab_defs list before branching on `flat` in build.py, so
        # their panel counts diverging would mean the grouping step (organize_tabs)
        # itself dropped or duplicated a leaf.
        flat_doc = dashboard.build(dashboard.dashboards.TAILNET, True)
        self.assertEqual(len(flat_doc["spec"]["elements"]), len(self.elements))

    def test_negative_a_dropped_leaf_would_be_caught(self):
        """Proves test_every_original_leaf_tab_still_exists_exactly_once is not
        vacuous: dropping one leaf from the found set must fail the equality."""
        leaves = self._all_leaf_tabs()
        with_one_dropped = set(leaves) - {"Tailnets"}
        self.assertNotEqual(with_one_dropped, ORIGINAL_LEAF_TABS)


class TwoLevelGatingTest(unittest.TestCase):
    """Requirement 3: sub-tab-level conditional rendering is present exactly where the
    leaf's entire content is feature-gated, and domains with any always-present leaf
    are left ungated."""

    def setUp(self):
        self.doc = dashboard.build(dashboard.dashboards.TAILNET)
        self.top_tabs = self.doc["spec"]["layout"]["spec"]["tabs"]

    def _domain(self, title):
        return next(t for t in self.top_tabs if t["spec"]["title"] == title)

    def _leaf(self, domain_title, leaf_title):
        domain = self._domain(domain_title)
        return next(c for c in domain["spec"]["layout"]["spec"]["tabs"]
                     if c["spec"]["title"] == leaf_title)

    def test_feature_gated_leaves_keep_their_own_conditional_rendering_when_nested(self):
        for leaf_title, (domain_title, sentinel) in EXPECTED_GATED_LEAVES.items():
            leaf = self._leaf(domain_title, leaf_title)
            gate = _matching_gate(leaf["spec"].get("conditionalRendering"))
            self.assertEqual(gate, sentinel,
                              "%r must stay gated on %r once nested under its domain" %
                              (leaf_title, sentinel))

    def test_core_leaves_carry_no_conditional_rendering(self):
        # A core (always-present) leaf must not accidentally inherit gating either from
        # a copy-paste of a neighbouring optional leaf's present= or from the grouping
        # step itself.
        core_leaves = [("Fleet & Network", "Fleet & Devices"),
                       ("Fleet & Network", "Network & Flows"),
                       ("Security & Policy", "Security & Audit"),
                       ("Security & Policy", "Policy & Config")]
        for domain_title, leaf_title in core_leaves:
            leaf = self._leaf(domain_title, leaf_title)
            self.assertIsNone(leaf["spec"].get("conditionalRendering"),
                               "%r is core content and must not be gated" % leaf_title)

    def test_domains_with_any_core_leaf_are_not_gated_at_the_domain_level(self):
        # Every current domain carries at least one always-present leaf (Fleet &
        # Devices, Security & Audit), so gating the domain would
        # hide that core content whenever the domain's OPTIONAL sibling's sentinel
        # happens to be absent — worse than the ungrouped flat tab ever was.
        for title in UNGATED_DOMAINS:
            domain = self._domain(title)
            self.assertIsNone(domain["spec"].get("conditionalRendering"),
                               "domain %r contains always-present content and must "
                               "not be gated" % title)

    def test_negative_an_ungated_optional_leaf_would_be_caught(self):
        """Proves test_feature_gated_leaves_keep_their_own_conditional_rendering_when_nested
        is not vacuous: a Node Metrics tab built with no present= (i.e. what would
        render its rows unconditionally even with no node-metrics scraper configured)
        must fail the sentinel check."""
        unggated_leaf = builder.tab("Node Metrics", [])
        self.assertIsNone(_matching_gate(unggated_leaf["spec"].get("conditionalRendering")))
        self.assertNotEqual(
            _matching_gate(unggated_leaf["spec"].get("conditionalRendering")),
            "has_nodemetrics")

    def test_negative_a_wrongly_gated_core_domain_would_be_caught(self):
        """Proves test_domains_with_any_core_leaf_are_not_gated_at_the_domain_level is
        not vacuous: a domain built WITH a present= (the mistake the docstring on
        builder.tab_group warns against) must fail the assertIsNone check."""
        wrongly_gated = builder.tab_group("Fleet & Network", [], present="has_nodemetrics")
        self.assertIsNotNone(wrongly_gated["spec"].get("conditionalRendering"))


if __name__ == "__main__":
    unittest.main()
