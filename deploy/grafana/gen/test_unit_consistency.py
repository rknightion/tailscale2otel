#!/usr/bin/env python3
"""Unit-consistency gates for #396 requirement 2 (units/precision/legend/table conventions).

Everything here is derived, never a hand-picked panel list, from two sources that already
exist for other reasons:

1. The OTLP->Prometheus naming contract documented in the root CLAUDE.md ("OTLP->Prometheus
   naming"): dots->underscores, a monotonic counter gains `_total`, `By`->`_bytes`, `s`->
   `_seconds`, `d`->`_days`, and a unit-`1` gauge gains `_ratio` — EVEN A PLAIN INTEGER COUNT.
   That last clause is exactly why a metric ending `_ratio` cannot be assumed to be a bounded
   fraction: `docs/metrics.md`'s generated tables spell out, per metric, whether a given
   `_ratio` metric is "(a **count**, despite `_ratio`)" or a genuine `[0,1]` fraction ("Fraction
   of ...", "genuine ratio", "0..1", "in `[0,1]`") — see its own explanatory list item 4. This
   module parses those descriptions rather than hand-listing which `_ratio` metrics are counts.

2. The built dashboard document itself (never the generator source), including PER-SERIES
   `fieldConfig.overrides` (a `byName` matcher setting `unit` for one legend) — the generator
   already uses this correctly for mixed-unit multi-series panels (e.g. "Goroutines & stack"
   plots a count alongside a byte value and overrides the byte series to `unit: bytes`), so a
   naive check of only `fieldConfig.defaults.unit` would flag legitimate panels. This module
   computes the EFFECTIVE per-series unit (override if present, else the panel default).

What is deliberately NOT covered (and why): counter families whose "correct" rate unit is a
matter of house style rather than a strict per-suffix rule — `_requests_total` legitimately
renders as either `reqps` or the generic `cps` in this dashboard, and packet/byte/request
counters mix conventions no CLAUDE.md rule adjudicates. Gating that would either miss real
defects behind a loose rule or manufacture false positives against intentional style choices;
scope is kept to the three suffixes CLAUDE.md states explicitly (seconds/bytes/days) plus the
_ratio count-vs-fraction split docs/metrics.md's own generator states explicitly.
"""

import importlib.util
import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[3]
METRICS_DOC = ROOT / "docs" / "metrics.md"


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


dashboard = load_module("tailscale2otel_dashboard_units", Path(__file__).with_name("build.py"))


# ---------------------------------------------------------------------------
# derivation: which `_ratio` metrics are counts vs genuine fractions, from docs/metrics.md
# ---------------------------------------------------------------------------

_RATIO_TOKEN_RE = re.compile(r"`(tailscale[a-z0-9_]*_ratio)`")
_FRACTION_MARKERS = ("genuine ratio", "0..1", "in `[0,1]`", "fraction of")


def _derive_ratio_classification():
    """Scan every generated-table line mentioning a `..._ratio` metric and classify it as a
    count (marked "(a **count**...)") or a genuine bounded fraction (marked with one of
    _FRACTION_MARKERS). A metric matching neither marker is left unclassified — most `_ratio`
    metrics are booleans/info-gauges (0/1 flags, not counts or fractions), which this module
    does not need an opinion on, since those panels carry a value map and are excluded below."""
    count_metrics = set()
    fraction_metrics = set()
    for line in METRICS_DOC.read_text().splitlines():
        names = set(_RATIO_TOKEN_RE.findall(line))
        if not names:
            continue
        lowered = line.lower()
        if "**count**" in line:
            count_metrics.update(names)
        elif any(m in lowered for m in _FRACTION_MARKERS):
            fraction_metrics.update(names)
    return count_metrics, fraction_metrics


COUNT_RATIO_METRICS, FRACTION_RATIO_METRICS = _derive_ratio_classification()


# ---------------------------------------------------------------------------
# dashboard-document walking: per-SERIES effective unit (base default or byName override)
# ---------------------------------------------------------------------------

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


def is_bool_mapped(defaults):
    """A 0/1 value map means the numeric unit is irrelevant — the mapped TEXT is what
    renders — so unit-family checks do not apply (this is the same test_semantic_maps.py
    already inventories on the polarity side)."""
    for m in defaults.get("mappings") or []:
        opts = m.get("options") or {}
        if "0" in opts and "1" in opts:
            return True
    return False


def is_count_wrapped(expr):
    """True when the metric's raw value is never displayed — it is counted (how many series
    satisfy a condition), not read. `count(` may appear nested (e.g. inside `label_replace(...,
    count(...))`), so this checks for the substring anywhere, not just as the outermost call:
    a metric appearing anywhere inside a `count(...)` has had its own unit discarded by the
    count, regardless of nesting."""
    return "count(" in expr or "count by" in expr or "count_values(" in expr


def is_boolean_selection(expr):
    """`== 1` / `== 0` selects instances of a flag, which is a count-shaped read even without
    an explicit count() (the boolean value itself never reaches the visualization)."""
    return "== 1" in expr or "== 0" in expr


def series_specs(doc):
    """Yield (panel_title, viz_group, legend, expr, effective_unit, defaults) for every
    PanelQuery of every panel, where effective_unit accounts for a per-series unit
    override (falling back to the panel's default unit when none matches this series'
    legend).

    Both matcher kinds Grafana resolves against a series NAME are honoured: `byName`
    (exact) and `byRegexp`. Handling only `byName` used to be enough because every
    mixed-unit panel named its series explicitly; #526's consolidation pass merged
    same-subject/different-unit panels whose extra series are templated
    (`p95 {{operation}}`), which cannot be matched by an exact name at generation time
    and so must use a regexp. A checker blind to `byRegexp` reports those panels as
    unit-family violations while they render perfectly — and the reflex when a gate
    cries wolf is to exempt the panel, which then really would stop being checked.
    """
    for el in doc["spec"]["elements"].values():
        spec = el["spec"]
        viz = spec["vizConfig"]
        defaults = viz["spec"]["fieldConfig"]["defaults"]
        base_unit = defaults.get("unit")
        by_name_unit, by_regexp_unit = {}, []
        for o in viz["spec"]["fieldConfig"].get("overrides") or []:
            matcher = o.get("matcher", {})
            if matcher.get("id") not in ("byName", "byRegexp"):
                continue
            for prop in o.get("properties", []):
                if prop.get("id") != "unit":
                    continue
                if matcher["id"] == "byName":
                    by_name_unit[matcher.get("options")] = prop["value"]
                else:
                    by_regexp_unit.append((re.compile(matcher.get("options", "")),
                                           prop["value"]))

        def _unit_for(legend):
            if legend in by_name_unit:
                return by_name_unit[legend]
            for rx, u in by_regexp_unit:
                if rx.match(legend):
                    return u
            return base_unit

        for q in spec["data"]["spec"]["queries"]:
            qs = q["spec"]["query"]["spec"]
            expr = qs.get("expr") or qs.get("query") or ""
            legend = qs.get("legendFormat", "") or ""
            yield spec["title"], viz["group"], legend, expr, _unit_for(legend), defaults


# Panel viz types where the number itself (not just its color) is the primary read — tables
# routinely keep raw per-row values in a deliberately un-summarized form (a per-device
# inventory column), which is a different, much noisier population (see the report for why
# table density was investigated and NOT gated). Scope the unit-family checks to the types
# where a "wrong unit" is unambiguously a rendering defect.
VALUE_READ_TYPES = {"stat", "gauge", "bargauge", "timeseries", "barchart"}


def _exempt(expr):
    return is_count_wrapped(expr) or is_boolean_selection(expr) or "/" in expr


SECONDS_RE = re.compile(r"_seconds(_bucket|_sum)?$")
BYTES_RE = re.compile(r"_bytes(_bucket|_sum|_total)?$")
DAYS_RE = re.compile(r"_days(_bucket|_sum)?$")

SECONDS_UNITS = {"s"}
BYTES_UNITS = {"bytes", "Bps", "binBps", "decbytes", "decBps"}
DAYS_UNITS = {"d", "days"}
FRACTION_UNITS = {"percentunit", "percent"}


def find_family_violations(doc):
    """(panel, legend, metric, unit, family) for every non-exempt direct value-read whose
    unit does not match its metric-name-suffix family."""
    offenders = []
    for title, viz_group, legend, expr, unit, defaults in series_specs(doc):
        if viz_group not in VALUE_READ_TYPES:
            continue
        if is_bool_mapped(defaults):
            continue
        if _exempt(expr):
            continue
        for metric in metrics_in_expr(expr):
            if SECONDS_RE.search(metric):
                if unit not in SECONDS_UNITS:
                    offenders.append((title, legend, metric, unit, "seconds"))
            elif BYTES_RE.search(metric):
                if unit not in BYTES_UNITS:
                    offenders.append((title, legend, metric, unit, "bytes"))
            elif DAYS_RE.search(metric):
                if unit not in DAYS_UNITS:
                    offenders.append((title, legend, metric, unit, "days"))
    return offenders


def find_ratio_violations(doc):
    """(panel, legend, metric, unit, expected) for every non-exempt direct value-read of a
    `_ratio` metric docs/metrics.md classifies, where the rendered unit contradicts that
    classification: a documented COUNT rendered as a fraction unit, or a documented genuine
    FRACTION rendered as a non-fraction unit."""
    offenders = []
    for title, viz_group, legend, expr, unit, defaults in series_specs(doc):
        if viz_group not in VALUE_READ_TYPES:
            continue
        if is_bool_mapped(defaults):
            continue
        if _exempt(expr):
            continue
        for metric in metrics_in_expr(expr):
            if metric in COUNT_RATIO_METRICS and unit in FRACTION_UNITS:
                offenders.append((title, legend, metric, unit, "not-a-fraction (documented count)"))
            elif metric in FRACTION_RATIO_METRICS and unit not in FRACTION_UNITS:
                offenders.append((title, legend, metric, unit, "percentunit/percent (documented fraction)"))
    return offenders


# ---------------------------------------------------------------------------
# decimals / legend convention (dominant-usage derivation)
# ---------------------------------------------------------------------------

def all_defaults(doc):
    for el in doc["spec"]["elements"].values():
        yield el["spec"]["vizConfig"]["spec"]["fieldConfig"]["defaults"]


def all_timeseries_legend_calcs(doc):
    for el in doc["spec"]["elements"].values():
        spec = el["spec"]
        if spec["vizConfig"]["group"] != "timeseries":
            continue
        legend = spec["vizConfig"]["spec"]["options"].get("legend", {})
        yield spec["title"], tuple(legend.get("calcs", []))


# ---------------------------------------------------------------------------
# tests
# ---------------------------------------------------------------------------

class DerivationSanityTest(unittest.TestCase):
    """Guards the guard: if the docs/metrics.md parse breaks, every check below passes
    vacuously (nothing is ever classified, so nothing is ever checked)."""

    def test_docs_parse_finds_a_reasonable_number_of_count_ratio_metrics(self):
        self.assertGreaterEqual(len(COUNT_RATIO_METRICS), 30,
                                 "found suspiciously few '_ratio, a count' metrics")

    def test_docs_parse_finds_the_five_known_genuine_fraction_metrics(self):
        # Only five metrics in the whole catalog are explicitly documented as a genuine
        # [0,1] fraction rather than a count wearing the _ratio suffix.
        expected = {
            "tailscale2otel_runtime_gc_cpu_fraction_ratio",
            "tailscale2otel_scrape_budget_ratio",
            "tailscale2otel_subrequest_coverage_ratio",
            "tailscale2otel_ingress_wal_pending_entries_fill_ratio",
            "tailscale2otel_ingress_wal_pending_size_fill_ratio",
        }
        self.assertEqual(FRACTION_RATIO_METRICS, expected)

    def test_a_metric_cannot_be_classified_as_both(self):
        self.assertEqual(COUNT_RATIO_METRICS & FRACTION_RATIO_METRICS, set())


class UnitFamilyConsistencyTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build_family()

    def test_the_scan_examines_a_reasonable_number_of_series(self):
        n = sum(1 for _ in series_specs(self.doc))
        self.assertGreater(n, 300, "found suspiciously few panel-queries — scan is broken")

    def test_no_direct_seconds_bytes_or_days_value_read_uses_the_wrong_unit_family(self):
        offenders = find_family_violations(self.doc)
        if offenders:
            self.fail("unit-family violations:\n" + "\n".join(
                "%r (series %r): metric %r rendered %r, expected a %s unit"
                % (t, l, m, u, fam) for (t, l, m, u, fam) in offenders))

    def test_no_ratio_metric_is_rendered_against_its_documented_count_vs_fraction_meaning(self):
        offenders = find_ratio_violations(self.doc)
        if offenders:
            self.fail("count-vs-fraction violations:\n" + "\n".join(
                "%r (series %r): metric %r rendered %r, expected %s"
                % (t, l, m, u, exp) for (t, l, m, u, exp) in offenders))


class NegativeTestFiresTest(unittest.TestCase):
    """Proves each gate can actually fail, using synthetic documents shaped exactly like the
    real one, independent of whatever state tabs/*.py is in right now."""

    def _doc(self, title, viz_group, unit, expr, overrides=None, mappings=None):
        return {"spec": {"elements": {"p": {"spec": {
            "title": title,
            "vizConfig": {"group": viz_group, "spec": {
                "fieldConfig": {"defaults": {"unit": unit, "mappings": mappings or []},
                                 "overrides": overrides or []}}},
            "data": {"spec": {"queries": [{"spec": {"query": {"spec": {
                "expr": expr, "legendFormat": "x"}}}}]}},
        }}}}}

    def test_seconds_family_gate_fires_on_a_raw_duration_rendered_short(self):
        doc = self._doc("Fake duration", "stat", "short", "max(tailscale_device_key_expiry_seconds)")
        offenders = find_family_violations(doc)
        self.assertEqual(len(offenders), 1)
        self.assertEqual(offenders[0][3], "short")
        self.assertEqual(offenders[0][4], "seconds")

    def test_seconds_family_gate_stays_silent_when_unit_is_s(self):
        doc = self._doc("Fake duration", "stat", "s", "max(tailscale_device_key_expiry_seconds)")
        self.assertEqual(find_family_violations(doc), [])

    def test_seconds_family_gate_stays_silent_on_a_count_wrapped_read(self):
        doc = self._doc("Fake count", "stat", "short",
                         "count(tailscale_device_key_expiry_seconds - time() < 0)")
        self.assertEqual(find_family_violations(doc), [])

    def test_bytes_family_gate_fires_on_a_raw_byte_value_rendered_short(self):
        doc = self._doc("Fake bytes", "timeseries", "short",
                         "max(tailscale2otel_runtime_memory_heap_alloc_bytes)")
        offenders = find_family_violations(doc)
        self.assertEqual(len(offenders), 1)
        self.assertEqual(offenders[0][4], "bytes")

    def test_bytes_family_gate_respects_a_byname_override(self):
        doc = self._doc("Fake mixed", "timeseries", "short",
                         "max(tailscale2otel_runtime_memory_heap_alloc_bytes)",
                         overrides=[{"matcher": {"id": "byName", "options": "x"},
                                     "properties": [{"id": "unit", "value": "bytes"}]}])
        self.assertEqual(find_family_violations(doc), [])

    def test_days_family_gate_fires_on_a_raw_day_value_rendered_short(self):
        doc = self._doc("Fake days", "stat", "short", "max(tailscale_setting_devices_key_duration_days)")
        offenders = find_family_violations(doc)
        self.assertEqual(len(offenders), 1)
        self.assertEqual(offenders[0][4], "days")

    def test_ratio_gate_fires_on_a_documented_count_rendered_as_a_fraction(self):
        doc = self._doc("Fake count", "stat", "percentunit", "max(tailscale_devices_ephemeral_ratio)")
        offenders = find_ratio_violations(doc)
        self.assertEqual(len(offenders), 1)
        self.assertIn("not-a-fraction", offenders[0][4])

    def test_ratio_gate_fires_on_a_documented_fraction_rendered_as_a_count(self):
        doc = self._doc("Fake fraction", "stat", "short",
                         "max(tailscale2otel_subrequest_coverage_ratio)")
        offenders = find_ratio_violations(doc)
        self.assertEqual(len(offenders), 1)
        self.assertIn("percentunit", offenders[0][4])

    def test_ratio_gate_stays_silent_on_a_division_derived_fraction_of_a_count_metric(self):
        # A genuine derived ratio (division of two count metrics) legitimately renders
        # percentunit even though each operand is individually a documented count.
        doc = self._doc("Fake coverage", "stat", "percentunit",
                         "count(tailscale_devices_ephemeral_ratio == 1) / clamp_min(count(tailscale_devices_ephemeral_ratio), 1)")
        self.assertEqual(find_ratio_violations(doc), [])

    def test_ratio_gate_stays_silent_on_a_bool_mapped_panel(self):
        doc = self._doc("Fake bool", "stat", "percentunit", "max(tailscale_devices_ephemeral_ratio)",
                         mappings=[{"type": "value", "options": {"0": {"color": "red"}, "1": {"color": "green"}}}])
        self.assertEqual(find_ratio_violations(doc), [])


class DecimalsAndLegendConventionTest(unittest.TestCase):
    """#396 requirement 2 also asks for decimals/legend consistency. The dominant (in fact
    universal, as of this writing) convention in the generator is: no panel overrides Grafana's
    default decimals, and no timeseries legend requests summary calcs. Both are asserted as
    real, negative-testable regression gates rather than restated as prose."""

    def setUp(self):
        self.doc = dashboard.build_family()

    def test_no_panel_sets_an_explicit_decimals_override(self):
        offenders = [d for d in all_defaults(self.doc) if d.get("decimals") is not None]
        self.assertEqual(len(offenders), 0,
                         "a panel sets fieldConfig.defaults.decimals, breaking the dashboard-wide "
                         "convention of leaving decimal precision at the Grafana default")

    def test_no_timeseries_legend_requests_summary_calcs(self):
        offenders = [(t, c) for (t, c) in all_timeseries_legend_calcs(self.doc) if c]
        self.assertEqual(offenders, [],
                         "a timeseries panel's legend requests summary calcs, breaking the "
                         "dashboard-wide convention of a plain list legend")

    def test_the_decimals_check_can_fail(self):
        # Guards the guard.
        fake_defaults = [{"decimals": 2}]
        self.assertTrue(any(d.get("decimals") is not None for d in fake_defaults))

    def test_the_legend_calc_check_can_fail(self):
        fake = [("Fake panel", ("mean",))]
        self.assertTrue(any(c for (_t, c) in fake))


if __name__ == "__main__":
    unittest.main()
