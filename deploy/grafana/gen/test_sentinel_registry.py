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
    "pii_host", "pii_emails", "pii_int_ips", "pii_ext_ips", "pii_ts_ips",
}


class SentinelRegistryTest(unittest.TestCase):
    def setUp(self):
        self.doc = dashboard.build("test", "test", False)
        self.registered = set(builder._sentinels.keys())
        self.referenced = referenced_sentinel_names(self.doc)

    def test_every_referenced_sentinel_is_registered(self):
        # The higher-value direction: a typo'd present=/hide_when= name must fail the
        # build, not silently produce an always-hidden row.
        undefined = self.referenced - self.registered
        self.assertEqual(undefined, set(),
                          "row/tab references unregistered sentinel(s) %s — typo, or the "
                          "declaring tab's sentinel() call is missing" % sorted(undefined))

    def test_every_registered_sentinel_is_referenced_or_known_dead(self):
        dead = self.registered - self.referenced
        unexpected_dead = dead - KNOWN_DEAD_SENTINELS
        self.assertEqual(unexpected_dead, set(),
                          "sentinel(s) %s are registered but no row/tab references them — "
                          "either wire them into a present=/hide_when=, or add them to "
                          "KNOWN_DEAD_SENTINELS with a reason" % sorted(unexpected_dead))
        # If a KNOWN_DEAD entry starts being referenced, drop it from the allowlist so
        # this test keeps covering it going forward.
        newly_alive = KNOWN_DEAD_SENTINELS & self.referenced
        self.assertEqual(newly_alive, set(),
                          "sentinel(s) %s are in KNOWN_DEAD_SENTINELS but ARE referenced now — "
                          "remove them from the allowlist" % sorted(newly_alive))


class SentinelDuplicateRegistrationTest(unittest.TestCase):
    def tearDown(self):
        # A later test file's dashboard.build() call resets the registry anyway (build()
        # calls reset_sentinels() first), but leave no trace regardless.
        builder.reset_sentinels()

    def test_declaring_the_same_sentinel_name_twice_raises(self):
        builder.reset_sentinels()
        builder.sentinel("test_dup_sentinel", "some_metric")
        with self.assertRaises(ValueError):
            builder.sentinel("test_dup_sentinel", "some_other_metric")

    def test_declaring_the_same_sentinel_name_twice_raises_across_kinds(self):
        # The collision check is on the NAME, regardless of which registration
        # function is used — a pii_sentinel colliding with a sentinel() name is the
        # same bug (#414-equivalent) as two sentinel() calls colliding.
        builder.reset_sentinels()
        builder.sentinel("test_dup_sentinel2", "some_metric")
        with self.assertRaises(ValueError):
            builder.pii_sentinel("test_dup_sentinel2", "some_expr == 0")


if __name__ == "__main__":
    unittest.main()
