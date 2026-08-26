#!/usr/bin/env python3
"""Generate and check the public capability-count summaries."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
import re
import subprocess
import sys


COUNT_KEYS = {
    "alert_rules",
    "collectors",
    "dashboards",
    "log_events",
    "metrics",
    "recording_rules",
}


class SummaryPattern:
    def __init__(self, path: str, expression: str) -> None:
        self.path = path
        self.expression = re.compile(expression, re.MULTILINE)


SUMMARY_PATTERNS = (
    SummaryPattern("README.md", r"ships (?P<dashboards>\d+) dashboards"),
    SummaryPattern(
        "README.md",
        r"ships \*\*(?P<alert_rules>\d+) alert\s+rules and "
        r"(?P<recording_rules>\d+) recording rules\*\*",
    ),
    SummaryPattern("README.md", r"All (?P<metrics>\d+) metrics and (?P<log_events>\d+) log events"),
    SummaryPattern(
        "docs/index.md", r"all (?P<metrics>\d+) metrics and (?P<log_events>\d+) log-event types",
    ),
    SummaryPattern("docs/index.md", r"(?P<collectors>\d+) collectors run on independent schedules"),
    SummaryPattern(
        "docs/comparison.md",
        r"derives (?P<metrics>\d+) metrics and (?P<log_events>\d+) log-event types across "
        r"(?P<collectors>\d+) collectors",
    ),
    SummaryPattern(
        "docs/comparison.md", r"all (?P<metrics>\d+) metrics and (?P<log_events>\d+) log-event types",
    ),
    SummaryPattern("docs/comparison.md", r"(?P<collectors>\d+) collectors, four ingestion paths"),
    SummaryPattern("docs/faq.md", r"Each of the (?P<collectors>\d+) collectors runs"),
    SummaryPattern("docs/alerts.md", r"All (?P<alert_rules>\d+) alert rules carry"),
    SummaryPattern(
        "deploy/alerts/README.md",
        r"has \*\*(?P<alert_rules>\d+) alert rules \+ "
        r"(?P<recording_rules>\d+) recording rules",
    ),
)


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description=__doc__)
    result.add_argument("--write", action="store_true", help="regenerate the deterministic count source")
    result.add_argument("--root", type=Path, help=argparse.SUPPRESS)
    result.add_argument("--skip-source-check", action="store_true", help=argparse.SUPPRESS)
    return result


def repository_root(args: argparse.Namespace) -> Path:
    if args.root is not None:
        return args.root.resolve()
    return Path(__file__).resolve().parents[1]


def run_source_check(root: Path, write: bool) -> bool:
    command = [
        "go",
        "test",
        "./internal/catalog",
        "-run",
        "^TestCapabilityCountsSourceInSync$",
        "-count=1",
    ]
    if write:
        command.append("-write-capability-counts")
    completed = subprocess.run(command, cwd=root, check=False)
    if completed.returncode:
        print(
            "capability count source is stale or could not be derived; "
            "run scripts/regen-generated.sh counts after fixing the reported source",
            file=sys.stderr,
        )
        return False
    return True


def load_counts(root: Path) -> dict[str, int]:
    path = root / "internal/catalog/capability_counts.json"
    try:
        raw = json.loads(path.read_text())
    except FileNotFoundError:
        raise ValueError(f"missing {path}; run scripts/regen-generated.sh counts") from None
    except json.JSONDecodeError as err:
        raise ValueError(f"invalid {path}: {err}; run scripts/regen-generated.sh counts") from err
    if (
        not isinstance(raw, dict)
        or set(raw) != COUNT_KEYS
        or not all(type(raw[key]) is int and raw[key] >= 0 for key in COUNT_KEYS)
    ):
        raise ValueError(f"invalid {path}; run scripts/regen-generated.sh counts")
    return raw


def check_summaries(root: Path, counts: dict[str, int]) -> list[str]:
    errors: list[str] = []
    for pattern in SUMMARY_PATTERNS:
        path = root / pattern.path
        text = path.read_text()
        matches = list(pattern.expression.finditer(text))
        if len(matches) != 1:
            errors.append(
                f"{pattern.path}: expected one capability-count summary matching "
                f"{pattern.expression.pattern!r}; edit the summary to match "
                "internal/catalog/capability_counts.json"
            )
            continue
        for key, actual in matches[0].groupdict().items():
            expected = counts[key]
            if int(actual) != expected:
                errors.append(
                    f"{pattern.path}: {key} is {actual}, source says {expected}; "
                    "edit the public summary or run scripts/regen-generated.sh counts "
                    "after changing its source"
                )
    return errors


def main() -> int:
    args = parser().parse_args()
    root = repository_root(args)
    if not args.skip_source_check and not run_source_check(root, args.write):
        return 1
    try:
        counts = load_counts(root)
    except ValueError as err:
        print(err, file=sys.stderr)
        return 1
    errors = check_summaries(root, counts)
    if errors:
        print("public capability-count summaries are out of date:", file=sys.stderr)
        print(*errors, sep="\n", file=sys.stderr)
        return 1
    print("public capability-count summaries are in sync")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
