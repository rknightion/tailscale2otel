#!/usr/bin/env python3
"""Post-publish completeness gate for a GitHub release (#442).

WHY THIS EXISTS. The release is visible before its assets are. release-please
creates the GitHub Release immediately (`.goreleaser.yaml` sets `draft: false`),
and three independent actors then attach files to it in parallel with no ordering
between them: the container-publish reusable, the notices job, and the binaries
reusable. Nothing waits for the others and nothing reads the result back, so for
several minutes the release is public and incomplete — and if one actor fails
outright, it stays that way forever with a green-looking workflow.

That is not hypothetical. Two published releases are permanently incomplete:

  * v2.0.0 has NO archives, checksums, signature or provenance at all. The Go
    module path had not been moved to /v2 before the tag was cut, so GoReleaser's
    module-proxy rebuild failed and the whole binaries job produced nothing
    (#174). Four assets shipped instead of seventeen.
  * v2.0.1 and v1.0.0 are each missing SHA256SUMS.intoto.jsonl, because the
    provenance step was fail-soft and skipped silently (#176).

In every case the run went green. Nothing compared what was published against
what should have been.

WHAT IT CANNOT DO. It cannot make the release atomic. GitHub has no "publish all
assets or none" primitive, and the jobs that upload live in another repository
(rknightion/.github), so ordering them is not in this repo's gift. This closes
the half that is: after the uploads settle, read the release back and say loudly
whether it is complete. An incomplete release then fails a workflow instead of
sitting unnoticed for two years.

Usage:
    scripts/check_release_assets.py --version 3.0.0 --tag v3.0.0
    scripts/check_release_assets.py --version 3.0.0 --names-from FILE   # offline

Exit codes:
    0  every expected asset is present
    1  the release is incomplete (or extra assets suggest a naming change)
    2  could not read the release
"""

import argparse
import json
import subprocess
import sys

# The five build targets GoReleaser produces. Derived from what v3.0.0 and
# v2.0.2 — the two releases the current pipeline produced completely — actually
# contain, NOT from the union of everything ever published: an older era's asset
# set would make a correct release look like it was missing things.
PLATFORMS = (
    ("linux_amd64", "tar.gz"),
    ("linux_arm64", "tar.gz"),
    ("darwin_amd64", "tar.gz"),
    ("darwin_arm64", "tar.gz"),
    ("windows_amd64", "zip"),
)


def expected_assets(version):
    """Every asset a complete release of `version` must carry (17 of them)."""
    stem = "tailscale2otel_%s" % version
    names = []
    for platform, ext in PLATFORMS:
        archive = "%s_%s.%s" % (stem, platform, ext)
        names.append(archive)
        # Per-archive SBOM. Its absence is the quiet one: the archive downloads
        # and runs fine, so nothing surfaces until someone needs provenance.
        names.append(archive + ".sbom.json")
    names += [
        "%s_SHA256SUMS" % stem,
        # cosign emits a self-contained bundle, not separate .sig/.pem files.
        "%s_SHA256SUMS.sigstore.json" % stem,
        # The one that has silently gone missing twice.
        "%s_SHA256SUMS.intoto.jsonl" % stem,
        "tailscale2otel-%s.tgz" % version,  # the Helm chart
        "tailscale2otel.cdx.json",
        "tailscale2otel.spdx.json",
        "THIRD_PARTY_NOTICES.md",
    ]
    return names


def fetch_asset_names(tag):
    try:
        p = subprocess.run(
            ["gh", "release", "view", tag, "--json", "assets"],
            capture_output=True, text=True, check=False,
        )
    except FileNotFoundError:
        print("check_release_assets: gh is not on PATH", file=sys.stderr)
        raise SystemExit(2)
    if p.returncode != 0:
        print("check_release_assets: cannot read release %s:\n%s"
              % (tag, (p.stderr or p.stdout).strip()), file=sys.stderr)
        raise SystemExit(2)
    try:
        return [a["name"] for a in json.loads(p.stdout).get("assets", [])]
    except (ValueError, KeyError, TypeError) as exc:
        print("check_release_assets: unreadable response for %s: %s" % (tag, exc),
              file=sys.stderr)
        raise SystemExit(2)


def check(version, present):
    """Return (missing, unexpected) for a release of `version`."""
    want = expected_assets(version)
    have = set(present)
    missing = [n for n in want if n not in have]
    unexpected = sorted(have - set(want))
    return missing, unexpected


def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--version", required=True, help="release version without the leading v")
    ap.add_argument("--tag", help="git tag to read back (default: v<version>)")
    ap.add_argument("--names-from", metavar="FILE",
                    help="read asset names from a file, one per line, instead of calling gh")
    args = ap.parse_args()

    if args.names_from:
        with open(args.names_from, encoding="utf-8") as f:
            present = [line.strip() for line in f if line.strip()]
    else:
        present = fetch_asset_names(args.tag or "v" + args.version)

    missing, unexpected = check(args.version, present)

    print("release %s: %d assets present, %d expected"
          % (args.version, len(present), len(expected_assets(args.version))))
    for name in missing:
        print("  MISSING   %s" % name)
    # Extra assets do not fail the gate — a deliberate addition should not break
    # a release — but they are reported, because the usual cause is a RENAME,
    # which shows up here as one missing plus one unexpected.
    for name in unexpected:
        print("  unexpected %s" % name)

    if missing:
        print("\nrelease %s is INCOMPLETE. Do not leave it published in this state: the "
              "asset set is what users verify downloads against." % args.version,
              file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
