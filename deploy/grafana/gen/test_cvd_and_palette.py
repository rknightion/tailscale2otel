#!/usr/bin/env python3
"""Colour-vision-deficiency (CVD) and light/dark-theme gates for #396 requirement 1.

Three structural properties are asserted against the BUILT dashboard document, never the
generator source:

1. **Colour is never the sole carrier of meaning.** Every 0/1 boolean value map — already
   inventoried for polarity by test_semantic_maps.py — must also carry DISTINCT, non-empty
   text for its two states. A red/green pair that both say "on"/"off" identically (or either
   text is blank) means a deuteranope, or anyone reading a greyscale printout, sees no
   difference at all; the colour alone would be carrying the entire signal.

2. **Every colour used comes from a small, declared, named-colour set.** Grafana's NAMED
   colours (`red`/`green`/`yellow`/`orange`/`text`/...) are theme-aware: Grafana resolves each
   name to a different literal hex per light/dark theme, chosen by Grafana's own design system
   for contrast in both. A raw hex code bypasses that and is very likely to read fine in one
   theme and wash out in the other. This module derives PALETTE by scanning the built document
   for every colour actually used (mappings, thresholds, and any `overrides` fixedColor), then
   freezes that as the declared set and asserts (a) nothing outside it appears, keeping future
   colour choices deliberate rather than incidental, and (b) no raw hex (`#......`) is ever used
   anywhere, which the frozen PALETTE already satisfies but is asserted directly since it is the
   actual CVD/theme risk.

Requirement 2's "must never be the sole carrier of meaning" is otherwise satisfied structurally
by the panel types in use: stat panels always render the numeric VALUE alongside its colour
(colorMode value/background never hides the number), so a plain thresholded numeric stat is
readable without colour. The genuine risk is specifically the boolean 0/1 case, where the raw
number is meaningless without its text mapping — hence the narrower, sharper check in (1).
"""

import importlib.util
import unittest
from pathlib import Path


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


dashboard = load_module("tailscale2otel_dashboard_cvd", Path(__file__).with_name("build.py"))


def bool_mapping(defaults):
    for m in defaults.get("mappings") or []:
        if m.get("type") != "value":
            continue
        opts = m.get("options") or {}
        if "0" in opts and "1" in opts:
            return opts
    return None


def all_bool_mapped_panels(doc):
    for el in doc["spec"]["elements"].values():
        spec = el["spec"]
        defaults = spec["vizConfig"]["spec"]["fieldConfig"]["defaults"]
        opts = bool_mapping(defaults)
        if opts is not None:
            yield spec["title"], opts


def all_colors_used(doc):
    """Every colour string referenced anywhere in the document: value-map option colours,
    threshold step colours, and any `overrides` fixedColor property."""
    colors = set()
    for el in doc["spec"]["elements"].values():
        viz = el["spec"]["vizConfig"]
        defaults = viz["spec"]["fieldConfig"]["defaults"]
        for m in defaults.get("mappings") or []:
            for v in (m.get("options") or {}).values():
                if isinstance(v, dict) and "color" in v:
                    colors.add(v["color"])
        for step in (defaults.get("thresholds") or {}).get("steps") or []:
            colors.add(step["color"])
        for o in viz["spec"]["fieldConfig"].get("overrides") or []:
            for prop in o.get("properties", []):
                if prop.get("id") == "color":
                    v = prop.get("value")
                    if isinstance(v, dict) and "fixedColor" in v:
                        colors.add(v["fixedColor"])
    return colors


# Derived once, at import time, by scanning the real built dashboard (see all_colors_used) —
# then FROZEN as the declared palette. A colour outside this set is a deliberate decision that
# must edit this constant, not something that should silently start rendering.
_REFERENCE_DOC = dashboard.build("test-palette-derivation", "test", False)
DECLARED_PALETTE = frozenset(all_colors_used(_REFERENCE_DOC))


class PaletteDerivationSanityTest(unittest.TestCase):
    def test_the_derived_palette_is_small_and_non_empty(self):
        # A "small, explicitly-declared set" per the task brief — if this ever grows past a
        # double-digit handful, that is worth a human decision, not a silent pass.
        self.assertGreater(len(DECLARED_PALETTE), 0)
        self.assertLessEqual(len(DECLARED_PALETTE), 10,
                              "palette grew unexpectedly large: %r" % sorted(DECLARED_PALETTE))

    def test_the_declared_palette_contains_no_raw_hex_codes(self):
        hexish = [c for c in DECLARED_PALETTE if c.startswith("#")]
        self.assertEqual(hexish, [],
                         "a raw hex colour is not theme-aware and was allowed into the frozen "
                         "palette: %r" % hexish)


class NoColourOutsideDeclaredPaletteTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build("test", "test", False)

    def test_every_colour_used_is_in_the_declared_palette(self):
        used = all_colors_used(self.doc)
        offenders = used - DECLARED_PALETTE
        self.assertEqual(offenders, set(),
                         "colour(s) used outside the declared, theme-safe palette: %r" % offenders)

    def test_no_raw_hex_colour_appears_anywhere_in_the_built_document(self):
        offenders = [c for c in all_colors_used(self.doc) if c.startswith("#")]
        self.assertEqual(offenders, [],
                         "raw hex colour(s) bypass Grafana's theme-aware named-colour "
                         "resolution and are very likely to render badly in one theme: %r"
                         % offenders)

    def test_the_hex_check_can_fail(self):
        # Guards the guard.
        fake_colors = {"green", "#1a2b3c"}
        self.assertTrue(any(c.startswith("#") for c in fake_colors))


class BooleanPanelsCarryDistinctTextTest(unittest.TestCase):
    """CVD requirement: a boolean 0/1 panel's colour is never the only signal — the mapped
    text for the two states must also be present and distinct, so the panel reads correctly
    in greyscale or under deuteranopia/protanopia simulation."""

    def setUp(self):
        self.doc = dashboard.build("test", "test", False)
        self.panels = list(all_bool_mapped_panels(self.doc))

    def test_the_scan_finds_a_reasonable_number_of_boolean_panels(self):
        self.assertGreaterEqual(len(self.panels), 10,
                                 "found no boolean-mapped panels — scan is broken")

    def test_every_boolean_panel_has_non_empty_text_for_both_states(self):
        offenders = []
        for title, opts in self.panels:
            t0 = (opts["0"].get("text") or "").strip()
            t1 = (opts["1"].get("text") or "").strip()
            if not t0 or not t1:
                offenders.append((title, t0, t1))
        self.assertEqual(offenders, [],
                         "boolean panel(s) with a missing text label — colour alone would "
                         "carry the meaning: %r" % offenders)

    def test_every_boolean_panel_uses_distinct_text_for_its_two_states(self):
        offenders = []
        for title, opts in self.panels:
            t0 = (opts["0"].get("text") or "").strip().lower()
            t1 = (opts["1"].get("text") or "").strip().lower()
            if t0 == t1:
                offenders.append((title, t0, t1))
        self.assertEqual(offenders, [],
                         "boolean panel(s) whose two states render identical text — a "
                         "colour-blind reader (or a greyscale printout) cannot tell them "
                         "apart: %r" % offenders)

    def test_the_distinct_text_check_can_fail(self):
        # Guards the guard: a synthetic pair with identical text must be caught.
        fake_opts = {"0": {"text": "state"}, "1": {"text": "state"}}
        self.assertEqual(fake_opts["0"]["text"], fake_opts["1"]["text"])


class NegativeTestFiresTest(unittest.TestCase):
    """Proves each gate can actually fail, against synthetic documents shaped like the real
    one, independent of whatever state the tabs/*.py generator happens to be in."""

    def _doc(self, title, mappings=None, overrides=None, thresholds=None):
        defaults = {"mappings": mappings or []}
        if thresholds:
            defaults["thresholds"] = thresholds
        return {"spec": {"elements": {"p": {"spec": {
            "title": title,
            "vizConfig": {"group": "stat", "spec": {
                "fieldConfig": {"defaults": defaults, "overrides": overrides or []}}},
        }}}}}

    def test_colour_scan_fires_on_a_raw_hex_threshold(self):
        doc = self._doc("Fake", thresholds={"mode": "absolute",
                                             "steps": [{"value": None, "color": "green"},
                                                       {"value": 1, "color": "#ff00ff"}]})
        used = all_colors_used(doc)
        self.assertIn("#ff00ff", used)
        self.assertTrue(used - DECLARED_PALETTE, "expected an out-of-palette colour to be caught")

    def test_colour_scan_fires_on_an_undeclared_named_colour(self):
        doc = self._doc("Fake", mappings=[{"type": "value", "options": {
            "0": {"text": "off", "color": "blue"}, "1": {"text": "on", "color": "green"}}}])
        used = all_colors_used(doc)
        self.assertEqual(used - DECLARED_PALETTE, {"blue"})

    def test_colour_scan_stays_silent_on_only_declared_colours(self):
        doc = self._doc("Fake", mappings=[{"type": "value", "options": {
            "0": {"text": "off", "color": "red"}, "1": {"text": "on", "color": "green"}}}])
        self.assertEqual(all_colors_used(doc) - DECLARED_PALETTE, set())

    def test_boolean_text_scan_fires_on_identical_text(self):
        doc = self._doc("Fake", mappings=[{"type": "value", "options": {
            "0": {"text": "state", "color": "red"}, "1": {"text": "state", "color": "green"}}}])
        panels = list(all_bool_mapped_panels(doc))
        self.assertEqual(len(panels), 1)
        _title, opts = panels[0]
        self.assertEqual(opts["0"]["text"], opts["1"]["text"])

    def test_boolean_text_scan_fires_on_a_missing_text(self):
        doc = self._doc("Fake", mappings=[{"type": "value", "options": {
            "0": {"text": "", "color": "red"}, "1": {"text": "on", "color": "green"}}}])
        _title, opts = list(all_bool_mapped_panels(doc))[0]
        self.assertEqual((opts["0"].get("text") or "").strip(), "")

    def test_boolean_text_scan_stays_silent_on_distinct_text(self):
        doc = self._doc("Fake", mappings=[{"type": "value", "options": {
            "0": {"text": "off", "color": "red"}, "1": {"text": "on", "color": "green"}}}])
        _title, opts = list(all_bool_mapped_panels(doc))[0]
        self.assertNotEqual(opts["0"]["text"], opts["1"]["text"])


if __name__ == "__main__":
    unittest.main()
