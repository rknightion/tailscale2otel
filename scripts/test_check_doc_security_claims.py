#!/usr/bin/env python3
"""Tests for check_doc_security_claims."""

import pathlib
import sys
import tempfile
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import check_doc_security_claims as mod  # noqa: E402


class TestScan(unittest.TestCase):
    def _scan(self, name, text):
        # The file lives flat in the temp dir; `name` is only the repo-relative
        # label scan_file reports back.
        with tempfile.TemporaryDirectory() as d:
            p = pathlib.Path(d) / pathlib.PurePosixPath(name).name
            p.write_text(text, encoding="utf-8")
            return mod.scan_file(p, name)

    def test_flags_admin_page_described_as_open_by_default(self):
        hits = self._scan(
            "docs/getting-started.md",
            "By default the admin page is open (no token required), so bind carefully.\n",
        )
        self.assertTrue(hits, "an 'open by default' admin claim was not flagged")
        self.assertIn("403", hits[0][2])

    def test_flags_empty_token_leaves_it_open(self):
        for text in (
            "Leave empty to keep the status page open; required if you enable pprof.\n",
            "Empty leaves the status page open (a WARN fires on a wildcard bind).\n",
            "Empty = open (a WARN fires when prometheus.enabled).\n",
        ):
            with self.subTest(text=text):
                self.assertTrue(self._scan("deploy/x.yaml", text),
                                "an empty-token-is-open claim was not flagged")

    def test_flags_streaming_token_described_as_a_no_op(self):
        hits = self._scan("deploy/x.yaml", "Empty makes streaming token auth a no-op.\n")
        self.assertTrue(hits, "the streaming no-op claim was not flagged")

    def test_flags_warn_only_framing_for_a_refusal(self):
        hits = self._scan(
            "deploy/x.yaml",
            "prometheus serves every series unauthenticated with an empty token\n",
        )
        self.assertTrue(hits, "an unauthenticated-serving claim was not flagged")

    def test_accurate_text_is_not_flagged(self):
        # The corrected wording for every rule above. A guard that also fires on
        # the FIX is a guard nobody can satisfy.
        ok = (
            "With no token the status page is served only on a loopback bind; any other "
            "bind is refused with HTTP 403.\n"
            "An empty streaming token is accepted only on a loopback listen; network binds "
            "refuse every request.\n"
            "/metrics refuses requests on a network-reachable bind unless a token is set or "
            "prometheus.auth.allow_unauthenticated acknowledges the exposure.\n"
            "/healthz and /readyz are never gated and stay open.\n"
        )
        self.assertEqual(self._scan("docs/security.md", ok), [])

    def test_ignores_files_outside_the_scanned_set(self):
        # CHANGELOG entries describe what WAS true at the time and must not be
        # rewritten by a docs guard.
        self.assertFalse(mod.should_scan("CHANGELOG.md"))
        self.assertTrue(mod.should_scan("docs/getting-started.md"))
        self.assertTrue(mod.should_scan("deploy/docker-compose.yaml"))
        self.assertTrue(mod.should_scan("deploy/helm/tailscale2otel/values.yaml"))

    def test_rules_are_non_empty_and_explained(self):
        # A rule with no explanation is a failure message nobody can act on.
        self.assertTrue(mod.RULES)
        for pattern, why in mod.RULES:
            self.assertTrue(pattern.pattern, "empty pattern")
            self.assertGreater(len(why), 30, "rule %r has no usable explanation" % pattern.pattern)


class TestRepository(unittest.TestCase):
    def test_repository_is_clean(self):
        root = pathlib.Path(__file__).resolve().parent.parent
        problems = mod.scan_repo(root)
        self.assertEqual(
            problems, [],
            "documentation claims a surface is open when the code refuses it:\n"
            + "\n".join("  %s:%s: %s" % (p[0], p[1], p[2]) for p in problems),
        )


class TestWrappedClaims(unittest.TestCase):
    """A claim broken across two comment lines must still be caught.

    This is not hypothetical: values.yaml wrapped "Empty leaves the / # status
    page open" and only the GENERATED chart README, which puts it on one line,
    was flagged by the first version of this scanner.
    """

    def _scan(self, text):
        with tempfile.TemporaryDirectory() as d:
            p = pathlib.Path(d) / "values.yaml"
            p.write_text(text, encoding="utf-8")
            return mod.scan_file(p, "deploy/helm/tailscale2otel/values.yaml")

    def test_flags_a_claim_wrapped_across_two_comment_lines(self):
        hits = self._scan("  # -- Shared token. Empty leaves the\n  # status page open (a WARN fires).\n")
        self.assertTrue(hits, "a wrapped claim was not flagged")

    def test_reports_a_wrapped_claim_once(self):
        hits = self._scan("  # -- Shared token. Empty leaves the\n  # status page open (a WARN fires).\n")
        self.assertEqual(len(hits), 1, "a wrapped claim was reported %d times: %r" % (len(hits), hits))


if __name__ == "__main__":
    unittest.main()
