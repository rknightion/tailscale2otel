#!/usr/bin/env python3
"""Delete alert/recording rules on a Grafana stack that this repo no longer defines.

`gcx resources push` is create-or-update only: it never deletes. Without this step a
rule dropped from the generator keeps evaluating and paging forever, and nothing in CI
says so. This closes that half.

    python3 scripts/grafana-prune-rules.py \
        --folder opnsense-alerts --folder opnsense-exporter-health-alerts \
        --keep-file /tmp/expected.txt

`--keep-file` is a newline-separated list of the rule names (App Platform
`metadata.name`) the repo defines. Anything live in `--folder` and absent from that
list is an orphan and is deleted.

Ordering note: an orphan is by definition a name the subsequent push will NOT write, so
pruning before or after the push is equivalent. It is run first only so a failed push
leaves the stack strictly smaller rather than in a half-pruned state.

Auth comes from gcx (env vars GRAFANA_SERVER / GRAFANA_TOKEN / GRAFANA_STACK_ID, or a
config file via GCX_CONFIG). Stack-id is read from the environment because the App
Platform namespace is `stacks-<id>`.

Stdlib only, matching the other generators in this repo.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys

GROUP = "rules.alerting.grafana.app/v0alpha1"
KINDS = ("alertrules", "recordingrules")
# The singular kind gcx wants for a delete, per resource collection.
DELETE_KIND = {
    "alertrules": "alertrules.v0alpha1.rules.alerting.grafana.app",
    "recordingrules": "recordingrules.v0alpha1.rules.alerting.grafana.app",
}


class PruneError(RuntimeError):
    """gcx is missing, unauthenticated, or returned something unparseable."""


def gcx(args: list[str], context: str | None) -> str:
    cmd = ["gcx"]
    if context:
        cmd += ["--context", context]
    cmd += args
    try:
        done = subprocess.run(cmd, check=False, capture_output=True, text=True)
    except OSError as exc:
        raise PruneError(f"cannot execute gcx: {exc}") from exc
    if done.returncode != 0:
        raise PruneError(f"gcx {' '.join(args)} failed ({done.returncode}):\n{done.stderr.strip()}")
    return done.stdout


def gcx_json(args: list[str], context: str | None):
    """Run gcx and parse its JSON.

    In agent mode gcx prints a one-line `{"class":"hint",...}` banner before the payload,
    so the first line is dropped when it is that banner rather than the document itself.
    """
    out = gcx(args + ["-o", "json"], context)
    try:
        return json.loads(out)
    except json.JSONDecodeError:
        pass
    head, _, rest = out.partition("\n")
    if '"class"' not in head:
        raise PruneError(f"unparseable gcx output: {out[:400]}")
    try:
        return json.loads(rest)
    except json.JSONDecodeError as exc:
        raise PruneError(f"unparseable gcx output: {out[:400]}") from exc


def folder_of(item: dict) -> str | None:
    meta = item.get("metadata", {})
    for source in (meta.get("annotations", {}), meta.get("labels", {})):
        value = source.get("grafana.app/folder")
        if value:
            return value
    return None


def live_rules(namespace: str, folders: set[str], context: str | None) -> list[tuple[str, str]]:
    """(collection, name) for every rule living in one of `folders`."""
    found = []
    for kind in KINDS:
        doc = gcx_json(["api", f"/apis/{GROUP}/namespaces/{namespace}/{kind}"], context)
        for item in doc.get("items", []):
            if folder_of(item) in folders:
                found.append((kind, item["metadata"]["name"]))
    return found


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--folder", action="append", required=True,
                    help="folder UID to prune within (repeatable)")
    ap.add_argument("--keep-file", required=True,
                    help="file of newline-separated rule names the repo defines")
    ap.add_argument("--context", default=os.environ.get("GRAFANA_CONTEXT") or None,
                    help="gcx context name; omit to use env-var auth")
    ap.add_argument("--stack-id", default=os.environ.get("GRAFANA_STACK_ID"),
                    help="Grafana stack id; namespace is stacks-<id>")
    ap.add_argument("--dry-run", action="store_true",
                    help="report orphans without deleting them")
    args = ap.parse_args()

    if not args.stack_id:
        print("error: --stack-id or GRAFANA_STACK_ID is required", file=sys.stderr)
        return 2
    namespace = f"stacks-{args.stack_id}"

    with open(args.keep_file, encoding="utf-8") as fh:
        keep = {line.strip() for line in fh if line.strip()}
    if not keep:
        # A push that generated nothing is a broken build, not an instruction to
        # delete every rule in the folder. Refuse rather than empty the stack.
        print(f"error: {args.keep_file} lists no rules — refusing to prune", file=sys.stderr)
        return 2

    folders = set(args.folder)
    try:
        live = live_rules(namespace, folders, args.context)
    except PruneError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1

    orphans = sorted((kind, name) for kind, name in live if name not in keep)
    print(f"folders={sorted(folders)} live={len(live)} repo={len(keep)} orphans={len(orphans)}")
    if not orphans:
        print("nothing to prune")
        return 0

    for kind, name in orphans:
        if args.dry_run:
            print(f"would delete {kind}/{name}")
            continue
        try:
            gcx(["resources", "delete", f"{DELETE_KIND[kind]}/{name}"], args.context)
        except PruneError as exc:
            print(f"error: deleting {kind}/{name}: {exc}", file=sys.stderr)
            return 1
        print(f"deleted {kind}/{name}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
