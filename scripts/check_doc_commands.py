#!/usr/bin/env python3
"""Validate the install commands in README.md and docs/ against the real schemas (#445).

WHY THIS EXISTS. The install commands in the README are the first thing anyone
runs, and nothing checked them. `deploy/helm/tests/render-tests.sh` renders the
chart from `--set` flags constructed inside the script, so it proves the chart
works — it never reads the command a user would actually copy. CI runs no command
taken from any documentation file.

The failure that motivated it had been sitting in the README, verbatim, for
months:

    --set-string secrets.TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID=<client-id>

The chart's key is `secret`, singular. Helm accepts unknown `--set` paths
silently, so the documented quick start installs the chart with NO credentials
and the exporter starts, fails to authenticate, and reports an auth error that
looks like a bad OAuth client rather than a typo in the docs.

WHAT IT CHECKS, and why these two specifically. Both are cases where the WRONG
value is accepted silently and only surfaces as a confusing runtime symptom:

  1. Every `--set`/`--set-string` key in a documented `helm` command resolves
     against `values.schema.json` — the generated, authoritative description of
     what the chart accepts.
  2. Every `TS2OTEL_*` variable in a documented `docker run -e` resolves against
     `docs/env-vars.md` — the generated reference of every real variable. A
     mistyped variable is simply ignored by the config loader, so the exporter
     starts with a default nobody intended.

WHAT IT DOES NOT DO. It does not execute anything. Running `helm install` or
`docker run` from CI would need a cluster or a registry pull, and — the reason
the issue calls it out — must never touch a real control plane. Checking the
commands against the schemas catches the class of error that actually occurs
(a key that does not exist) without any of that.

Usage:
    scripts/check_doc_commands.py            # check README.md + docs/*.md
    scripts/check_doc_commands.py --list     # print what was extracted, then check

Exit codes:
    0  every documented key resolves
    1  a documented command references something that does not exist
    2  a schema this depends on could not be read
"""

import argparse
import glob
import json
import os
import re
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SCHEMA = os.path.join(REPO, "deploy", "helm", "tailscale2otel", "values.schema.json")
ENV_DOC = os.path.join(REPO, "docs", "env-vars.md")

# --set/--set-string flags, quoted or bare, up to the '='.
SET_RE = re.compile(r"--set(?:-string)?[= ]+[\"']?([A-Za-z0-9_.\[\]-]+)=")
# -e / --env NAME=..., restricted to this project's prefix.
ENV_RE = re.compile(r"(?:-e|--env)[= ]+[\"']?(TS2OTEL_[A-Z0-9_]+)")
# Env-var reference rows: | `TS2OTEL_FOO` | ... |
ENV_DOC_RE = re.compile(r"^\|\s*`(TS2OTEL_[A-Z0-9_]+)`\s*\|", re.M)


def die(msg):
    print("check_doc_commands: %s" % msg, file=sys.stderr)
    raise SystemExit(2)


def load_schema_paths():
    """Every dotted value path the chart's schema declares.

    Object nodes with no declared `properties` (free-form maps such as
    `podAnnotations`) accept anything, so they are recorded as open prefixes —
    validating a user-chosen key inside one would be wrong.
    """
    try:
        with open(SCHEMA, encoding="utf-8") as f:
            doc = json.load(f)
    except (OSError, ValueError) as exc:
        die("cannot read %s: %s (run scripts/regen-generated.sh helm)" % (SCHEMA, exc))

    known, open_prefixes = set(), set()

    def walk(node, prefix):
        if not isinstance(node, dict):
            return
        props = node.get("properties")
        if node.get("type") == "object" and not props:
            if prefix:
                open_prefixes.add(prefix)
            return
        for name, child in (props or {}).items():
            path = "%s.%s" % (prefix, name) if prefix else name
            known.add(path)
            walk(child, path)
        items = node.get("items")
        if isinstance(items, dict) and prefix:
            open_prefixes.add(prefix)

    walk(doc, "")
    return known, open_prefixes


def load_known_env():
    try:
        with open(ENV_DOC, encoding="utf-8") as f:
            body = f.read()
    except OSError as exc:
        die("cannot read %s: %s" % (ENV_DOC, exc))
    names = set(ENV_DOC_RE.findall(body))
    if not names:
        die("%s lists no TS2OTEL_* variables; the reference table may have changed shape, "
            "and an empty set here would make every check below pass vacuously" % ENV_DOC)
    return names


def doc_files():
    files = [os.path.join(REPO, "README.md")]
    files += sorted(glob.glob(os.path.join(REPO, "docs", "*.md")))
    return files


def extract(path):
    """(helm --set keys, docker -e vars) referenced anywhere in a document.

    Deliberately scans the whole file rather than only fenced blocks: docs/ uses
    MkDocs tabbed sections whose fences are indented four spaces, and a
    column-zero fence matcher silently finds nothing in exactly the file that
    carries the most install commands.
    """
    with open(path, encoding="utf-8") as f:
        body = f.read()
    return SET_RE.findall(body), ENV_RE.findall(body)


def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--list", action="store_true", help="print everything extracted")
    args = ap.parse_args()

    known_paths, open_prefixes = load_schema_paths()
    known_env = load_known_env()

    problems, checked = [], 0
    for path in doc_files():
        rel = os.path.relpath(path, REPO)
        set_keys, env_vars = extract(path)
        for key in set_keys:
            checked += 1
            # Strip list indices; the schema describes the element, not the slot.
            clean = re.sub(r"\[\d+\]", "", key)
            if clean in known_paths:
                continue
            if any(clean == p or clean.startswith(p + ".") for p in open_prefixes):
                continue
            problems.append((rel, "--set %s" % key,
                             "no such chart value; helm accepts unknown --set paths "
                             "SILENTLY, so this documented command installs without it"))
        for var in env_vars:
            checked += 1
            if var not in known_env:
                problems.append((rel, "-e %s" % var,
                                 "not a real TS2OTEL_* variable; the config loader ignores "
                                 "unknown variables, so this silently takes the default"))
        if args.list and (set_keys or env_vars):
            print("%s: %d --set keys, %d env vars" % (rel, len(set_keys), len(env_vars)))

    print("checked %d documented keys across %d files" % (checked, len(doc_files())))
    if not checked:
        print("check_doc_commands: extracted nothing at all — the extractor is broken or the "
              "docs no longer carry install commands, either way this is not a pass",
              file=sys.stderr)
        return 1

    for rel, what, why in problems:
        print("  %s: %s — %s" % (rel, what, why), file=sys.stderr)
    if problems:
        print("\n%d documented command(s) reference something that does not exist."
              % len(problems), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
