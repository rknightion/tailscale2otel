#!/usr/bin/env python3
"""Panel-description gates for #396 requirement 3 (caveats and missing-state behaviour).

The set under test is DERIVED from the built dashboard's own layout, never hand-picked:
**every panel that sits inside a conditionally-rendered row** (a row whose own `present=`/
`hide_when=` gate applies, OR whose enclosing TAB is itself conditionally rendered — e.g.
every row in the "Node Metrics" tab, gated at the tab level on `has_nodemetrics`). A panel that
can silently not exist in the rendered dashboard needs a description that at minimum says what
it shows, so an operator who DOES see it (the gate fired) understands why it is there and what
absence would otherwise have meant.

This is deliberately NOT "every panel must have a non-empty description" — that gate passes
vacuously on a single punctuation character, which is exactly the failure mode #396 warns
against. The bar here is a real length threshold (the same 20-character floor
test_empty_state_contract.py already applies to noValue prose), applied to a scoped,
structurally-derived population rather than the whole document.

Every panel flagged by this gate when it was first written (69 of them, across
fleet/network/events/security/policy/nodemetrics/diagnostics) has since had a real description
added — see the accompanying report for the full list. This module is the regression gate that
keeps that true.
"""

import importlib.util
import unittest
from pathlib import Path


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


dashboard = load_module("tailscale2otel_dashboard_desc", Path(__file__).with_name("build.py"))

MIN_DESC_LEN = 20


def _matching_gates(conditional_rendering):
    return [i["spec"]["variable"] for i in conditional_rendering["spec"]["items"]
            if i["kind"] == "ConditionalRenderingVariable" and i["spec"]["operator"] == "matches"]


def gated_row_panels(doc):
    """(row_title, panel_title, description) for every panel inside a row that is
    conditionally rendered, either directly (its own `present=`) or via its enclosing tab."""
    elements = doc["spec"]["elements"]
    out = []

    def panels_of(grid_layout):
        result = []
        if grid_layout["kind"] != "GridLayout":
            return result
        for item in grid_layout["spec"]["items"]:
            el = elements[item["spec"]["element"]["name"]]
            result.append(el["spec"])
        return result

    def walk(layout, gates):
        kind = layout["kind"]
        if kind == "TabsLayout":
            for t in layout["spec"]["tabs"]:
                tab_gates = gates
                cr = t["spec"].get("conditionalRendering")
                if cr:
                    tab_gates = gates + _matching_gates(cr)
                walk(t["spec"]["layout"], tab_gates)
        elif kind == "RowsLayout":
            for r in layout["spec"]["rows"]:
                row_gates = gates
                cr = r["spec"].get("conditionalRendering")
                if cr:
                    row_gates = gates + _matching_gates(cr)
                if row_gates:
                    for p in panels_of(r["spec"]["layout"]):
                        out.append((r["spec"].get("title"), p["title"], p.get("description") or ""))

    walk(doc["spec"]["layout"], [])
    return out


class GatedRowPanelDescriptionTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build_family()
        self.rows = gated_row_panels(self.doc)

    def test_the_scan_finds_a_reasonable_number_of_gated_panels(self):
        # Guards the guard: if the layout walk regresses to finding nothing, the checks
        # below pass vacuously on an empty population.
        self.assertGreaterEqual(len(self.rows), 150,
                                 "found suspiciously few panels in conditionally-rendered rows")

    def test_every_panel_in_a_conditionally_rendered_row_has_a_real_description(self):
        offenders = [(row, panel) for (row, panel, desc) in self.rows
                     if len(desc.strip()) < MIN_DESC_LEN]
        self.assertEqual(offenders, [],
                         "panel(s) in a conditionally-rendered row with no (or too short) a "
                         "description — an operator who sees the row rendered has no idea "
                         "what it shows or what its absence would have meant: %r" % offenders)


class NegativeTestFiresTest(unittest.TestCase):
    """Proves the gate actually catches both a missing and a too-short description, using a
    synthetic document shaped like the real one."""

    def _doc(self, tab_gate, row_gate, panel_descs):
        elements = {}
        items = []
        for i, desc in enumerate(panel_descs):
            name = "p%d" % i
            elements[name] = {"spec": {"title": "Panel %d" % i, "description": desc}}
            items.append({"kind": "GridLayoutItem",
                          "spec": {"element": {"kind": "ElementReference", "name": name}}})
        row = {"kind": "RowsLayoutRow", "spec": {
            "title": "Fake row", "layout": {"kind": "GridLayout", "spec": {"items": items}}}}
        if row_gate:
            row["spec"]["conditionalRendering"] = {"spec": {"items": [
                {"kind": "ConditionalRenderingVariable", "spec": {"variable": row_gate, "operator": "matches"}}]}}
        tab = {"spec": {"title": "Fake tab", "layout": {"kind": "RowsLayout", "spec": {"rows": [row]}}}}
        if tab_gate:
            tab["spec"]["conditionalRendering"] = {"spec": {"items": [
                {"kind": "ConditionalRenderingVariable", "spec": {"variable": tab_gate, "operator": "matches"}}]}}
        return {"spec": {"elements": elements,
                         "layout": {"kind": "TabsLayout", "spec": {"tabs": [tab]}}}}

    def test_gate_fires_on_a_missing_description_in_a_row_gated_panel(self):
        doc = self._doc(tab_gate=None, row_gate="has_x", panel_descs=[""])
        rows = gated_row_panels(doc)
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0][2], "")

    def test_gate_fires_on_a_too_short_description(self):
        doc = self._doc(tab_gate=None, row_gate="has_x", panel_descs=["too short"])
        rows = gated_row_panels(doc)
        self.assertLess(len(rows[0][2]), MIN_DESC_LEN)

    def test_gate_fires_on_a_panel_gated_only_via_its_enclosing_tab(self):
        doc = self._doc(tab_gate="has_nodemetrics", row_gate=None, panel_descs=[""])
        rows = gated_row_panels(doc)
        self.assertEqual(len(rows), 1, "a tab-level gate must still scope this panel in")

    def test_gate_stays_silent_on_a_real_description(self):
        real = "A description long enough to explain what this panel actually shows."
        doc = self._doc(tab_gate=None, row_gate="has_x", panel_descs=[real])
        rows = gated_row_panels(doc)
        self.assertGreaterEqual(len(rows[0][2].strip()), MIN_DESC_LEN)

    def test_gate_stays_silent_on_an_ungated_panel(self):
        doc = self._doc(tab_gate=None, row_gate=None, panel_descs=[""])
        self.assertEqual(gated_row_panels(doc), [])


if __name__ == "__main__":
    unittest.main()
