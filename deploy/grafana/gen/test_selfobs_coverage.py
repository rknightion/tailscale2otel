#!/usr/bin/env python3
"""Coverage tests for the exporter-health signals rebuilt in #386/#399/#404/#405.

The disposition manifest (#390) proved a block of catalog signals had no
dashboard and no alert coverage at all. This file locks the dashboard half of
that gap shut for the object-store, ingress-WAL, subrequest, capability,
rate-limiter and receiver-loss families.

Everything here asserts against the BUILT dashboard document rather than the
generator's source text. A source-text scan passes on a metric name that sits
in a comment, in a dead local, or in a panel that no row ever places — the
three ways a "covered" signal renders nowhere. Reading the built document is
the only check that tracks what an operator actually sees.

Three shapes of assertion, each guarding a different regression:

1. per-signal presence — a signal that stops being queried fails here;
2. an INVENTORY assertion that the covered set is exactly the intended one, so
   deleting a panel cannot quietly shrink coverage while the suite stays green;
3. guards-the-guard — the scan itself is proved capable of failing, because a
   substring scan over a large JSON blob is exactly the kind of test that
   passes vacuously once the extraction breaks.
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


dashboard = load_module("tailscale2otel_dashboard_selfobs", Path(__file__).with_name("build.py"))

# The health dashboard's leaf tabs (#526). Before the split these were one
# "Exporter Diagnostics" tab of 83 panels; the point of naming them separately is
# that this file now checks PLACEMENT — that a family lands on the pipeline stage
# it belongs to — rather than mere presence. Presence is the Go gate's job
# (internal/catalog/coverage_test.go, catalog -> panel over both artifacts), and
# checking it in both places means every panel move fails two tests for one reason.
OVERVIEW = "Overview"
COLLECTION = "Collection"
INGESTION = "Ingestion"
DELIVERY = "Delivery"
RUNTIME = "Runtime"
CARDINALITY = "Cost & Cardinality"
INTERNALS = "Exporter internals"

_TABS = (OVERVIEW, COLLECTION, INGESTION, DELIVERY, RUNTIME, CARDINALITY, INTERNALS)

# Every signal this lane took ownership of, mapped to the tab that must query it.
# Frozen on purpose: this doubles as the inventory assertion, so a future panel
# deletion fails rather than silently reopening the coverage hole (#390).
COVERED = {
    # --- object-store ingestion, #399 (all 17) --------------------------------
    "tailscale2otel_objectstore_objects_total": INGESTION,
    "tailscale2otel_objectstore_records_total": INGESTION,
    "tailscale2otel_objectstore_bytes_total": INGESTION,
    "tailscale2otel_objectstore_decompressed_bytes_total": INGESTION,
    "tailscale2otel_objectstore_expansion_limit_failures_total": INGESTION,
    "tailscale2otel_objectstore_skipped_total": INGESTION,
    "tailscale2otel_objectstore_backlog_ratio": INGESTION,
    "tailscale2otel_objectstore_scan_truncated_ratio": INGESTION,
    "tailscale2otel_objectstore_gaps_ratio": INGESTION,
    "tailscale2otel_objectstore_gap_oldest_age_seconds": INGESTION,
    "tailscale2otel_objectstore_gap_healthy_ratio": INGESTION,
    "tailscale2otel_objectstore_requests_total": INGESTION,
    "tailscale2otel_objectstore_request_duration_seconds_bucket": INGESTION,
    "tailscale2otel_objectstore_retries_total": INGESTION,
    "tailscale2otel_objectstore_cursor_age_seconds": INGESTION,
    "tailscale2otel_objectstore_discovered_newest_age_seconds": INGESTION,
    "tailscale2otel_objectstore_pending_oldest_age_seconds": INGESTION,
    # --- ingress WAL, #386 ----------------------------------------------------
    "tailscale2otel_ingress_wal_pending_entries_ratio": INGESTION,
    "tailscale2otel_ingress_wal_pending_size_bytes": INGESTION,
    "tailscale2otel_ingress_wal_orphan_stages_ratio": INGESTION,
    "tailscale2otel_ingress_wal_orphan_size_bytes": INGESTION,
    "tailscale2otel_ingress_wal_completion_markers_ratio": INGESTION,
    # --- per-entity subrequest fan-out, #386 ----------------------------------
    "tailscale2otel_subrequest_attempts_total": INGESTION,
    "tailscale2otel_subrequest_coverage_ratio": INGESTION,
    "tailscale2otel_subrequest_failures_total": INGESTION,
    # --- capability / scope preflight, #386 -----------------------------------
    "tailscale2otel_capability_status_ratio": COLLECTION,
    "tailscale2otel_capability_scope_satisfied_ratio": COLLECTION,
    # --- API rate-limiter wait + probe freshness, #404 ------------------------
    "tailscale2otel_api_rate_limit_wait_seconds_bucket": COLLECTION,
    "tailscale2otel_api_last_probe_seconds": COLLECTION,
    # --- loss / overflow, #405 ------------------------------------------------
    "tailscale_rdns_cache_overflows_total": INTERNALS,
    "tailscale_stream_skipped_total": INGESTION,
    "tailscale_webhook_request_duration_seconds_bucket": INGESTION,
    "tailscale2otel_nodemetrics_metric_names_dropped_total": CARDINALITY,
}

# Panels whose section is optional (its data exists only under a specific
# configuration) and therefore MUST carry a prerequisite-aware empty state.
# Presence cannot distinguish disabled from unsupported from never-deployed —
# all three produce no series — so the text names the prerequisite and never
# claims which one is unmet (#385).
# The panels that must carry a prerequisite-aware empty state are DERIVED, not
# listed (#526). A frozen title list rots on every consolidation — the moment a
# panel is merged or renamed the assertion fails for a reason that has nothing to
# do with what it is checking, and the reflex is to edit the list rather than look.
#
# So the direction is inverted: every empty state that EXISTS must be well-formed,
# and the count may not fall below a floor. That keeps both halves of the original
# contract — #385's "never state a cause as fact", and "optional sections say what
# they need" — while surviving a rename, and it extends automatically to empty
# states added by tabs that did not exist when the list was written.
EMPTY_STATE_FLOOR = 30

# Phrasings that state a reason for absence as fact. An empty panel cannot tell
# "feature off" from "feature unsupported on this tailnet" from "never deployed",
# so any of these renders a guess as an answer.
CAUSE_ASSERTIONS = ("is disabled", "is off", "not deployed", "is not enabled",
                    "means the feature", "because ")

# The bounded attribute keys the object-store family is allowed to group by.
# Everything else in that family is deliberately attribute-free so no object key,
# bucket name, endpoint or credential can reach a label (#399).
OBJECTSTORE_GROUP_LABELS = {
    # Not a real series label: `source` is SYNTHESISED by label_replace on the
    # merged loss panel, with three fixed values (stream/webhook/objectstore). It
    # is bounded by construction rather than by the catalog, which is why it needs
    # naming here explicitly rather than being derived from the attribute list.
    "source","reason", "limit", "operation", "outcome", "le"}


def panels(doc):
    """title -> panel spec, for every panel the built document actually places.

    Keyed by title because that is what an operator sees; a duplicate title
    would mask one of the two panels, so it is rejected outright.
    """
    out = {}
    for element in doc["spec"]["elements"].values():
        if element.get("kind") != "Panel":
            continue
        spec = element["spec"]
        title = spec["title"]
        if title in out:
            raise AssertionError("two panels share the title %r" % title)
        out[title] = spec
    return out


def panel_exprs(spec):
    """Every Prometheus expression a panel queries (Loki/Tempo targets have none)."""
    out = []
    for query in spec["data"]["spec"]["queries"]:
        expr = query["spec"]["query"]["spec"].get("expr")
        if expr:
            out.append(expr)
    return out


def all_exprs(doc):
    out = []
    for spec in panels(doc).values():
        out.extend(panel_exprs(spec))
    return out


def novalue(spec):
    return spec["vizConfig"]["spec"]["fieldConfig"]["defaults"].get("noValue")


def _tab_of(panels, title):
    """Which health tab carries the panel titled `title`. Placement is what this
    file checks since #526; presence is the Go gate's job."""
    for tab, byTitle in panels.items():
        if title in byTitle:
            return tab
    raise AssertionError("no health tab carries a panel titled %r" % title)


class _TabDocs(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.docs = {name: dashboard.build(dashboard.dashboards.HEALTH, True, only=name)
                    for name in _TABS}
        cls.exprs = {name: all_exprs(doc) for (name, doc) in cls.docs.items()}
        cls.panels = {name: panels(doc) for (name, doc) in cls.docs.items()}

    def _panel(self, title):
        """The panel spec for `title`, wherever on the health dashboard it lives."""
        return self.panels[_tab_of(self.panels, title)][title]


class SignalCoverageTest(_TabDocs):
    def test_every_assigned_signal_is_queried_by_its_tab(self):
        for (metric, tab) in sorted(COVERED.items()):
            with self.subTest(metric=metric):
                hits = [e for e in self.exprs[tab] if metric in e]
                self.assertTrue(hits, "%s is queried by no panel on the %r tab" % (metric, tab))

    def test_covered_inventory_is_exactly_what_this_lane_intends(self):
        # An inventory check, not a per-signal one: a deleted panel that takes the
        # last reference to a signal with it must fail even if some OTHER panel
        # still mentions a similarly-named series.
        found = set()
        for (metric, tab) in COVERED.items():
            if any(metric in e for e in self.exprs[tab]):
                found.add(metric)
        self.assertEqual(found, set(COVERED),
                         "coverage shrank: %s no longer queried" % sorted(set(COVERED) - found))
        self.assertEqual(len(COVERED), 33,
                         "the assigned signal count changed; update COVERED deliberately, "
                         "not to make this test pass")

    def test_scan_is_not_vacuous(self):
        # Guards the guard. If panels()/panel_exprs() ever stop extracting queries,
        # every assertion above degrades to "not found in nothing" — which is how a
        # substring scan over JSON passes while covering nothing at all.
        self.assertGreater(sum(len(v) for v in self.exprs.values()), 50)
        self.assertGreater(len(self.exprs[CARDINALITY]), 10)
        for tab in _TABS:
            self.assertNotIn(
                "tailscale2otel_objectstore_metric_that_does_not_exist",
                " ".join(self.exprs[tab]),
                "the scan matches a metric name that is not in any panel — "
                "the extraction is returning something other than panel queries")


class EmptyStateTest(_TabDocs):
    def _spec(self, title):
        for tab in _TABS:
            if title in self.panels[tab]:
                return self.panels[tab][title]
        self.fail("no panel titled %r on either tab" % title)

    def _empty_states(self):
        """(title, text) for every panel on the health dashboard carrying a noValue."""
        out = []
        for byTitle in self.panels.values():
            for title, spec in byTitle.items():
                text = novalue(spec)
                if text:
                    out.append((title, text))
        return out

    def test_optional_sections_carry_a_prerequisite_aware_empty_state(self):
        states = self._empty_states()
        self.assertGreaterEqual(
            len(states), EMPTY_STATE_FLOOR,
            "only %d panels carry an empty state, below the floor of %d — empty states "
            "are how an operator tells 'switched off' from 'broken', so losing them is "
            "a regression even though nothing renders differently"
            % (len(states), EMPTY_STATE_FLOOR))
        for title, text in states:
            with self.subTest(panel=title):
                # Two kinds of empty state are both legitimate and only one of them
                # is making a prerequisite claim:
                #   "0"                          — zero IS the reading (a counter)
                #   "Last error: none"           — a state, fully self-describing
                #   "No <x> series. Requires ..." — an OPTIONAL section explaining
                #                                   what has to be switched on
                # Only the third kind is asserted on, and it is recognised by the
                # convention it already follows rather than by a frozen title list:
                # a text that opens by reporting an ABSENCE has to say what would
                # make it present, or it leaves the reader with "no data" and no
                # next step — which is the whole point of #385/#386.
                # The established shape is exactly "No <thing> series. Requires ...":
                # a first sentence reporting that a SERIES FAMILY is absent, then what
                # would make it appear. Matching that shape precisely matters — a
                # looser test catches states that merely start with "No " ("No metrics
                # over cap - all series fully resolved.") and demands a prerequisite
                # from a panel that is reporting good news.
                if not re.match(r"^No .+ series\. ", text):
                    continue
                self.assertIn("Requires ", text,
                              "panel %r's empty state reports an absence but never says "
                              "what would make the data appear" % title)

    def test_empty_states_never_assert_a_cause(self):
        for title, raw in self._empty_states():
            text = raw.lower()
            for phrase in CAUSE_ASSERTIONS:
                with self.subTest(panel=title, phrase=phrase):
                    self.assertNotIn(phrase, text,
                                     "panel %r's empty state states a cause as fact; absence "
                                     "cannot distinguish disabled from unsupported from "
                                     "never-deployed (#385)" % title)

    def test_the_cause_assertion_check_can_fail(self):
        # Guards the guard: prove the phrase list actually matches something.
        sample = "This section is disabled on this tailnet."
        self.assertTrue(any(p in sample.lower() for p in CAUSE_ASSERTIONS))


class ObjectStoreCardinalityTest(_TabDocs):
    def test_object_store_panels_group_only_by_bounded_attributes(self):
        # Bucket names, object keys, endpoints and credentials must never reach a
        # label. The catalog gives the object-store family exactly four bounded
        # attribute keys; anything else in a `by (...)` clause on an objectstore
        # series is a cardinality/PII regression, not a panel improvement.
        import re
        by_clause = re.compile(r"by \(([^)]*)\)")
        for expr in [e for v in self.exprs.values() for e in v]:
            if "tailscale2otel_objectstore_" not in expr:
                continue
            for group in by_clause.findall(expr):
                for label in (part.strip() for part in group.split(",")):
                    if not label:
                        continue
                    with self.subTest(expr=expr, label=label):
                        self.assertIn(label, OBJECTSTORE_GROUP_LABELS)

    def test_newest_discovered_age_renders_its_minus_one_sentinel(self):
        # -1 means "the cycle listed no object with a usable timestamp". It is never
        # absent and never a confusable 0, so the panel must not render it as an age.
        spec = self._panel("Object-store age (cursor & newest object)")
        mappings = spec["vizConfig"]["spec"]["fieldConfig"]["defaults"].get("mappings") or []
        keys = set()
        for mapping in mappings:
            keys.update((mapping.get("options") or {}).keys())
        self.assertIn("-1", keys,
                      "the -1 sentinel is unmapped, so it renders as a negative age")


class LossPairingTest(_TabDocs):
    def test_every_loss_counter_is_paired_with_accepted_volume(self):
        # "12 dropped" is unreadable without "of how many", so each loss counter
        # shares a panel with the accepted-volume series it should be read against.
        for (title, loss, accepted) in (
            ("rDNS cache overflows vs lookups/s",
             "tailscale_rdns_cache_overflows_total", "tailscale_rdns_cache_lookups_total"),
            ("Stream records accepted vs skipped/s",
             "tailscale_stream_skipped_total", "tailscale_stream_records_total"),
            # Merged with the schema-drift panel by the Ingestion tab (#526); the
             # pairing this asserts is unchanged, only the panel it lives on.
             ("Webhook accepted vs duplicates & schema drift/s",
             "tailscale_webhook_duplicates_total", "tailscale_webhook_events_total"),
            ("Forwarded metric-name drops/s by reason",
             "tailscale2otel_nodemetrics_metric_names_dropped_total",
             "tailscale2otel_series_by_group"),
        ):
            with self.subTest(panel=title):
                tab = _tab_of(self.panels, title)
                joined = " ".join(panel_exprs(self.panels[tab][title]))
                self.assertIn(loss, joined)
                self.assertIn(accepted, joined)


class RateLimiterSeparationTest(_TabDocs):
    def test_rate_limiter_wait_is_not_mixed_into_the_api_latency_panel(self):
        # Rate-limiter wait and upstream API latency are different faults, and
        # api.duration deliberately excludes the wait. Charting them as one series
        # would make a throttled poller look like a slow tailnet.
        latency = " ".join(panel_exprs(
            self._panel("API latency p50/p95/p99 by endpoint")))
        self.assertNotIn("rate_limit_wait", latency)
        quantiles = " ".join(panel_exprs(
            self._panel("Rate-limiter wait p50/p95/p99 by endpoint")))
        self.assertNotIn("tailscale2otel_api_duration_seconds", quantiles)

    def test_wait_versus_wire_panel_charts_both_budgets(self):
        expr = " ".join(panel_exprs(
            self._panel("Time waiting vs time on the wire")))
        self.assertIn("tailscale2otel_api_rate_limit_wait_seconds_sum", expr)
        self.assertIn("tailscale2otel_api_duration_seconds_sum", expr)


if __name__ == "__main__":
    unittest.main()
