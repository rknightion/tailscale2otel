#!/usr/bin/env python3
"""Tests for the documented-command checker (#445)."""

import unittest

import check_doc_commands as doc


class SchemaTest(unittest.TestCase):
    def setUp(self):
        self.known, self.open_prefixes = doc.load_schema_paths()

    def test_the_schema_actually_loaded(self):
        # Guards the guard: an empty set here would make every path below
        # "unknown", or — worse, if the check were inverted — every path known.
        self.assertGreater(len(self.known), 50)

    def test_the_real_chart_keys_resolve(self):
        for path in ("secret", "config", "config.tailscale.tailnet", "image.tag",
                     "persistence.enabled", "rolloutTrigger"):
            self.assertIn(path, self.known, "%s should be a real chart value" % path)

    def test_the_readme_typo_does_not_resolve(self):
        """The specific defect: `secrets` plural, against a chart whose key is `secret`.

        Helm accepts an unknown --set path silently, so the documented quick
        start installed the chart with no credentials at all and the failure
        surfaced as an authentication error rather than a typo.
        """
        self.assertNotIn("secrets", self.known)
        self.assertNotIn("secrets.TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID", self.known)

    def test_free_form_maps_are_open_not_unknown(self):
        # podAnnotations takes user-chosen keys. Treating them as unknown would
        # make the checker reject correct documentation.
        self.assertIn("podAnnotations", self.open_prefixes)


class ExtractionTest(unittest.TestCase):
    def test_it_finds_set_and_set_string_in_several_shapes(self):
        body = """
        helm install x oci://y \\
          --set-string config.tailscale.tailnet=example.com \\
          --set persistence.enabled=true \\
          --set "secret.TS2OTEL_X=abc" \\
          --set='image.tag=1.2.3'
        """
        keys = doc.SET_RE.findall(body)
        self.assertIn("config.tailscale.tailnet", keys)
        self.assertIn("persistence.enabled", keys)
        self.assertIn("secret.TS2OTEL_X", keys)
        self.assertIn("image.tag", keys)

    def test_it_finds_docker_env_flags(self):
        body = 'docker run -e TS2OTEL_TAILSCALE__TAILNET=a --env TS2OTEL_LOG_LEVEL=debug img'
        found = doc.ENV_RE.findall(body)
        self.assertEqual(sorted(found), ["TS2OTEL_LOG_LEVEL", "TS2OTEL_TAILSCALE__TAILNET"])

    def test_it_reads_indented_mkdocs_fences(self):
        """docs/installation.md puts its fences inside four-space-indented tabs.

        A column-zero fence matcher finds nothing there — in the single file that
        carries the most install commands — and reports a clean pass.
        """
        body = '    ```sh\n    helm install x --set-string config.tailscale.tailnet=e.com\n    ```'
        self.assertIn("config.tailscale.tailnet", doc.SET_RE.findall(body))


class EnvReferenceTest(unittest.TestCase):
    def test_the_env_reference_parses(self):
        known = doc.load_known_env()
        self.assertGreater(len(known), 100)
        self.assertIn("TS2OTEL_LOG_LEVEL", known)
        self.assertIn("TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET", known)

    def test_a_plausible_typo_is_not_in_the_reference(self):
        known = doc.load_known_env()
        # Single underscore where the convention requires a double: exactly the
        # mistake the TS2OTEL_ naming rule invites, and one the config loader
        # ignores in silence.
        self.assertNotIn("TS2OTEL_TAILSCALE_TAILNET", known)


if __name__ == "__main__":
    unittest.main()
