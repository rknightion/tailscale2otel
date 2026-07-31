#!/usr/bin/env python3
"""Generator-level gate for the #395 "prerequisite-aware empty states" contract.

The contract: a stat-like panel (`stat`/`gauge`/`bargauge`) inside a conditionally-rendered
row may zero-fill with `or vector(0)` ONLY IF the row's gating sentinel is fed by the SAME
COLLECTOR as the panel's own query. Otherwise the row renders because collector A is
present while the panel's metric comes from collector B, which may be absent — and the
panel shows a green "0" that reads as "checked, nothing found" when the truth is "never
checked". Metric NAME identity is not the test — two metrics from one collector always
co-exist — COLLECTOR identity is the test.

Collector ownership is DERIVED, never hand-listed, from two facts already checked in by
other code:

1. `internal/collector/<name>/*.go` (non-test files) declares each metric's dotted OTEL
   name as a Go string literal (`metricFoo = "tailscale.foo.bar"`), or, for the node-metrics
   scraper's raw verbatim-forwarded families, the already-Prometheus-shaped name itself
   (`"tailscaled_inbound_bytes_total"`) — see internal/collector/nodemetrics/curated.go.
   Scanning each collector subdirectory's source for these literals gives an owner map
   that a future collector/metric is automatically included in — nothing here is a
   per-metric or per-panel list.
2. `docs/metrics.md`'s generated tables (the `<!-- BEGIN/END GENERATED: metrics groups=... -->`
   blocks) carry both the dotted OTEL name and the normalized Prometheus name for every
   curated metric, so a Prometheus name seen in a dashboard query can be translated back to
   the dotted name before the source scan above is applied.

A metric found in exactly one collector directory is owned by that collector. A metric
found in zero or more-than-one directory (self-observability, the checkpoint/scheduler
internals, rdns, release-checks, ...) is bucketed as "shared" — by construction these are
cross-cutting signals that co-exist with every collector, exactly the "Service health" /
"App health" rows #395 explicitly calls out as NOT violations. A row gated ONLY by
shared-owned sentinels (including the non-metric `has_multitailnet` raw_sentinel, which
gates on ">1 distinct tailnet observed" rather than any single collector's presence) carries
no collector information to check against, so it is skipped rather than guessed at.

See WAVE_C_SPEC.md (this task's frozen contract) for the three known violations and the
five explicitly-NOT-violations this test must stay silent on.
"""

import importlib.util
import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[3]
COLLECTOR_ROOT = ROOT / "internal" / "collector"
METRICS_DOC = ROOT / "docs" / "metrics.md"


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


dashboard = load_module("tailscale2otel_dashboard_empty_state", Path(__file__).with_name("build.py"))


# ---------------------------------------------------------------------------
# collector-ownership derivation
# ---------------------------------------------------------------------------

_STRING_LITERAL_RE = re.compile(r'"([a-zA-Z0-9_.]+)"')
_HIST_SUFFIXES = ("_bucket", "_count", "_sum")


def _strip_go_comments(src):
    """Drop `//` and `/* */` comments before scanning for string literals.

    Go doc comments routinely quote a *different* metric's dotted name for cross-
    reference ("see also `tailscale.acl.validation.test_failures`"), which would
    otherwise be picked up as a literal declared in the wrong collector.
    """
    src = re.sub(r"/\*.*?\*/", "", src, flags=re.S)
    lines = []
    for line in src.splitlines():
        idx = line.find("//")
        lines.append(line[:idx] if idx != -1 else line)
    return "\n".join(lines)


def _collector_dirs():
    return sorted(p.name for p in COLLECTOR_ROOT.iterdir() if p.is_dir())


def _collector_literals():
    """dir name -> set of every `"tailscale..."`-shaped string literal declared in that
    collector's non-test .go files (dotted OTEL names, and the raw forwarded
    `tailscaled_*` names the node-metrics scraper curates from)."""
    literals = {}
    for d in _collector_dirs():
        lits = set()
        for f in (COLLECTOR_ROOT / d).glob("*.go"):
            if f.name.endswith("_test.go"):
                continue
            text = _strip_go_comments(f.read_text())
            for m in _STRING_LITERAL_RE.finditer(text):
                s = m.group(1)
                if s.startswith("tailscale"):
                    lits.add(s)
        literals[d] = lits
    return literals


def _prom_to_otel_map():
    """Prometheus (normalized) name -> dotted OTEL name, parsed from every generated
    `metrics groups=...` table in docs/metrics.md (the `logs` block is skipped: log
    events have no Prometheus name column at all)."""
    doctext = METRICS_DOC.read_text()
    mapping = {}
    for block in re.finditer(
        r'<!-- BEGIN GENERATED: metrics groups="[^"]+" -->\n(.*?)\n<!-- END GENERATED -->',
        doctext, re.S,
    ):
        for line in block.group(1).splitlines():
            if not line.startswith("| `"):
                continue
            cols = [c.strip() for c in line.strip().strip("|").split("|")]
            if len(cols) < 4:
                continue
            otel = cols[0].strip("`")
            prom = cols[3].strip("`")
            if otel and prom:
                mapping[prom] = otel
    return mapping


LITERALS = _collector_literals()
PROM_TO_OTEL = _prom_to_otel_map()

# Every token this module will ever recognise as "a metric name" when scanning a PromQL
# expression: every documented Prometheus name, plus the raw underscore-style literals
# found directly in collector source (currently only nodemetrics' verbatim-forwarded
# `tailscaled_*` families, which docs/metrics.md deliberately does not catalog).
ALL_METRIC_NAMES = set(PROM_TO_OTEL)
for _lits in LITERALS.values():
    for _s in _lits:
        if "." not in _s:
            ALL_METRIC_NAMES.add(_s)


def _resolve_otel(prom_name):
    """Prometheus name -> dotted OTEL name, tolerating the `_bucket`/`_count`/`_sum`
    suffixes Prometheus auto-appends to a histogram's base name (docs/metrics.md
    catalogs only the base, e.g. `tailscale_devices_key_expiry_days`, never the
    `_count` variant a sentinel or panel may query directly)."""
    if prom_name in PROM_TO_OTEL:
        return PROM_TO_OTEL[prom_name]
    for suf in _HIST_SUFFIXES:
        if prom_name.endswith(suf):
            base = prom_name[: -len(suf)]
            if base in PROM_TO_OTEL:
                return PROM_TO_OTEL[base]
    return None


def collector_owner(prom_name):
    """The single internal/collector/<dir> that owns `prom_name`, or "shared" when it
    is emitted by zero or more-than-one collector directory (self-observability,
    checkpoint/scheduler internals, rdns, release checks, ...) — signals that by
    construction co-exist with every collector and so never gate a cross-collector
    check."""
    candidates = {d for d, lits in LITERALS.items() if prom_name in lits}
    if not candidates:
        otel = _resolve_otel(prom_name)
        if otel:
            candidates = {d for d, lits in LITERALS.items() if otel in lits}
    if len(candidates) == 1:
        return candidates.pop()
    return "shared"


_METRIC_TOKEN_RE = re.compile(
    r"\b(tailscale2otel_[a-zA-Z0-9_]+|tailscale_[a-zA-Z0-9_]+|tailscaled_[a-zA-Z0-9_]+)\b"
)


def metrics_in_expr(expr):
    """Every recognised metric name a PromQL expression queries at vector-selector
    position (not as a label key/value). Label filters narrowing a metric to a value
    subset (`{tailscale_exit_node_state="advertised"}`) do not change which metric owns
    the base series, so this is exactly the granularity #395 asks for: "only whole-metric
    identity matters, label-filtered subsets are the same metric". A token immediately
    followed by `=` is a label key (`tailscale_device_invite_accepted="false"`), not a
    metric-selector reference, and is excluded."""
    found = set()
    for m in _METRIC_TOKEN_RE.finditer(expr):
        tok = m.group(1)
        if expr[m.end(1):m.end(1) + 1] == "=":
            continue
        if tok in ALL_METRIC_NAMES:
            found.add(tok)
    return found


# ---------------------------------------------------------------------------
# dashboard-document walking
# ---------------------------------------------------------------------------

STAT_LIKE = {"stat", "gauge", "bargauge"}


def _matching_gates(conditional_rendering):
    """Sentinel variable names gating this row/tab via `op="matches"` (i.e. "show when
    present") — `notMatches` (hide_when, PII redaction sentinels) do not assert a
    collector is running, so they are not gates for this contract."""
    out = []
    for item in conditional_rendering["spec"]["items"]:
        if item["kind"] == "ConditionalRenderingVariable" and item["spec"]["operator"] == "matches":
            out.append(item["spec"]["variable"])
    return out


def extract_rows(doc):
    """Every row in the built document as (row_title, [gate_names], [(panel_title,
    viz_group, expr), ...]) — gate_names includes both the row's own present= gate AND
    its enclosing tab's gate (a row cannot render unless BOTH hold), and panels list
    every query expression of every panel actually placed in that row (a panel with N
    queries appears once per expr)."""
    elements = doc["spec"]["elements"]
    rows = []

    def panels_of(grid_layout):
        # BOTH layout kinds. #526 decision 6 converts rows that are N same-size panels
        # to AutoGridLayout so they reflow, and a reader of this function that accepted
        # only GridLayout would return an empty panel list for every converted row —
        # which does not FAIL anything here, it just silently stops checking them. The
        # floor assertions below exist because that is exactly what happened.
        out = []
        if grid_layout["kind"] not in ("GridLayout", "AutoGridLayout"):
            return out
        for item in grid_layout["spec"]["items"]:
            el = elements[item["spec"]["element"]["name"]]
            spec = el["spec"]
            viz_group = spec["vizConfig"]["group"]
            for q in spec["data"]["spec"]["queries"]:
                qspec = q["spec"]["query"]["spec"]
                expr = qspec.get("expr") or ""
                out.append((spec["title"], viz_group, expr))
        return out

    def walk(layout, gates):
        kind = layout["kind"]
        if kind == "TabsLayout":
            for t in layout["spec"]["tabs"]:
                tg = gates
                cr = t["spec"].get("conditionalRendering")
                if cr:
                    tg = gates + _matching_gates(cr)
                walk(t["spec"]["layout"], tg)
        elif kind == "RowsLayout":
            for r in layout["spec"]["rows"]:
                rg = gates
                cr = r["spec"].get("conditionalRendering")
                if cr:
                    rg = gates + _matching_gates(cr)
                rows.append((r["spec"].get("title"), rg, panels_of(r["spec"]["layout"])))

    walk(doc["spec"]["layout"], [])
    return rows


def sentinel_metric_map(doc):
    """Sentinel variable name -> the metric its presence query checks (from
    `sentinel()`'s `label_values(<metric>, __name__)` shape), or absent for a
    `raw_sentinel()` gate with no single metric (e.g. `has_multitailnet`)."""
    out = {}
    for v in doc["spec"]["variables"]:
        if v["kind"] != "QueryVariable":
            continue
        q = v["spec"]["query"]["spec"]["query"]
        m = re.match(r"label_values\(([^,]+),\s*__name__\)", q)
        if m:
            out[v["spec"]["name"]] = m.group(1)
    return out


# ---------------------------------------------------------------------------
# the violation-finding function itself — used against the real built dashboard AND,
# with synthetic input, as this gate's own negative test (see NegativeTestFiresTest).
# ---------------------------------------------------------------------------

def gate_owners(gate_names, sentinel_metric):
    """The set of collector owners a row's gates (its own present= plus its enclosing
    tab's) collectively assert are running. `sentinel_metric` maps a gate name to its
    metric (None for a raw_sentinel with no single metric)."""
    owners = set()
    for g in gate_names:
        metric = sentinel_metric.get(g)
        owners.add("shared" if metric is None else collector_owner(metric))
    return owners


def is_cross_collector_violation(metric_owner, owners):
    """True when a zero-filled panel's metric owner cannot be vouched for by anything
    gating the row it sits in.

    - A "shared" metric (self-observability, etc.) always co-exists with any
      collector, so it is never a violation regardless of what gates the row.
    - A row gated ONLY by "shared"/non-metric sentinels asserts nothing about any
      specific collector's presence, so there is nothing to contradict — skip rather
      than guess (this is what keeps `has_multitailnet`-gated rows out of scope).
    - Otherwise: a violation iff the metric's owner is not among the row's gate
      owners.
    """
    if metric_owner == "shared":
        return False
    if owners == {"shared"}:
        return False
    return metric_owner not in owners


def find_cross_collector_violations(rows, sentinel_metric):
    """rows: see extract_rows(). Returns a list of dicts, one per offending
    (panel, metric) pair, naming the panel, its row, the row's gates, the offending
    metric, and both collectors involved."""
    offenders = []
    for (row_title, gates, panels) in rows:
        if not gates:
            continue  # ungated row: not this contract's concern
        owners = gate_owners(gates, sentinel_metric)
        for (panel_title, viz_group, expr) in panels:
            if viz_group not in STAT_LIKE:
                continue
            if "or vector(0)" not in expr:
                continue
            for metric in metrics_in_expr(expr):
                metric_owner = collector_owner(metric)
                if is_cross_collector_violation(metric_owner, owners):
                    offenders.append({
                        "panel": panel_title, "row": row_title, "gates": gates,
                        "metric": metric, "metric_owner": metric_owner,
                        "gate_owners": sorted(owners),
                    })
    return offenders


def _format_offender(o):
    return ("panel %r in row %r (gated by %s, owner(s) %s) zero-fills metric %r "
            "owned by collector %r — the row can render while that collector is "
            "absent, so the panel's `0` is a lie" %
            (o["panel"], o["row"], o["gates"], o["gate_owners"], o["metric"], o["metric_owner"]))


# ---------------------------------------------------------------------------
# tests
# ---------------------------------------------------------------------------

class CollectorOwnershipDerivationTest(unittest.TestCase):
    """Guards the derivation itself: if the source/doc scan breaks, every other test
    in this file passes vacuously (everything falls back to "shared" and nothing is
    ever flagged), so this class proves the scan is actually finding things."""

    def test_scan_finds_a_reasonable_number_of_collector_dirs(self):
        dirs = _collector_dirs()
        self.assertGreaterEqual(len(dirs), 10, "collector directory scan found too few dirs")
        for expected in ("acl", "devices", "nodemetrics", "keys", "users"):
            self.assertIn(expected, dirs)

    def test_scan_finds_a_reasonable_number_of_literals_per_known_collector(self):
        for d in ("acl", "devices", "nodemetrics"):
            self.assertGreater(len(LITERALS[d]), 3, "%s: suspiciously few literals found" % d)

    def test_docs_table_parse_is_not_empty(self):
        self.assertGreater(len(PROM_TO_OTEL), 100, "docs/metrics.md table parse found too little")

    def test_known_metrics_resolve_to_their_documented_collector(self):
        # Spot-checks against the three known #395 violations plus the raw
        # tailscaled_* forwarded family, so a change to the scan regex/extraction
        # can't silently stop resolving the exact metrics this contract cares about.
        cases = [
            ("tailscale_acl_unrestricted_rules_ratio", "acl"),
            ("tailscale_devices_untagged_ratio", "devices"),
            ("tailscale_subnet_routes_unapproved", "devices"),
            ("tailscale_device_invites_count_ratio", "devices"),
            ("tailscale_node_up_ratio", "nodemetrics"),
            ("tailscaled_inbound_bytes_total", "nodemetrics"),
        ]
        for prom_name, expected in cases:
            self.assertEqual(collector_owner(prom_name), expected, prom_name)

    def test_selfobs_and_unresolvable_metrics_bucket_as_shared(self):
        for prom_name in ("tailscale2otel_series_active", "tailscale2otel_scrape_success_ratio",
                           "no_such_metric_anywhere"):
            self.assertEqual(collector_owner(prom_name), "shared", prom_name)

    def test_histogram_suffix_stripping_resolves_to_the_base_metrics_collector(self):
        # has_key_expiry_hist gates on the _count variant of a histogram whose base
        # name is all docs/metrics.md ever lists; without suffix-stripping this
        # metric falls back to "shared" and the whole row's gate becomes untestable.
        self.assertEqual(collector_owner("tailscale_devices_key_expiry_days_count"), "devices")
        self.assertEqual(collector_owner("tailscale_devices_key_expiry_days_bucket"), "devices")

    def test_metrics_in_expr_ignores_label_keys_and_matches_selector_position(self):
        expr = ('sum(last_over_time(tailscale_device_invites_count_ratio'
                '{tailscale_device_invite_accepted="false",tailscale_device_invite_allow_exit_node="true"}'
                '[2h])) or vector(0)')
        self.assertEqual(metrics_in_expr(expr), {"tailscale_device_invites_count_ratio"})

    def test_metrics_in_expr_finds_every_metric_in_a_multi_metric_expression(self):
        expr = "sum(tailscale_acl_unrestricted_rules_ratio) + sum(tailscale_devices_untagged_ratio)"
        self.assertEqual(metrics_in_expr(expr),
                         {"tailscale_acl_unrestricted_rules_ratio", "tailscale_devices_untagged_ratio"})


class CrossCollectorZeroFillTest(unittest.TestCase):
    """The gate itself, run against the real built dashboard."""

    def setUp(self):
        self.doc = dashboard.build_family()
        self.rows = extract_rows(self.doc)
        self.sentinel_metric = sentinel_metric_map(self.doc)

    def test_no_gated_stat_panel_zero_fills_a_different_collectors_metric(self):
        offenders = find_cross_collector_violations(self.rows, self.sentinel_metric)
        if offenders:
            self.fail("cross-collector zero-fill violation(s):\n" +
                      "\n".join(_format_offender(o) for o in offenders))

    def test_the_scan_actually_examined_a_reasonable_number_of_gated_rows(self):
        # Guards the guard: if the layout-walk regresses to finding zero gated rows,
        # the assertion above passes on an empty scan rather than a clean dashboard.
        gated = [r for r in self.rows if r[1]]
        self.assertGreaterEqual(len(gated), 30, "found suspiciously few gated rows")
        zero_fill_stat_panels = 0
        for (_title, gates, panels) in self.rows:
            if not gates:
                continue
            for (_ptitle, viz_group, expr) in panels:
                if viz_group in STAT_LIKE and "or vector(0)" in expr:
                    zero_fill_stat_panels += 1
        self.assertGreater(zero_fill_stat_panels, 5,
                           "found suspiciously few zero-filled stat-like panels in gated rows")


class RowScanIsNotBlindTest(unittest.TestCase):
    """Every check in this file reads panels through extract_rows(). If that walker
    does not understand a layout kind it returns an empty panel list, and every
    per-panel assertion then passes VACUOUSLY on the rows it could not read — no
    failure, just less coverage.

    That is not hypothetical. #526 decision 6 converted uniform rows to
    AutoGridLayout so they reflow; the walker accepted only GridLayout, and every
    converted row — including "Exit nodes", which one test names explicitly —
    silently left the contract. The only reason it surfaced is that the named test
    asserted its panel was FOUND before checking it. These assertions generalise
    that: a row that reads as empty is a bug in the reader, not a row without panels,
    because the generator cannot build one.
    """

    def setUp(self):
        self.rows = extract_rows(dashboard.build_family())

    def test_no_row_reads_as_having_zero_panels(self):
        empty = sorted(title for (title, _gates, panels) in self.rows if not panels)
        self.assertEqual(empty, [],
                         "extract_rows() found no panels in these rows — it almost "
                         "certainly does not understand their layout kind, so every "
                         "check in this file is passing vacuously on them")

    def test_the_scan_covers_the_whole_family(self):
        # A floor, so a walker that silently stopped descending into (say) sub-tabs
        # fails here rather than quietly shrinking what is checked.
        self.assertGreater(len(self.rows), 100, "row scan found far too little")

    def test_negative_an_unreadable_layout_kind_would_be_caught(self):
        """Proves the zero-panel assertion is not vacuous: a row whose layout kind the
        walker does not handle must read as empty and be reported."""
        doc = {"spec": {"elements": {}, "layout": {"kind": "TabsLayout", "spec": {"tabs": [
            {"kind": "TabsLayoutTab", "spec": {"title": "T", "layout": {
                "kind": "RowsLayout", "spec": {"rows": [
                    {"kind": "RowsLayoutRow", "spec": {
                        "title": "future layout", "layout": {
                            "kind": "SomeLayoutKindFromTheFuture", "spec": {"items": []}}}}]}}}},
        ]}}}}
        self.assertEqual([t for (t, _g, p) in extract_rows(doc) if not p],
                         ["future layout"])


class NotViolationsStayInvisibleTest(unittest.TestCase):
    """Explicit regression coverage for the five cases #395 names as deliberately NOT
    violations — if collector-ownership derivation is ever "fixed" in a way that makes
    it fire on one of these, that is the derivation being wrong, not a new real bug
    (per the task brief: fix the derivation, do not add an exception list)."""

    def setUp(self):
        self.doc = dashboard.build_family()
        self.rows = extract_rows(self.doc)
        self.sentinel_metric = sentinel_metric_map(self.doc)
        self.by_row = {title: (gates, panels) for (title, gates, panels) in self.rows}

    def _panel_exprs(self, row_title, panel_title):
        _gates, panels = self.by_row[row_title]
        return [expr for (t, _vg, expr) in panels if t == panel_title]

    def test_exit_nodes_row_unapproved_subnet_routes_is_not_flagged(self):
        # has_exit (devices) containing "Unapproved subnet routes" (devices) — same
        # collector, honest zero. NB this is a DIFFERENT panel instance from the one
        # of the same title inside "Security scorecard" (has_acl_risk) — titles repeat
        # across rows in this dashboard, panels do not.
        self.assertIn("Exit nodes", self.by_row)
        exprs = self._panel_exprs("Exit nodes", "Unapproved subnet routes")
        self.assertTrue(exprs, "expected panel not found in row")
        gates, _panels = self.by_row["Exit nodes"]
        owners = gate_owners(gates, self.sentinel_metric)
        for expr in exprs:
            for metric in metrics_in_expr(expr):
                self.assertFalse(is_cross_collector_violation(collector_owner(metric), owners),
                                 "Exit nodes / Unapproved subnet routes wrongly flagged")

    def test_selfobs_rows_are_never_flagged(self):
        # "Service health (golden signals)" / "App health" — has_selfobs containing
        # Scrape budget (max) / Config warnings: all self-observability, always
        # emitted together.
        for row_title in self.by_row:
            gates, panels = self.by_row[row_title]
            if "has_selfobs" not in gates:
                continue
            owners = gate_owners(gates, self.sentinel_metric)
            for (_ptitle, viz_group, expr) in panels:
                if viz_group not in STAT_LIKE or "or vector(0)" not in expr:
                    continue
                for metric in metrics_in_expr(expr):
                    self.assertFalse(is_cross_collector_violation(collector_owner(metric), owners))

    def test_key_expiry_distribution_row_is_not_flagged(self):
        # has_key_expiry_hist (devices) containing "Keys already expired" (devices) —
        # same collector. This is also the histogram-suffix regression case.
        self.assertIn("Key-expiry distribution", self.by_row)
        gates, panels = self.by_row["Key-expiry distribution"]
        owners = gate_owners(gates, self.sentinel_metric)
        found_zero_fill = False
        for (_ptitle, viz_group, expr) in panels:
            if viz_group not in STAT_LIKE or "or vector(0)" not in expr:
                continue
            for metric in metrics_in_expr(expr):
                found_zero_fill = True
                self.assertFalse(is_cross_collector_violation(collector_owner(metric), owners))
        self.assertTrue(found_zero_fill, "expected at least one zero-filled stat panel here")

    def test_security_scorecards_own_acl_fed_panels_are_not_flagged(self):
        # Unrestricted ACL rules / Auto-approved exit nodes / SSH wildcard enabled —
        # same collector (acl) as the row's own has_acl_risk sentinel.
        self.assertIn("Security scorecard", self.by_row)
        gates, panels = self.by_row["Security scorecard"]
        owners = gate_owners(gates, self.sentinel_metric)
        for title in ("Unrestricted ACL rules", "Auto-approved exit nodes", "SSH wildcard enabled"):
            exprs = [expr for (t, vg, expr) in panels if t == title and vg in STAT_LIKE]
            self.assertTrue(exprs, "%r not found in Security scorecard" % title)
            for expr in exprs:
                for metric in metrics_in_expr(expr):
                    self.assertFalse(is_cross_collector_violation(collector_owner(metric), owners),
                                     "%s wrongly flagged" % title)


class NegativeTestFiresTest(unittest.TestCase):
    """Proves the gate actually catches a violation, independent of whatever state
    tabs/overview.py happens to be in right now (a sibling change is landing the fix
    for the three real #395 violations concurrently with this test file). This
    reproduces the exact violation shape — an acl-gated row containing a devices-owned
    zero-filled stat panel — as synthetic input to the SAME find_cross_collector_violations
    used against the real dashboard above, so the check under test is the real one, not
    a restatement of it.

    This was also verified against the real dashboard by temporarily reverting
    tabs/overview.py to its pre-fix (committed) state and re-running the gate: it named
    exactly the three panels documented in WAVE_C_SPEC.md
    ("Unapproved subnet routes", "Untagged devices", "Pending exit-node shares", all
    gated by has_acl_risk on the acl collector while querying devices-owned metrics) —
    see this task's final report for the verbatim output.
    """

    def test_gate_fires_on_a_cross_collector_zero_fill(self):
        sentinel_metric = {"has_acl_risk": "tailscale_acl_unrestricted_rules_ratio"}
        rows = [(
            "Security scorecard", ["has_acl_risk"],
            [("Untagged devices", "stat",
              "max(last_over_time(tailscale_devices_untagged_ratio[10m])) or vector(0)")],
        )]
        offenders = find_cross_collector_violations(rows, sentinel_metric)
        self.assertEqual(len(offenders), 1)
        o = offenders[0]
        self.assertEqual(o["panel"], "Untagged devices")
        self.assertEqual(o["row"], "Security scorecard")
        self.assertEqual(o["metric"], "tailscale_devices_untagged_ratio")
        self.assertEqual(o["metric_owner"], "devices")
        self.assertEqual(o["gate_owners"], ["acl"])

    def test_gate_clears_once_the_fix_shape_is_applied(self):
        # The real fix: drop `or vector(0)` (an absent panel then has no value to
        # zero-fill, so it is out of this gate's scope entirely — see the empty-state
        # text-rule tests below for the OTHER half of the real fix).
        sentinel_metric = {"has_acl_risk": "tailscale_acl_unrestricted_rules_ratio"}
        rows = [(
            "Security scorecard", ["has_acl_risk"],
            [("Untagged devices", "stat",
              "max(last_over_time(tailscale_devices_untagged_ratio[10m]))")],
        )]
        self.assertEqual(find_cross_collector_violations(rows, sentinel_metric), [])

    def test_gate_stays_silent_on_a_same_collector_zero_fill(self):
        sentinel_metric = {"has_exit": "tailscale_device_exit_node_ratio"}
        rows = [(
            "Exit nodes", ["has_exit"],
            [("Unapproved subnet routes", "stat",
              "max(last_over_time(tailscale_subnet_routes_unapproved[2h])) or vector(0)")],
        )]
        self.assertEqual(find_cross_collector_violations(rows, sentinel_metric), [])

    def test_gate_stays_silent_when_the_only_gate_is_collector_agnostic(self):
        # has_multitailnet-shaped gate: a raw_sentinel with no single metric, so
        # sentinel_metric has no entry for it -> "shared" owner -> nothing to check
        # against. This must not be guessed at as a violation.
        sentinel_metric = {}
        rows = [(
            "MSP scorecard", ["has_multitailnet"],
            [("Tailnets observed", "stat",
              "count(tailscale_devices_untagged_ratio) or vector(0)")],
        )]
        self.assertEqual(find_cross_collector_violations(rows, sentinel_metric), [])

    def test_gate_ignores_non_stat_like_panels(self):
        sentinel_metric = {"has_acl_risk": "tailscale_acl_unrestricted_rules_ratio"}
        rows = [(
            "Security scorecard", ["has_acl_risk"],
            [("Untagged devices over time", "timeseries",
              "max(tailscale_devices_untagged_ratio) or vector(0)")],
        )]
        self.assertEqual(find_cross_collector_violations(rows, sentinel_metric), [])


# ---------------------------------------------------------------------------
# empty-state text rules (global, not scoped to a hand-picked panel list)
# ---------------------------------------------------------------------------

# Phrasings that state a reason for absence as fact. Absence cannot distinguish
# "disabled" from "unsupported" from "never-deployed" — all three produce no series —
# so asserting a cause is a guess rendered as fact (same rule test_selfobs_coverage.py,
# test_policy_inventory.py and test_fleet_signals.py already enforce, but each only
# over its own hand-picked panel inventory). This test applies it to every prose
# noValue string in the whole document.
FORBIDDEN_CAUSE_PHRASES = ("is disabled", "is off", "not deployed", "because")

_NUMERIC_NOVALUE_RE = re.compile(r"^-?\d+(\.\d+)?$")

# A bare number ("0") is a display-formatting default (render absent data as the
# literal number zero), not an attempted prose empty-state message, so the length/
# cause-phrase rules below — which are about what an empty-state SENTENCE claims —
# do not apply to it. This mirrors test_semantic_maps.py's own
# `assertNotEqual(novalue.strip(), "0", ...)`, generalised from its hand-list to every
# panel in the document.


def _all_novalue_strings(doc):
    """(panel_title, novalue_string) for every panel that sets a noValue default."""
    out = []
    for el in doc["spec"]["elements"].values():
        spec = el["spec"]
        nv = spec["vizConfig"]["spec"]["fieldConfig"]["defaults"].get("noValue")
        if nv is not None:
            out.append((spec["title"], nv))
    return out


class EmptyStateTextRulesTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build_family()
        self.novalues = _all_novalue_strings(self.doc)

    def test_the_scan_actually_found_prose_empty_states(self):
        prose = [(t, nv) for (t, nv) in self.novalues if not _NUMERIC_NOVALUE_RE.match(nv.strip())]
        self.assertGreater(len(prose), 20, "found suspiciously few prose empty-state strings")

    def test_every_prose_empty_state_says_more_than_a_word(self):
        for (title, nv) in self.novalues:
            if _NUMERIC_NOVALUE_RE.match(nv.strip()):
                continue  # a bare number is a display default, not a prose message
            with self.subTest(panel=title):
                self.assertGreater(len(nv), 20,
                                   "%r: empty-state text says too little to name a "
                                   "prerequisite (%r)" % (title, nv))

    def test_no_prose_empty_state_asserts_a_cause_as_fact(self):
        offenders = []
        for (title, nv) in self.novalues:
            if _NUMERIC_NOVALUE_RE.match(nv.strip()):
                continue
            lowered = nv.lower()
            for phrase in FORBIDDEN_CAUSE_PHRASES:
                if phrase in lowered:
                    offenders.append((title, phrase, nv))
        self.assertEqual(offenders, [],
                         "empty-state text states a cause as fact (absence cannot "
                         "distinguish disabled from unsupported from never-deployed): "
                         + "; ".join("%r contains %r" % (t, p) for (t, p, _nv) in offenders))

    def test_the_cause_phrase_check_can_fail(self):
        # Guards the guard.
        sample = "this metric is off right now"
        self.assertTrue(any(p in sample for p in FORBIDDEN_CAUSE_PHRASES))


if __name__ == "__main__":
    unittest.main()
