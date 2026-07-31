#!/usr/bin/env python3
"""Regression tests for the sentinel (presence-variable) registry (#495).

Before #495 every has_*/pii_* presence variable was declared in one place
(variables.py's build_variables()), so two agents adding feature-gated rows to
different tabs collided on that same function. Sentinels are now declared by
the tab module that consumes them (builder.sentinel()/pii_sentinel()/
raw_sentinel()), which opens two new ways to get it wrong that these tests
guard against:

1. A row/tab references a sentinel name that was never registered (a typo) —
   the row/tab silently renders permanently hidden, which looks identical to
   a correctly-gated one. This is the higher-value direction: a dead sentinel
   wastes a Prometheus query on every dashboard load, but a typo'd reference
   hides a row forever with no visible symptom.
2. A sentinel is registered but no row/tab ever references it — a dead
   presence variable that queries Prometheus on every dashboard load for
   nothing.
3. Two call sites declare the same sentinel name — must raise, not silently
   dedupe (see builder._claim_sentinel's docstring for why).
"""

import importlib.util
from pathlib import Path
import unittest


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


dashboard = load_module("tailscale2otel_dashboard_sentinels", Path(__file__).with_name("build.py"))
builder = dashboard.builder


def referenced_sentinel_names(doc):
    """Every variable name appearing in a ConditionalRenderingVariable anywhere
    in the built document (row present=/hide_when=, tab present=)."""
    found = set()

    def walk(node):
        if isinstance(node, dict):
            if node.get("kind") == "ConditionalRenderingVariable":
                found.add(node["spec"]["variable"])
            for v in node.values():
                walk(v)
        elif isinstance(node, list):
            for v in node:
                walk(v)

    walk(doc)
    return found


# Pre-existing dead sentinels (#495 found these; they predate the registry and are a
# pure-move refactor away from being fixed here, not introduced by it). Do NOT add a
# name here for a NEWLY-added sentinel — an addition failing this test means the row
# gating it is either missing or misspelled.
KNOWN_DEAD_SENTINELS = {
    # has_unique and has_dropped left this list in #391/#402 — each now gates the row
    # whose panels query exactly the metric it names.
    # has_keys and has_svc left it in #393/#403 — each now gates the per-entity
    # detail row (policy.py "Key expiry detail" / "VIP service detail") whose
    # gauges the sentinel's metric is the presence check for.
    "has_posture_int",
    # The three IP-redaction sentinels stay dead on purpose: no panel anywhere renders
    # a raw address, so there is nothing for them to gate, and wiring them would mean
    # ADDING an address-rendering panel (see the note in tabs/network.py).
    "pii_host", "pii_int_ips", "pii_ext_ips", "pii_ts_ips",
    # pii_emails left this list in #462: tabs/k8saudit.py wires it into hide_when= on
    # every row that surfaces tailscale_k8s_user (a Kubernetes identity, typically an
    # email/login) — the first panel anywhere to actually render one.
}


def _per_dashboard():
    """(uid, registered names, referenced names) for each shipped dashboard.

    Per dashboard, not over the family: after the #526 split a sentinel is
    registered while building the dashboard that consumes it, so a row on the
    tailnet dashboard gating on a sentinel only the health dashboard's modules
    declare would render permanently hidden. Checking the union would hide
    exactly that.
    """
    out = []
    for spec in dashboard.dashboards.ALL:
        doc = dashboard.build(spec)
        out.append((spec.uid,
                    {name for (_scope, name) in builder._sentinels},
                    referenced_sentinel_names(doc)))
    return out


class SentinelRegistryTest(unittest.TestCase):
    def setUp(self):
        self.per_dashboard = _per_dashboard()

    def test_every_referenced_sentinel_is_registered(self):
        # The higher-value direction: a typo'd present=/hide_when= name must fail the
        # build, not silently produce an always-hidden row. Since #526 it also
        # catches a row left behind on one dashboard gating on a sentinel whose
        # declaring module moved to the other.
        for uid, registered, referenced in self.per_dashboard:
            undefined = referenced - registered
            self.assertEqual(undefined, set(),
                              "%s: row/tab references unregistered sentinel(s) %s — typo, "
                              "the declaring tab's sentinel() call is missing, or the "
                              "declaring module now lives on the other dashboard"
                              % (uid, sorted(undefined)))

    def test_every_registered_sentinel_is_referenced_or_known_dead(self):
        for uid, registered, referenced in self.per_dashboard:
            dead = registered - referenced
            unexpected_dead = dead - KNOWN_DEAD_SENTINELS
            self.assertEqual(unexpected_dead, set(),
                              "%s: sentinel(s) %s are registered but no row/tab references "
                              "them — either wire them into a present=/hide_when=, or add "
                              "them to KNOWN_DEAD_SENTINELS with a reason"
                              % (uid, sorted(unexpected_dead)))
            # If a KNOWN_DEAD entry starts being referenced, drop it from the allowlist
            # so this test keeps covering it going forward.
            newly_alive = KNOWN_DEAD_SENTINELS & referenced
            self.assertEqual(newly_alive, set(),
                              "%s: sentinel(s) %s are in KNOWN_DEAD_SENTINELS but ARE "
                              "referenced now — remove them from the allowlist"
                              % (uid, sorted(newly_alive)))


class ScopedSentinelTest(unittest.TestCase):
    """#526: a sentinel is registered against a SCOPE — the dashboard, or one leaf
    tab — so a presence variable consumed by exactly one tab stops being declared
    at dashboard level. 66 of the 82 dashboard-level variables were hidden
    sentinels, 50 of them consumed by rows in a single tab."""

    def setUp(self):
        builder.reset_sentinels()

    def tearDown(self):
        builder.reset_sentinels()

    def test_a_sentinel_is_returned_only_for_its_own_scope(self):
        # A name from the frozen _SENTINEL_ORDER: registered_sentinels() emits in
        # that order and rejects anything absent from it, so a made-up name here
        # would test the order guard rather than scoping.
        s = builder.tab_scope("Flows")
        builder.sentinel("has_flows", "tailscale_network_flows_total", s)
        self.assertEqual(
            [v["spec"]["name"] for v in builder.registered_sentinels(s)],
            ["has_flows"])
        self.assertEqual(builder.registered_sentinels(builder.DASHBOARD), [])

    def test_the_same_name_in_one_scope_twice_raises(self):
        s = builder.tab_scope("Flows")
        builder.sentinel("has_probe", "a_metric", s)
        with self.assertRaises(ValueError):
            builder.sentinel("has_probe", "another_metric", s)

    def test_the_same_name_in_two_tab_scopes_is_allowed(self):
        # The "<=2 tabs duplicates at tab scope" rule (#526) depends on this.
        # Whether GRAFANA accepts it is a separate, live-probed question; this
        # only fixes the generator's own behaviour so the probe has something to
        # push.
        a, b = builder.tab_scope("Flows"), builder.tab_scope("Devices")
        builder.sentinel("has_flows", "a_metric", a)
        builder.sentinel("has_flows", "a_metric", b)
        self.assertEqual(len(builder.registered_sentinels(a)), 1)
        self.assertEqual(len(builder.registered_sentinels(b)), 1)

    def test_the_same_name_at_dashboard_and_tab_scope_raises(self):
        # Ambiguous: a row referencing it would resolve to whichever declaration
        # Grafana finds first. Ban it rather than pick one.
        s = builder.tab_scope("Flows")
        builder.sentinel("has_probe", "a_metric", builder.DASHBOARD)
        with self.assertRaises(ValueError):
            builder.sentinel("has_probe", "a_metric", s)

    def test_tab_scope_is_stable_per_title(self):
        # Tab modules are re-executed on every build(); the scope token for a
        # given tab title must be the same object across calls or a rebuild would
        # scatter one tab's sentinels across two scopes.
        self.assertIs(builder.tab_scope("Flows"), builder.tab_scope("Flows"))
        self.assertIsNot(builder.tab_scope("Flows"), builder.tab_scope("Devices"))

    def test_scope_is_required_on_every_registrar(self):
        # No default on purpose: a default would let a tab module forget it and
        # silently re-create the dashboard-level pile #526 exists to remove.
        with self.assertRaises(TypeError):
            builder.sentinel("has_probe", "a_metric")
        with self.assertRaises(TypeError):
            builder.pii_sentinel("has_probe", "a_metric == 0")
        with self.assertRaises(TypeError):
            builder.raw_sentinel("has_probe", "query_result(x)")


class SentinelDuplicateRegistrationTest(unittest.TestCase):
    def tearDown(self):
        # A later test file's dashboard.build() call resets the registry anyway (build()
        # calls reset_sentinels() first), but leave no trace regardless.
        builder.reset_sentinels()

    def test_declaring_the_same_sentinel_name_twice_raises(self):
        builder.reset_sentinels()
        builder.sentinel("test_dup_sentinel", "some_metric", builder.DASHBOARD)
        with self.assertRaises(ValueError):
            builder.sentinel("test_dup_sentinel", "some_other_metric", builder.DASHBOARD)

    def test_declaring_the_same_sentinel_name_twice_raises_across_kinds(self):
        # The collision check is on the NAME, regardless of which registration
        # function is used — a pii_sentinel colliding with a sentinel() name is the
        # same bug (#414-equivalent) as two sentinel() calls colliding.
        builder.reset_sentinels()
        builder.sentinel("test_dup_sentinel2", "some_metric", builder.DASHBOARD)
        with self.assertRaises(ValueError):
            builder.pii_sentinel("test_dup_sentinel2", "some_expr == 0", builder.DASHBOARD)


if __name__ == "__main__":
    unittest.main()
