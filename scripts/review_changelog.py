#!/usr/bin/env python3
"""Watch the Tailscale changelog for new observability surfaces (#446).

WHAT WAS ALREADY COVERED, AND IS NOT REDONE HERE. The OpenAPI half of #446 is
done. `internal/tsapi/contract/operation_dispositions.json` (#423) classifies all
90 published operations across every verb — not just the 18 consumed — into
consumed / implement / redundant / privacy-rejected / unstable / parked / write,
each with a mandatory note, and `TestOperationDispositionsInSync` fails on an
empty or unknown disposition. `api-drift.yml` runs the operation-set classifier
daily. A new API operation therefore cannot appear without forcing a recorded
decision, which is exactly what the issue asks for.

WHAT WAS NOT. The changelog is where Tailscale announces a capability BEFORE, or
without, an OpenAPI change — a new log destination, a new client metric, a
behaviour change in something already collected. Nothing watched it, so the only
way a new observability surface got noticed was somebody happening to read a blog
post.

THE DESIGN CONSTRAINT IS NOISE, NOT DETECTION. Fetching a changelog is trivial;
the hard part is that 287 entries about billing, the admin console and client UI
are not observability surfaces, and a lane that reports all of them gets muted
within two months. So two filters apply, and both must pass: an entry must match
a term drawn from what this exporter actually collects, and it must not already
carry a recorded verdict.

RECORDING NEGATIVE VERDICTS IS THE POINT. `spec/changelog-reviewed.json` stores a
verdict for every entry that has been looked at, INCLUDING the ones judged
irrelevant. Without that, every run re-proposes the same surface forever and the
lane becomes a monthly reminder to re-reach a conclusion already reached.

Usage:
    scripts/review_changelog.py --report /tmp/r.md     # fetch and review
    scripts/review_changelog.py --feed FILE            # offline, from a saved feed
    scripts/review_changelog.py --seed                 # record current entries as reviewed

Exit codes:
    0  nothing new to review
    2  the feed or the baseline could not be read
    3  new candidate surfaces found (the repository's drift-signal convention)
"""

import argparse
import json
import os
import re
import sys
import urllib.request
import xml.etree.ElementTree as ET
from typing import NoReturn

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BASELINE = os.path.join(REPO, "spec", "changelog-reviewed.json")
FEED_URL = "https://tailscale.com/changelog/index.xml"

# Terms drawn from what this exporter COLLECTS, not from general interest. An
# entry has to plausibly imply a signal we could emit or one we already emit
# changing shape. Deliberately narrow: the failure mode of a watch lane is being
# ignored, and breadth is what gets it ignored.
RELEVANT = (
    # Log and metric surfaces — the things that become signals directly.
    "audit log", "flow log", "log stream", "webhook",
    "metric", "prometheus", "opentelemetry", "otel",
    # Collected entities whose shape or availability could change.
    "posture", "tailnet lock", "acl", "session recording",
    "derp", "exit node", "subnet router", "app connector",
    "oauth", "api key", "device approval",
)
# Bare "api" is deliberately NOT a term, despite being the most frequent match in
# the archive (26 of 287). Every published API operation is already classified
# daily against operation_dispositions.json, which forces a recorded decision on
# each one — matching "api" here duplicates a stronger signal with a weaker one,
# and it was by some margin the largest source of irrelevant hits. Same reasoning
# retires "grant" and "access control" as too generic to imply a signal.
#
# Terms subsumed by a shorter one are omitted rather than listed twice:
# "device posture" ⊂ "posture", "log streaming" ⊂ "log stream",
# "network flow log" ⊂ "flow log". Listing both only inflates the match list
# shown on the issue.

VERDICTS = ("adopted", "parked", "rejected", "not-relevant", "pre-baseline")

TAG_RE = re.compile(r"<[^>]+>")


# The feed is fetched over the network, so it is untrusted input to an XML
# parser. defusedxml is not available (this repository is stdlib-only), but both
# attacks that matter here — external entity expansion and the billion-laughs
# amplification — require a document type declaration, so refusing any input that
# carries one closes both without a dependency. A legitimate RSS feed has no DTD.
MAX_FEED_BYTES = 8 * 1024 * 1024
DTD_RE = re.compile(rb"<!\s*(DOCTYPE|ENTITY)", re.I)


def die(msg) -> NoReturn:
    print("review_changelog: %s" % msg, file=sys.stderr)
    raise SystemExit(2)


def load_feed(source):
    if source and os.path.exists(source):
        with open(source, "rb") as f:
            raw = f.read(MAX_FEED_BYTES + 1)
    else:
        url = source or FEED_URL
        if not url.startswith("https://"):
            die("refusing to fetch a feed over a non-HTTPS URL: %s" % url)
        try:
            with urllib.request.urlopen(url, timeout=60) as resp:  # noqa: S310 - scheme checked above
                raw = resp.read(MAX_FEED_BYTES + 1)
        except Exception as exc:  # noqa: BLE001 - any transport failure is fatal here
            die("cannot fetch %s: %s" % (url, exc))
    if len(raw) > MAX_FEED_BYTES:
        die("feed exceeds %d bytes; refusing to parse" % MAX_FEED_BYTES)
    if DTD_RE.search(raw):
        die("feed carries a DOCTYPE or ENTITY declaration. A legitimate RSS feed has "
            "neither, and both are the prerequisite for entity-expansion attacks against "
            "the stdlib XML parser — refusing to parse it.")
    try:
        root = ET.fromstring(raw)
    except ET.ParseError as exc:
        die("feed is not parseable XML: %s" % exc)
    items = []
    for item in root.findall(".//item"):
        guid = (item.findtext("guid") or "").strip()
        title = (item.findtext("title") or "").strip()
        if not guid or not title:
            continue
        items.append({
            "guid": guid,
            "title": title,
            "link": (item.findtext("link") or "").strip(),
            "date": (item.findtext("pubDate") or "").strip(),
            "text": TAG_RE.sub(" ", item.findtext("description") or ""),
        })
    if not items:
        die("feed contained no usable entries; the format may have changed, and an empty "
            "result here would read as 'nothing new' forever")
    return items


def load_baseline():
    try:
        with open(BASELINE, encoding="utf-8") as f:
            doc = json.load(f)
    except FileNotFoundError:
        return {}
    except (OSError, ValueError) as exc:
        die("cannot read %s: %s" % (BASELINE, exc))
    reviewed = doc.get("reviewed", {})
    for guid, entry in reviewed.items():
        verdict = entry.get("verdict")
        if verdict not in VERDICTS:
            die("%s: entry %s has verdict %r, which is not one of %s. An unrecognised "
                "verdict must fail rather than be treated as reviewed."
                % (BASELINE, guid, verdict, ", ".join(VERDICTS)))
    return reviewed


def is_relevant(item):
    hay = (item["title"] + " " + item["text"]).lower()
    return sorted({term for term in RELEVANT if term in hay})


def review(items, reviewed):
    out = []
    for item in items:
        if item["guid"] in reviewed:
            continue
        hits = is_relevant(item)
        if not hits:
            continue
        out.append((item, hits))
    return out


def write_baseline(items, reviewed, verdict, note):
    for item in items:
        reviewed.setdefault(item["guid"], {
            "title": item["title"],
            "verdict": verdict,
            "note": note,
        })
    doc = {
        "//": [
            "Reviewed Tailscale changelog entries (#446).",
            "An entry listed here is NOT re-proposed. Recording the negative verdicts is the",
            "point: without them every run re-raises a surface already considered and declined.",
            "verdict: adopted | parked | rejected | not-relevant | pre-baseline",
            "  adopted      - a signal was or will be built; the note names the issue",
            "  parked       - plausible, deferred; no plan, no objection",
            "  rejected     - deliberately not observed; the note must say why",
            "  not-relevant - matched a keyword but implies no signal we could emit",
            "  pre-baseline - predates this lane; never individually reviewed",
        ],
        "reviewed": dict(sorted(reviewed.items())),
    }
    os.makedirs(os.path.dirname(BASELINE), exist_ok=True)
    with open(BASELINE, "w", encoding="utf-8") as f:
        json.dump(doc, f, indent=2, sort_keys=False)
        f.write("\n")
    return len(reviewed)


def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--feed", help="feed URL or a local file (default: the live feed)")
    ap.add_argument("--report", help="write a markdown report here when candidates are found")
    ap.add_argument("--seed", action="store_true",
                    help="record every current entry as reviewed and exit")
    args = ap.parse_args()

    items = load_feed(args.feed)
    reviewed = load_baseline()

    if args.seed:
        total = write_baseline(items, reviewed, "pre-baseline",
                               "predates the changelog review lane; not individually reviewed")
        print("baseline now records %d entries" % total)
        return 0

    candidates = review(items, reviewed)
    print("feed: %d entries, %d already reviewed, %d new candidate(s)"
          % (len(items), len(reviewed), len(candidates)))
    if not candidates:
        return 0

    lines = [
        "## New Tailscale changelog entries that may be observability surfaces",
        "",
        "%d entry(ies) match a term this exporter collects and carry no recorded verdict."
        % len(candidates),
        "",
        "**Reviewing one means recording a verdict**, including a negative one. Add it to",
        "`spec/changelog-reviewed.json` — an entry left out is re-proposed on the next run.",
        "",
    ]
    for item, hits in candidates:
        lines += [
            "### %s" % item["title"],
            "",
            "- guid: `%s`" % item["guid"],
            "- date: %s" % item["date"],
            "- link: %s" % item["link"],
            "- matched: %s" % ", ".join("`%s`" % h for h in hits),
            "",
            "> %s" % " ".join(item["text"].split())[:600],
            "",
        ]
    report = "\n".join(lines)
    if args.report:
        with open(args.report, "w", encoding="utf-8") as f:
            f.write(report)
    else:
        print(report)
    return 3


if __name__ == "__main__":
    raise SystemExit(main())
