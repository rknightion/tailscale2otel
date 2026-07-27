#!/usr/bin/env python3
"""Post-deployment smoke verification: compare what is DEPLOYED with what this repo ships.

Read-only by default and by design. It never pushes, never mutates, and never prints
tenant data — see "Sanitization" below. `gcx resources push` is additive, so nothing in
the normal workflow ever deletes a rule that was removed from the repo: an orphan keeps
evaluating on the stack forever, firing against an expression nobody maintains. Finding
those is the main reason this exists.

Usage:
    python3 scripts/verify_deployment.py                 # verify against the current gcx context
    python3 scripts/verify_deployment.py --json          # machine-readable summary
    python3 scripts/verify_deployment.py --keep <dir>    # retain the sanitized pull for inspection

Exit codes:
    0  deployed state matches the repo
    1  drift found (missing, orphaned, or differing rules)
    2  could not talk to Grafana (gcx missing, not logged in, offline)

Requires `gcx` on PATH and an authenticated context (`gcx config check`). Only stdlib
Python is used; there is no network access except through gcx itself.

Sanitization. The pulled resources are written to a temporary directory that is deleted
unless --keep is passed, and only rule NAMES (the repo's own `ts2o-*` uids), counts, and
field names are ever printed. Rule expressions, annotations, folder UIDs, datasource UIDs
and every other pulled value stay out of stdout, because this output is meant to be safe
to paste into an issue. --keep writes the same sanitized comparison, not the raw pull.
"""

import argparse
import glob
import json
import os
import shutil
import subprocess
import sys
import tempfile

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SHIPPED = os.path.join(REPO, "deploy", "alerts", "grafana-managed")

# The repo's rules are all prefixed. Anything else on the stack belongs to another pack
# and is none of our business — an orphan check that flagged unrelated rules would be
# noise, and worse, would tempt someone into deleting someone else's alerting.
PREFIX = "ts2o-"

# Fields worth comparing between deployed and shipped. Deliberately NOT the whole spec:
# Grafana adds server-side fields (uid, version, timestamps, provenance) that differ on
# every pull and would make everything look drifted. These are the ones that change
# BEHAVIOUR, which is what a smoke check is for.
COMPARED = ("title", "noDataState", "execErrState", "for", "paused", "labels")


def die(msg, code=2):
    print("verify_deployment: %s" % msg, file=sys.stderr)
    raise SystemExit(code)


def run(cmd, cwd=None):
    try:
        return subprocess.run(cmd, cwd=cwd, capture_output=True, text=True)
    except FileNotFoundError:
        die("`%s` not found on PATH" % cmd[0])
        raise  # unreachable; die() exits, but keeps control flow explicit


def check_gcx():
    if shutil.which("gcx") is None:
        die("gcx is not installed — see docs for the deployment workflow")
    p = run(["gcx", "config", "check"])
    if p.returncode != 0 or "Connectivity: online" not in p.stdout:
        die("gcx cannot reach Grafana (not logged in, or offline):\n%s" % (p.stdout or p.stderr).strip())
    ctx = run(["gcx", "config", "current-context"])
    name = "?"
    for line in ctx.stdout.splitlines():
        try:
            name = json.loads(line).get("current-context", name)
        except ValueError:
            continue
    return name


def load_shipped():
    out = {}
    for path in glob.glob(os.path.join(SHIPPED, "*.json")):
        with open(path, encoding="utf-8") as f:
            doc = json.load(f)
        if doc.get("kind") not in ("AlertRule", "RecordingRule"):
            continue  # the folder manifest
        out[doc["metadata"]["name"]] = doc
    if not out:
        die("no shipped manifests under %s — run scripts/regen-generated.sh" % SHIPPED, code=2)
    return out


def pull_deployed(workdir):
    p = run(["gcx", "resources", "pull", "alertrules"], cwd=workdir)
    if p.returncode != 0:
        die("gcx resources pull failed:\n%s" % (p.stderr or p.stdout).strip())
    found = {}
    for path in glob.glob(os.path.join(workdir, "resources", "**", "*.json"), recursive=True):
        try:
            with open(path, encoding="utf-8") as f:
                doc = json.load(f)
        except (OSError, ValueError):
            continue
        name = (doc.get("metadata") or {}).get("name", "")
        if name.startswith(PREFIX):
            # A rule can appear under more than one API version; keep the one whose
            # apiVersion matches what this repo emits, so the comparison is like-for-like.
            prev = found.get(name)
            if prev is None or "v0alpha1" in doc.get("apiVersion", ""):
                found[name] = doc
    return found


def compare_specs(shipped, deployed):
    """Field names that differ, for the fields that change behaviour."""
    a, b = shipped.get("spec", {}), deployed.get("spec", {})
    return [f for f in COMPARED if f in a and a.get(f) != b.get(f)]


def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--json", action="store_true", help="emit a machine-readable summary")
    ap.add_argument("--keep", metavar="DIR", help="retain the sanitized comparison in DIR")
    args = ap.parse_args()

    ctx = check_gcx()
    shipped = load_shipped()

    workdir = tempfile.mkdtemp(prefix="ts2o-verify-")
    try:
        deployed = pull_deployed(workdir)
    finally:
        if not args.keep:
            shutil.rmtree(workdir, ignore_errors=True)

    missing = sorted(set(shipped) - set(deployed))
    orphaned = sorted(set(deployed) - set(shipped))
    drifted = {}
    for name in sorted(set(shipped) & set(deployed)):
        diff = compare_specs(shipped[name], deployed[name])
        if diff:
            drifted[name] = diff

    shipped_paused = sum(1 for d in shipped.values() if d["spec"].get("paused"))
    deployed_paused = sum(1 for d in deployed.values() if d["spec"].get("paused"))

    summary = {
        "context": ctx,
        "shipped": len(shipped),
        "deployed": len(deployed),
        "missing": missing,
        "orphaned": orphaned,
        "drifted": drifted,
        "paused_shipped": shipped_paused,
        "paused_deployed": deployed_paused,
    }

    if args.keep:
        os.makedirs(args.keep, exist_ok=True)
        with open(os.path.join(args.keep, "verify-summary.json"), "w", encoding="utf-8") as f:
            json.dump(summary, f, indent=2, sort_keys=True)
            f.write("\n")

    if args.json:
        json.dump(summary, sys.stdout, indent=2, sort_keys=True)
        sys.stdout.write("\n")
    else:
        print("context      : %s" % ctx)
        print("shipped      : %d rules (%d paused)" % (len(shipped), shipped_paused))
        print("deployed     : %d rules (%d paused)" % (len(deployed), deployed_paused))
        print("missing      : %d  (in repo, not on the stack — run `gcx resources push`)" % len(missing))
        for n in missing:
            print("               - %s" % n)
        print("orphaned     : %d  (on the stack, deleted from the repo — push will NOT remove these)"
              % len(orphaned))
        for n in orphaned:
            print("               - %s" % n)
        print("drifted      : %d  (same name, different behaviour)" % len(drifted))
        for n, fields in drifted.items():
            print("               - %s: %s" % (n, ", ".join(fields)))

    return 1 if (missing or orphaned or drifted) else 0


if __name__ == "__main__":
    raise SystemExit(main())
