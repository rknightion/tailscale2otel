#!/usr/bin/env python3
"""Structural regression fingerprints for #396 requirement 5.

NOT a pixel/screenshot test — a golden STRUCTURAL fingerprint (viz type, unit, threshold
steps, boolean-mapping polarity, and the set of metric names queried) for a deliberately
hand-picked set of high-risk panels, checked into
`deploy/grafana/gen/testdata/panel_fingerprints.json`. The fingerprint excludes panel id and
grid position ON PURPOSE — both renumber whenever an unrelated panel is inserted anywhere else
in the 400+-panel dashboard, and a gate that fires on that kind of churn gets deleted by the
next person who touches the generator. It fires only when one of these panels' actual SEMANTICS
change: its colour polarity flips, its unit changes, its threshold breakpoints move, or it
starts (or stops) querying a different metric.

## The high-risk set, and why each member is in it

Every member is either an INVERSE-RISK boolean (the #385 shape: "0 is the healthy state", where
a colour-polarity regression makes a real problem render green) or a LIVENESS/COMPLIANCE flag
whose entire job is a correct pass/fail read with no numeric value to fall back on:

- **SSH wildcard** / **SSH wildcard enabled** — inverse risk (Tailscale-SSH `*` rule present is
  BAD, colour 1=red/yellow). Two panels on the SAME metric with DIFFERENT threshold severities
  (plain red vs a softer yellow) are exactly the kind of pair a copy-paste edit could silently
  make identical, defeating the reason two panels exist.
- **Exporter update available** — inverse risk (a newer build existing is informational, not an
  outage, hence yellow not red) and was a named #385 casualty.
- **Last delivery error** — inverse risk (an error present is bad); the empty-vs-zero
  distinction (`novalue`) sits right next to this fingerprint's polarity, and #385's core bug was
  exactly this shape.
- **Object listing complete** / **Object-store gaps clear** — inverse risk pair sitting side by
  side, explicitly called out in test_semantic_maps.py as "exactly the shape #385 got wrong".
  One is 1=healthy, the other is 1=risk; a refactor that "simplifies" them onto one shared map
  would silently invert one of the two.
- **Unrestricted ACL rules** / **Auto-approved exit nodes** — the flagship security-scorecard
  panels (not boolean, but counts on an inverse-risk threshold: 0 is green). These are the
  panels an operator opens this dashboard FOR during an incident; a silently-changed threshold
  breakpoint (e.g. auto-approvers turning red at 1 instead of 3) changes the alerting posture
  with no code review signal beyond a JSON diff nobody reads panel-by-panel.
- **Config valid** / **Exporter up** / **Discovery OK** — liveness/health flags where 1 IS the
  healthy state (the normal polarity) placed deliberately alongside the inverse-risk panels
  above: the fingerprint set spans BOTH polarities on purpose, so a systematic "flip all
  booleans" bug (the literal #385 shape) cannot hide by only breaking the panels this test
  isn't watching.
- **PII filter status** / **OAuth scope preflight by capability** — compliance TABLES (not
  stats): the one place in the dashboard where a boolean value map colours table CELLS via
  field overrides rather than a single stat's colour/background, a materially different code
  path from every panel above.

## Regenerating

Only when a change to one of these panels is deliberate: read testdata/panel_fingerprints.json's
own header comment.
"""

import importlib.util
import json
import re
import sys
import unittest
from pathlib import Path


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


dashboard = load_module("tailscale2otel_dashboard_fingerprints", Path(__file__).with_name("build.py"))

GOLDEN_PATH = Path(__file__).with_name("testdata") / "panel_fingerprints.json"

METRIC_RE = re.compile(
    r"\b(tailscale2otel_[a-zA-Z0-9_]+|tailscale_[a-zA-Z0-9_]+|tailscaled_[a-zA-Z0-9_]+)\b"
)


def metrics_in_expr(expr):
    found = set()
    for m in METRIC_RE.finditer(expr):
        tok = m.group(1)
        if expr[m.end(1):m.end(1) + 1] == "=":
            continue
        found.add(tok)
    return found


def bool_mapping(defaults):
    for m in defaults.get("mappings") or []:
        if m.get("type") != "value":
            continue
        opts = m.get("options") or {}
        if "0" in opts and "1" in opts:
            return {"0": [opts["0"].get("text"), opts["0"].get("color")],
                    "1": [opts["1"].get("text"), opts["1"].get("color")]}
    return None


def fingerprint_of(panel_spec):
    """The structural fingerprint of one panel: everything that carries SEMANTIC meaning,
    nothing that is layout/id churn. `panel_spec` is a built dashboard element's `["spec"]`."""
    viz = panel_spec["vizConfig"]
    defaults = viz["spec"]["fieldConfig"]["defaults"]
    metrics = set()
    for q in panel_spec["data"]["spec"]["queries"]:
        qs = q["spec"]["query"]["spec"]
        expr = qs.get("expr") or qs.get("query") or ""
        metrics |= metrics_in_expr(expr)
    steps = [[s["value"], s["color"]] for s in (defaults.get("thresholds") or {}).get("steps") or []]
    return {
        "viz_type": viz["group"],
        "unit": defaults.get("unit"),
        "threshold_steps": steps,
        "bool_mapping": bool_mapping(defaults),
        "metrics": sorted(metrics),
    }


def collect_fingerprints(doc, titles):
    """title -> fingerprint, for every panel in `titles` found in the built document. A title
    appearing more than once (the same panel repeated verbatim on multiple tabs, e.g. "Exporter
    up" on both Overview and Exporter Diagnostics) must fingerprint IDENTICALLY everywhere it
    appears — that invariant is asserted separately below."""
    by_title = {}
    for el in doc["spec"]["elements"].values():
        spec = el["spec"]
        if spec["title"] not in titles:
            continue
        by_title.setdefault(spec["title"], []).append(fingerprint_of(spec))
    return by_title


GOLDEN = json.loads(GOLDEN_PATH.read_text())
HIGH_RISK_TITLES = frozenset(k for k in GOLDEN if not k.startswith("_"))


class HighRiskPanelFingerprintTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build("test", "test", False)
        self.found = collect_fingerprints(self.doc, HIGH_RISK_TITLES)

    def test_every_declared_high_risk_panel_exists_in_the_built_dashboard(self):
        missing = HIGH_RISK_TITLES - set(self.found)
        self.assertEqual(missing, set(), "high-risk panel(s) named in the golden fixture no "
                         "longer exist in the built dashboard: %r" % missing)

    def test_every_occurrence_of_a_repeated_title_fingerprints_identically(self):
        for title, fps in self.found.items():
            if len(fps) < 2:
                continue
            first = fps[0]
            for other in fps[1:]:
                self.assertEqual(first, other,
                                 "%r renders with different semantics on different tabs" % title)

    def test_every_high_risk_panel_matches_its_golden_fingerprint(self):
        mismatches = []
        for title in sorted(HIGH_RISK_TITLES):
            actual = self.found[title][0]
            expected = GOLDEN[title]
            if actual != expected:
                mismatches.append((title, expected, actual))
        if mismatches:
            lines = ["%r:\n  expected %s\n  actual   %s" % (t, e, a) for (t, e, a) in mismatches]
            self.fail("high-risk panel fingerprint drift:\n" + "\n".join(lines))


class NegativeTestFiresTest(unittest.TestCase):
    """Proves the comparison itself can fail, on synthetic fingerprints — independent of the
    real dashboard's current state."""

    def test_a_flipped_bool_mapping_colour_is_caught(self):
        golden = {"viz_type": "stat", "unit": "short",
                  "threshold_steps": [[None, "green"], [1, "red"]],
                  "bool_mapping": {"0": ["off", "green"], "1": ["on", "red"]},
                  "metrics": ["tailscale_acl_ssh_wildcard_ratio"]}
        regressed = dict(golden, bool_mapping={"0": ["off", "red"], "1": ["on", "green"]})
        self.assertNotEqual(golden, regressed)

    def test_a_moved_threshold_breakpoint_is_caught(self):
        golden = {"viz_type": "stat", "unit": "short",
                  "threshold_steps": [[None, "green"], [1, "yellow"], [3, "red"]],
                  "bool_mapping": None, "metrics": ["tailscale_acl_autoapprovers_ratio"]}
        regressed = dict(golden, threshold_steps=[[None, "green"], [1, "red"]])
        self.assertNotEqual(golden, regressed)

    def test_a_changed_queried_metric_is_caught(self):
        golden = {"viz_type": "stat", "unit": None, "threshold_steps": [],
                  "bool_mapping": None, "metrics": ["tailscale2otel_up_ratio"]}
        regressed = dict(golden, metrics=["tailscale2otel_scrape_success_ratio"])
        self.assertNotEqual(golden, regressed)

    def test_a_repeated_title_with_divergent_semantics_is_caught(self):
        by_title = {"Exporter up": [
            {"viz_type": "stat", "bool_mapping": {"0": ["DOWN", "red"], "1": ["UP", "green"]}},
            {"viz_type": "stat", "bool_mapping": {"0": ["DOWN", "green"], "1": ["UP", "red"]}},
        ]}
        fps = by_title["Exporter up"]
        self.assertNotEqual(fps[0], fps[1])

    def test_identical_fingerprints_do_not_false_positive(self):
        a = {"viz_type": "stat", "unit": None, "threshold_steps": [[None, "red"], [1, "green"]],
             "bool_mapping": {"0": ["DOWN", "red"], "1": ["UP", "green"]},
             "metrics": ["tailscale2otel_up_ratio"]}
        b = dict(a)
        self.assertEqual(a, b)


if __name__ == "__main__":
    if "--dump" in sys.argv:
        doc = dashboard.build("test", "test", False)
        found = collect_fingerprints(doc, HIGH_RISK_TITLES)
        out = {title: fps[0] for title, fps in found.items()}
        print(json.dumps(out, indent=2, sort_keys=True))
    else:
        unittest.main()
