#!/usr/bin/env python3
"""Fail when documentation claims a surface is open that the code refuses (#331).

WHY THIS EXISTS. Three separate changes made the exporter's local surfaces fail
CLOSED — the admin status page (#227), the streaming receiver (#228) and the
Prometheus pull endpoint (#315) — and each left behind documentation, Compose
comments and chart values still describing the OLD behaviour: "leave the token
empty to keep the page open", "empty makes token auth a no-op", "empty = open (a
WARN fires)".

That drift is worse than an ordinary stale sentence. Every one of those lines
tells an operator to do the exact thing that now produces a hard HTTP 403, and
the symptom — a page or a scrape that answers 403 forever — reads as a broken
exporter rather than as a documented refusal. The audit found six such claims
across four files.

Nothing could have caught it. `check_doc_commands.py` checks that documented
COMMANDS reference real keys; the completeness tests check that every config key
is MENTIONED. Neither has any opinion on whether a sentence is true. This does
one narrow thing: it fails when a file asserts a security posture the code no
longer has.

WHAT IT IS NOT. This is a drift alarm over known-false phrasings, not a proof of
correctness — it cannot tell that a newly written sentence is accurate. Its value
is that each rule below is a claim that was actually shipped and actually wrong,
so re-introducing one is caught rather than re-discovered by an operator.

Deliberately not scanned: CHANGELOG.md and anything under .github/, which
describe what WAS true at a point in time and must not be rewritten to match
today's behaviour.
"""

import pathlib
import re
import sys

# Each rule is (compiled pattern, why this claim is wrong now). The explanation
# is printed on failure and is the whole value of the rule — a bare "forbidden
# phrase" message tells the next person nothing about what to write instead.
RULES = [
    (
        re.compile(r"admin page is open|status page is open\b(?!\s*only)", re.I),
        "the admin page is NOT open by default: with no admin.auth.token it is served only on a "
        "loopback bind and every other bind is refused with HTTP 403 (#227), and admin.listen "
        "itself now defaults to 127.0.0.1 (#314). Say what the loopback exception is.",
    ),
    (
        re.compile(r"(leave|left)\s+empty\s+to\s+keep\s+.{0,40}\bopen", re.I),
        "an empty token does not keep a surface open on a network-reachable bind — it makes it "
        "refuse every request with HTTP 403. Only a loopback bind stays usable without a token.",
    ),
    (
        re.compile(r"empty\s+leaves\s+.{0,40}\bopen", re.I),
        "same as above: an empty token is a refusal on any network-reachable bind, not an open "
        "surface with a warning.",
    ),
    (
        re.compile(r"empty\s*=\s*open", re.I),
        "the Prometheus /metrics endpoint refuses requests on a network-reachable bind with no "
        "token since #315. It is not open-with-a-WARN; it is 403 until you set "
        "prometheus.auth.token, bind to loopback, or set prometheus.auth.allow_unauthenticated.",
    ),
    (
        re.compile(r"token auth a no-?op", re.I),
        "an empty streaming token is not a no-op: the receiver fails closed and serves only a "
        "loopback streaming.listen, refusing everything else (#228).",
    ),
    (
        re.compile(r"serves every series unauthenticated with an empty token", re.I),
        "/metrics no longer serves anything unauthenticated on a network bind (#315). The reason "
        "to be careful about publishing it is the acknowledgement flag, not an open default.",
    ),
    (
        re.compile(r"status page also renders .{0,40}\bwarnings", re.I),
        "the status page does not render config warnings — ConfigSummary carries no warning field. "
        "Surfacing them is #319, still open. Point at the config_warnings metric and the startup "
        "log instead.",
    ),
]

# Files whose security claims are load-bearing: an operator reads them and acts.
SCAN_PREFIXES = ("docs/", "deploy/", "README.md")
SCAN_SUFFIXES = (".md", ".yaml", ".yml", ".gotmpl")
SKIP = ("CHANGELOG.md", "docs/superpowers/")


def should_scan(rel):
    """Report whether a repo-relative path is in the scanned set."""
    rel = rel.replace("\\", "/")
    if any(rel.startswith(s) or rel == s for s in SKIP):
        return False
    if not rel.endswith(SCAN_SUFFIXES):
        return False
    return any(rel.startswith(p) or rel == p for p in SCAN_PREFIXES)


# A claim routinely wraps across two lines, especially in YAML comments where
# it is broken at 100 columns. Matching one line at a time missed exactly that:
# values.yaml's "Empty leaves the / # status page open" went unflagged while the
# GENERATED chart README, which puts it on one line, was caught. Relying on the
# generated copy to catch the source is not a check.
_CONT = re.compile(r"\s*(?:#|//|--)?\s*")


def _window(lines, i):
    """This line joined with the next, comment markers and indentation removed."""
    joined = lines[i]
    if i + 1 < len(lines):
        joined = joined + " " + _CONT.sub(" ", lines[i + 1], count=1).strip()
    return joined


def scan_file(path, rel):
    """Return [(rel, lineno, why)] for every rule a line in path violates."""
    hits = []
    try:
        text = path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError):
        return hits
    lines = text.splitlines()
    for i in range(len(lines)):
        window = _window(lines, i)
        for pattern, why in RULES:
            if pattern.search(window):
                # Report once per rule per starting line, not once per window it
                # appears in, or a wrapped claim is reported twice.
                if i > 0 and pattern.search(_window(lines, i - 1)):
                    continue
                hits.append((rel, i + 1, why))
    return hits


def scan_repo(root):
    """Scan every in-scope file under root."""
    problems = []
    for path in sorted(root.rglob("*")):
        if not path.is_file():
            continue
        rel = str(path.relative_to(root))
        if not should_scan(rel):
            continue
        problems.extend(scan_file(path, rel))
    return problems


def main():
    root = pathlib.Path(__file__).resolve().parent.parent
    problems = scan_repo(root)
    scanned = sum(1 for p in root.rglob("*") if p.is_file() and should_scan(str(p.relative_to(root))))
    if scanned == 0:
        print("check_doc_security_claims: scanned nothing at all — the file selection is broken, "
              "and an empty scan is not a pass", file=sys.stderr)
        return 1
    for rel, lineno, why in problems:
        print("  %s:%d — %s" % (rel, lineno, why), file=sys.stderr)
    if problems:
        print("\n%d documentation claim(s) describe a surface as open that the code refuses."
              % len(problems), file=sys.stderr)
        return 1
    print("check_doc_security_claims: %d files scanned against %d rules, 0 stale security claims"
          % (scanned, len(RULES)))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
