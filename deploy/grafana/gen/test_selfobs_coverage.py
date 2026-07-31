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
import unittest


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


dashboard = load_module("tailscale2otel_dashboard_selfobs", Path(__file__).with_name("build.py"))

DIAGNOSTICS = "Exporter Diagnostics"
CARDINALITY = "Cardinality & Cost"

# Every signal this lane took ownership of, mapped to the tab that must query it.
# Frozen on purpose: this doubles as the inventory assertion, so a future panel
# deletion fails rather than silently reopening the coverage hole (#390).
COVERED = {
    # --- object-store ingestion, #399 (all 17) --------------------------------
    "tailscale2otel_objectstore_objects_total": DIAGNOSTICS,
    "tailscale2otel_objectstore_records_total": DIAGNOSTICS,
    "tailscale2otel_objectstore_bytes_total": DIAGNOSTICS,
    "tailscale2otel_objectstore_decompressed_bytes_total": DIAGNOSTICS,
    "tailscale2otel_objectstore_expansion_limit_failures_total": DIAGNOSTICS,
    "tailscale2otel_objectstore_skipped_total": DIAGNOSTICS,
    "tailscale2otel_objectstore_backlog_ratio": DIAGNOSTICS,
    "tailscale2otel_objectstore_scan_truncated_ratio": DIAGNOSTICS,
    "tailscale2otel_objectstore_gaps_ratio": DIAGNOSTICS,
    "tailscale2otel_objectstore_gap_oldest_age_seconds": DIAGNOSTICS,
    "tailscale2otel_objectstore_gap_healthy_ratio": DIAGNOSTICS,
    "tailscale2otel_objectstore_requests_total": DIAGNOSTICS,
    "tailscale2otel_objectstore_request_duration_seconds_bucket": DIAGNOSTICS,
    "tailscale2otel_objectstore_retries_total": DIAGNOSTICS,
    "tailscale2otel_objectstore_cursor_age_seconds": DIAGNOSTICS,
    "tailscale2otel_objectstore_discovered_newest_age_seconds": DIAGNOSTICS,
    "tailscale2otel_objectstore_pending_oldest_age_seconds": DIAGNOSTICS,
    # --- ingress WAL, #386 ----------------------------------------------------
    "tailscale2otel_ingress_wal_pending_entries_ratio": DIAGNOSTICS,
    "tailscale2otel_ingress_wal_pending_size_bytes": DIAGNOSTICS,
    "tailscale2otel_ingress_wal_orphan_stages_ratio": DIAGNOSTICS,
    "tailscale2otel_ingress_wal_orphan_size_bytes": DIAGNOSTICS,
    "tailscale2otel_ingress_wal_completion_markers_ratio": DIAGNOSTICS,
    # --- per-entity subrequest fan-out, #386 ----------------------------------
    "tailscale2otel_subrequest_attempts_total": DIAGNOSTICS,
    "tailscale2otel_subrequest_coverage_ratio": DIAGNOSTICS,
    "tailscale2otel_subrequest_failures_total": DIAGNOSTICS,
    # --- capability / scope preflight, #386 -----------------------------------
    "tailscale2otel_capability_status_ratio": DIAGNOSTICS,
    "tailscale2otel_capability_scope_satisfied_ratio": DIAGNOSTICS,
    # --- API rate-limiter wait + probe freshness, #404 ------------------------
    "tailscale2otel_api_rate_limit_wait_seconds_bucket": DIAGNOSTICS,
    "tailscale2otel_api_last_probe_seconds": DIAGNOSTICS,
    # --- loss / overflow, #405 ------------------------------------------------
    "tailscale_rdns_cache_overflows_total": DIAGNOSTICS,
    "tailscale_stream_skipped_total": DIAGNOSTICS,
    "tailscale_webhook_request_duration_seconds_bucket": DIAGNOSTICS,
    "tailscale2otel_nodemetrics_metric_names_dropped_total": CARDINALITY,
}

# Panels whose section is optional (its data exists only under a specific
# configuration) and therefore MUST carry a prerequisite-aware empty state.
# Presence cannot distinguish disabled from unsupported from never-deployed —
# all three produce no series — so the text names the prerequisite and never
# claims which one is unmet (#385).
EMPTY_STATE_PANELS = [
    "Object-store cursor age",
    "Newest exported object age",
    "Object-store gaps clear",
    "Object listing complete",
    "Backlog & oldest pending object age",
    "Unresolved gaps & oldest gap age",
    "Objects & records ingested/s",
    "Object bytes read vs decompressed",
    "Objects skipped/s by reason",
    "Undecodable objects (broken feed)",
    "Expansion limit failures/s by limit",
    "Object retries/s",
    "Object-store provider requests/s",
    "Object-store provider latency p50/p95/p99",
    "Ingress WAL pending & retained entries",
    "Ingress WAL on-disk bytes",
    "Subrequest attempts/s",
    "Subrequest failures/s by API state",
    "Subrequest coverage (last pass)",
    "Capability state by collector",
    "OAuth scope preflight by capability",
    "Rate-limiter wait p50/p95/p99 by endpoint",
    "Time waiting vs time on the wire",
    "Requests delayed by the rate limiter",
    "API operation last-probe age",
    "Stream records accepted vs skipped/s",
    "Receiver rejections/s by reason",
    "Webhook request duration p50/p95/p99",
    "Webhook events accepted vs duplicates/s",
    "rDNS cache overflows vs lookups/s",
    "Forwarded metric-name drops/s by reason",
    "Metric names dropped over the window",
]

# Phrasings that state a reason for absence as fact. An empty panel cannot tell
# "feature off" from "feature unsupported on this tailnet" from "never deployed",
# so any of these renders a guess as an answer.
CAUSE_ASSERTIONS = ("is disabled", "is off", "not deployed", "is not enabled",
                    "means the feature", "because ")

# The bounded attribute keys the object-store family is allowed to group by.
# Everything else in that family is deliberately attribute-free so no object key,
# bucket name, endpoint or credential can reach a label (#399).
OBJECTSTORE_GROUP_LABELS = {"reason", "limit", "operation", "outcome", "le"}


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


class _TabDocs(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.docs = {name: dashboard.build(dashboard.dashboards.HEALTH, True, only=name)
                    for name in (DIAGNOSTICS, CARDINALITY)}
        cls.exprs = {name: all_exprs(doc) for (name, doc) in cls.docs.items()}
        cls.panels = {name: panels(doc) for (name, doc) in cls.docs.items()}


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
        self.assertGreater(len(self.exprs[DIAGNOSTICS]), 50)
        self.assertGreater(len(self.exprs[CARDINALITY]), 10)
        for tab in (DIAGNOSTICS, CARDINALITY):
            self.assertNotIn(
                "tailscale2otel_objectstore_metric_that_does_not_exist",
                " ".join(self.exprs[tab]),
                "the scan matches a metric name that is not in any panel — "
                "the extraction is returning something other than panel queries")


class EmptyStateTest(_TabDocs):
    def _spec(self, title):
        for tab in (DIAGNOSTICS, CARDINALITY):
            if title in self.panels[tab]:
                return self.panels[tab][title]
        self.fail("no panel titled %r on either tab" % title)

    def test_optional_sections_carry_a_prerequisite_aware_empty_state(self):
        for title in EMPTY_STATE_PANELS:
            with self.subTest(panel=title):
                text = novalue(self._spec(title))
                self.assertTrue(text, "panel %r has no noValue empty state" % title)
                self.assertIn("Requires ", text,
                              "panel %r's empty state does not name its prerequisite" % title)

    def test_empty_states_never_assert_a_cause(self):
        for title in EMPTY_STATE_PANELS:
            text = (novalue(self._spec(title)) or "").lower()
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
        for expr in self.exprs[DIAGNOSTICS]:
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
        spec = self.panels[DIAGNOSTICS]["Newest exported object age"]
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
            ("Webhook events accepted vs duplicates/s",
             "tailscale_webhook_duplicates_total", "tailscale_webhook_events_total"),
            ("Forwarded metric-name drops/s by reason",
             "tailscale2otel_nodemetrics_metric_names_dropped_total",
             "tailscale2otel_series_by_group"),
        ):
            with self.subTest(panel=title):
                tab = CARDINALITY if title.startswith("Forwarded") else DIAGNOSTICS
                joined = " ".join(panel_exprs(self.panels[tab][title]))
                self.assertIn(loss, joined)
                self.assertIn(accepted, joined)


class RateLimiterSeparationTest(_TabDocs):
    def test_rate_limiter_wait_is_not_mixed_into_the_api_latency_panel(self):
        # Rate-limiter wait and upstream API latency are different faults, and
        # api.duration deliberately excludes the wait. Charting them as one series
        # would make a throttled poller look like a slow tailnet.
        latency = " ".join(panel_exprs(
            self.panels[DIAGNOSTICS]["API latency p50/p95/p99 by endpoint"]))
        self.assertNotIn("rate_limit_wait", latency)
        quantiles = " ".join(panel_exprs(
            self.panels[DIAGNOSTICS]["Rate-limiter wait p50/p95/p99 by endpoint"]))
        self.assertNotIn("tailscale2otel_api_duration_seconds", quantiles)

    def test_wait_versus_wire_panel_charts_both_budgets(self):
        expr = " ".join(panel_exprs(
            self.panels[DIAGNOSTICS]["Time waiting vs time on the wire"]))
        self.assertIn("tailscale2otel_api_rate_limit_wait_seconds_sum", expr)
        self.assertIn("tailscale2otel_api_duration_seconds_sum", expr)


if __name__ == "__main__":
    unittest.main()
