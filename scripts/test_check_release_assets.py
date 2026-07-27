#!/usr/bin/env python3
"""Tests for the release completeness gate (#442).

The gate's whole value is that it fails on a release that already shipped and
looked fine, so the tests are built from the real asset lists of real releases —
read from the GitHub API when this was written — rather than from invented ones.
A gate validated only against a hand-written happy path would not have caught
v2.0.0, which is exactly the case it exists for.
"""

import unittest

import check_release_assets as gate


def complete(version):
    return gate.expected_assets(version)


# The exact 4 assets v2.0.0 shipped. The binaries job produced nothing at all
# because the Go module path had not been moved to /v2 before the tag was cut
# (#174), and the run still went green.
V2_0_0_ACTUAL = [
    "tailscale2otel-2.0.0.tgz",
    "tailscale2otel.cdx.json",
    "tailscale2otel.spdx.json",
    "THIRD_PARTY_NOTICES.md",
]


class ExpectedManifestTest(unittest.TestCase):
    def test_a_complete_release_is_seventeen_assets(self):
        # Five archives, five per-archive SBOMs, checksums + signature +
        # provenance, the chart, two chart SBOMs, and the notices file.
        self.assertEqual(len(complete("3.0.0")), 17)

    def test_every_archive_has_a_matching_sbom(self):
        names = complete("3.0.0")
        archives = [n for n in names if n.endswith((".tar.gz", ".zip"))]
        self.assertEqual(len(archives), 5)
        for a in archives:
            self.assertIn(a + ".sbom.json", names,
                          "%s has no SBOM; its absence is invisible until someone "
                          "needs provenance" % a)

    def test_the_manifest_is_version_scoped(self):
        # A gate that matched on a fixed version string would pass a release that
        # published the PREVIOUS version's archives.
        self.assertNotEqual(complete("3.0.0"), complete("3.1.0"))
        self.assertIn("tailscale2otel_3.1.0_SHA256SUMS", complete("3.1.0"))


class CheckTest(unittest.TestCase):
    def test_a_complete_release_passes(self):
        missing, unexpected = gate.check("3.0.0", complete("3.0.0"))
        self.assertEqual(missing, [])
        self.assertEqual(unexpected, [])

    def test_it_catches_the_v2_0_0_failure(self):
        """The real regression: the binaries job produced nothing."""
        missing, _ = gate.check("2.0.0", V2_0_0_ACTUAL)
        self.assertEqual(len(missing), 13, "expected all 13 binaries-job assets missing")
        self.assertIn("tailscale2otel_2.0.0_SHA256SUMS", missing)
        self.assertIn("tailscale2otel_2.0.0_linux_amd64.tar.gz", missing)

    def test_it_catches_the_v2_0_1_failure(self):
        """The subtle one: everything present except the provenance attestation.

        This shipped twice (v1.0.0 and v2.0.1) because the step was fail-soft and
        skipped silently. Sixteen of seventeen assets is the shape a completeness
        gate has to be able to see; a count-only check would not.
        """
        assets = [n for n in complete("2.0.1") if not n.endswith(".intoto.jsonl")]
        missing, unexpected = gate.check("2.0.1", assets)
        self.assertEqual(missing, ["tailscale2otel_2.0.1_SHA256SUMS.intoto.jsonl"])
        self.assertEqual(unexpected, [])

    def test_a_rename_shows_up_as_missing_plus_unexpected(self):
        assets = complete("3.0.0")
        assets = [n.replace("_SHA256SUMS", "_CHECKSUMS") if n.endswith("_SHA256SUMS") else n
                  for n in assets]
        missing, unexpected = gate.check("3.0.0", assets)
        self.assertIn("tailscale2otel_3.0.0_SHA256SUMS", missing)
        self.assertIn("tailscale2otel_3.0.0_CHECKSUMS", unexpected)

    def test_extra_assets_alone_do_not_fail(self):
        missing, unexpected = gate.check("3.0.0", complete("3.0.0") + ["EXTRA.txt"])
        self.assertEqual(missing, [], "a deliberate addition must not break a release")
        self.assertEqual(unexpected, ["EXTRA.txt"])

    def test_an_empty_release_is_not_silently_complete(self):
        missing, _ = gate.check("3.0.0", [])
        self.assertEqual(len(missing), 17,
                         "a release with no assets at all must report every one missing, "
                         "not pass vacuously")


if __name__ == "__main__":
    unittest.main()
