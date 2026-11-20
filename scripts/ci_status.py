#!/usr/bin/env python3
"""Report the required CI state of one commit: exit 0 green, 1 red, 2 pending or unreadable.

`just ci-status <sha>` runs this, and the loop landing tool reads `main` through it (LOOP.md
`ci-status:`). The required checks are `ci-success` (posted by the CI workflow) and
`helm-success` (posted by the Helm workflow, which runs on `main` only for `helm.yml`'s push
paths, so a commit with no Helm run is judged on CI alone).

Each gate is read from the newest run of its own workflow, by that run's check suite, never by
timestamp. Two cases make timestamps wrong:

- a run cancelled by concurrency still posts its gate (`if: always()`) as a failure, often after
  its replacement was created, so the newest gate by time can belong to the superseded run;
- jobs outside the gate (fuzz, coverage) finish after the gate, so an unfinished workflow run
  does not mean an unfinished gate.
"""

import json
import subprocess
import sys

REPO = "rknightion/tailscale2otel"
GATES = {"CI": "ci-success", "Helm": "helm-success"}
REQUIRED = {"CI"}
PASS = {"success", "skipped", "neutral"}

GREEN, RED, PENDING = 0, 1, 2


def newest(items, key):
    return max(items, key=key) if items else None


def decide(workflow_runs, check_runs):
    """Return (exit code, report lines) from the commit's workflow runs and gate check runs."""
    codes, lines = [], []
    for workflow, gate in GATES.items():
        run = newest([r for r in workflow_runs if r["name"] == workflow], key=lambda r: (r["created_at"], r["id"]))
        if run is None:
            if workflow in REQUIRED:
                codes.append(PENDING)
                lines.append(f"{gate}: no {workflow} run yet")
            continue
        suite = run["check_suite_id"]
        check = newest(
            [c for c in check_runs if c["name"] == gate and c["check_suite"]["id"] == suite],
            key=lambda c: (c.get("started_at") or "", c["id"]),
        )
        if check is None:
            codes.append(PENDING)
            lines.append(f"{gate}: {workflow} run {run['id']} has not posted it yet")
            continue
        status, conclusion = check["status"], check.get("conclusion") or ""
        lines.append(f"{gate}\t{status}\t{conclusion}")
        if status != "completed":
            codes.append(PENDING)
        elif conclusion in PASS:
            codes.append(GREEN)
        elif run.get("status") == "completed" and run.get("conclusion") == "cancelled":
            # The gate failed only because its run was cancelled: superseded, not red.
            codes.append(PENDING)
            lines.append(f"{gate}: {workflow} run {run['id']} was cancelled")
        elif conclusion == "cancelled":
            codes.append(PENDING)
        else:
            codes.append(RED)
    if RED in codes:
        return RED, lines
    if PENDING in codes:
        return PENDING, lines
    return GREEN, lines


def gh_items(path, field):
    out = subprocess.run(
        ["gh", "api", "--paginate", path, "--jq", f".{field}[]"],
        capture_output=True,
        text=True,
        check=True,
        timeout=120,
    ).stdout
    return [json.loads(line) for line in out.splitlines() if line.strip()]


def main(argv):
    if len(argv) != 2:
        print("usage: ci_status.py <sha>", file=sys.stderr)
        return PENDING
    sha = argv[1]
    try:
        runs = gh_items(f"repos/{REPO}/actions/runs?head_sha={sha}&per_page=100", "workflow_runs")
        checks = gh_items(f"repos/{REPO}/commits/{sha}/check-runs?filter=all&per_page=100", "check_runs")
    except (subprocess.SubprocessError, OSError, ValueError) as error:
        print(f"ci-status: cannot read {sha}: {error}", file=sys.stderr)
        return PENDING
    code, lines = decide(runs, checks)
    print("\n".join(lines))
    return code


if __name__ == "__main__":
    sys.exit(main(sys.argv))
