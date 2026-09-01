#!/usr/bin/env python3
"""Run an agent-mode CodeRabbit review once per directory shard.

The developer-facing entry point is the ``just review-sharded`` recipe.  This
module deliberately keeps the review output as raw NDJSON: each invocation's
stdout is appended to one aggregate file in the order the shards were run.
Human-readable shard status is written to stderr so the aggregate remains
machine-readable.

CodeRabbit can exit before emitting its terminal event (for example when the
review service closes the WebSocket).  A zero process exit status is therefore
not enough to call a shard clean.  A shard is clean only when it exits zero and
emits a JSON event whose ``type``, ``status`` or ``event`` field is
``"complete"``.

Exit codes:
    0  every shard exited zero and emitted a completion event
    1  at least one shard failed or lacked a completion event
    2  invalid arguments or an aggregate output error
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path
from typing import Callable, Iterable, Optional, Sequence, TextIO


DEFAULT_TIMEOUT_SECONDS = 15 * 60
FALSE_POSITIVE_WARNING = (
    "WARNING: --dir hides the rest of the repository. Before acting on any "
    "missing-symbol or missing-wiring finding, check the whole tree; the "
    "finding may be an artifact of this shard's scope."
)


@dataclass(frozen=True)
class ShardResult:
    directory: str
    returncode: Optional[int]
    complete: bool
    stdout: str
    stderr: str
    error: Optional[str] = None

    @property
    def clean(self) -> bool:
        """Whether the shard completed successfully and emitted its sentinel."""

        return self.returncode == 0 and self.complete


def has_complete_line(output: str) -> bool:
    """Return whether *output* contains a structured completion event.

    Agent-mode output is NDJSON, but malformed or human-readable lines should
    not be able to claim completion merely because they contain the word
    ``complete``.  Accept the two event shapes used by the CLI, plus the
    equivalent generic ``event`` spelling for forward compatibility.
    """

    for line in output.splitlines():
        if not line.strip():
            continue
        try:
            event = json.loads(line)
        except (TypeError, ValueError):
            continue
        if not isinstance(event, dict):
            continue
        if any(event.get(field) == "complete" for field in ("type", "status", "event")):
            return True
    return False


def review_command(coderabbit: str, base: str, directory: str) -> list[str]:
    """Build one CodeRabbit invocation without involving a shell."""

    return [
        coderabbit,
        "review",
        "--agent",
        "--base",
        base,
        "--dir",
        directory,
    ]


def _output_text(value: Optional[object]) -> str:
    """Normalize partial subprocess output, which may be bytes on timeout."""

    if value is None:
        return ""
    if isinstance(value, bytes):
        return value.decode("utf-8", errors="replace")
    return str(value)


def _run_shard(
    coderabbit: str,
    base: str,
    directory: str,
    timeout_seconds: int,
    runner: Callable[..., subprocess.CompletedProcess[str]],
) -> ShardResult:
    command = review_command(coderabbit, base, directory)
    try:
        completed = runner(
            command,
            capture_output=True,
            text=True,
            check=False,
            timeout=timeout_seconds,
        )
    except subprocess.TimeoutExpired as exc:
        return ShardResult(
            directory=directory,
            returncode=None,
            complete=False,
            stdout=_output_text(exc.stdout),
            stderr=_output_text(exc.stderr),
            error=f"timed out after {timeout_seconds}s",
        )
    except OSError as exc:
        return ShardResult(
            directory=directory,
            returncode=None,
            complete=False,
            stdout="",
            stderr="",
            error=str(exc),
        )

    stdout = completed.stdout or ""
    stderr = completed.stderr or ""
    return ShardResult(
        directory=directory,
        returncode=completed.returncode,
        complete=has_complete_line(stdout),
        stdout=stdout,
        stderr=stderr,
    )


def _validate_directories(directories: Iterable[str]) -> list[str]:
    values = list(directories)
    if not values:
        raise ValueError("at least one directory shard is required")
    if any(not directory for directory in values):
        raise ValueError("directory shards must not be empty")
    if len(set(values)) != len(values):
        raise ValueError("directory shards must be unique")
    return values


def _write_stdout(aggregate: TextIO, stdout: str) -> None:
    if not stdout:
        return
    aggregate.write(stdout)
    # NDJSON permits the final line to be unterminated, but adding the newline
    # here keeps adjacent shard output from being joined into one JSON value.
    if not stdout.endswith("\n"):
        aggregate.write("\n")


def _print_status(result: ShardResult, stream: TextIO) -> None:
    if result.clean:
        print(f"[{result.directory}] CLEAN (complete)", file=stream)
        return

    reasons = []
    if result.returncode is None:
        reasons.append("could not start CodeRabbit")
    elif result.returncode != 0:
        reasons.append(f"exit status {result.returncode}")
    if not result.complete:
        reasons.append("missing complete line")
    if result.error:
        reasons.append(result.error)
    print(f"[{result.directory}] FAILED ({'; '.join(reasons)})", file=stream)
    if result.stderr.strip():
        print(f"[{result.directory}] stderr:", file=stream)
        print(result.stderr.rstrip(), file=stream)


def run_shards(
    coderabbit: str,
    base: str,
    directories: Iterable[str],
    output: Optional[str],
    *,
    timeout_seconds: int = DEFAULT_TIMEOUT_SECONDS,
    status_stream: Optional[TextIO] = None,
    runner: Callable[..., subprocess.CompletedProcess[str]] = subprocess.run,
) -> int:
    """Run all directory shards, aggregate stdout, and return the gate result."""

    directories = _validate_directories(directories)
    status_stream = status_stream or sys.stderr
    print(FALSE_POSITIVE_WARNING, file=status_stream)

    aggregate: Optional[TextIO] = None
    aggregate_path = output
    close_aggregate = False
    try:
        if output == "-":
            aggregate = sys.stdout
        elif output is None:
            aggregate = tempfile.NamedTemporaryFile(
                mode="w",
                encoding="utf-8",
                prefix="tailscale2otel-coderabbit-review-",
                suffix=".ndjson",
                delete=False,
            )
            aggregate_path = aggregate.name
            close_aggregate = True
        else:
            output_path = Path(output)
            output_path.parent.mkdir(parents=True, exist_ok=True)
            aggregate = output_path.open("w", encoding="utf-8")
            close_aggregate = True

        results = []
        for directory in directories:
            result = _run_shard(
                coderabbit,
                base,
                directory,
                timeout_seconds,
                runner,
            )
            results.append(result)
            _write_stdout(aggregate, result.stdout)
            _print_status(result, status_stream)
    except OSError as exc:
        print(f"sharded CodeRabbit review: cannot write aggregate {aggregate_path}: {exc}",
              file=status_stream)
        return 2
    finally:
        if close_aggregate and aggregate is not None:
            aggregate.close()

    failed = [result for result in results if not result.clean]
    clean = len(results) - len(failed)
    if failed:
        failed_names = ", ".join(result.directory for result in failed)
        print(
            f"sharded CodeRabbit review: {clean} clean, {len(failed)} failed; "
            f"aggregate={aggregate_path}; failed shards: {failed_names}",
            file=status_stream,
        )
        return 1

    print(
        f"sharded CodeRabbit review: {clean} clean, 0 failed; aggregate={aggregate_path}",
        file=status_stream,
    )
    return 0


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True, help="wave base branch or commit")
    parser.add_argument(
        "--dirs",
        nargs="+",
        action="append",
        dest="dir_groups",
        help="one or more directory shards (may be repeated)",
    )
    parser.add_argument(
        "--dir",
        action="append",
        dest="single_dirs",
        default=[],
        help="one directory shard; repeat for each shard",
    )
    parser.add_argument(
        "--output",
        help="aggregate NDJSON path, or '-' for stdout (default: secure temporary file)",
    )
    parser.add_argument(
        "--timeout-seconds",
        type=int,
        default=DEFAULT_TIMEOUT_SECONDS,
        help=f"per-shard timeout (default: {DEFAULT_TIMEOUT_SECONDS})",
    )
    parser.add_argument(
        "--coderabbit",
        default=os.environ.get("CODERABBIT_BIN", "coderabbit"),
        help="CodeRabbit executable (default: CODERABBIT_BIN or coderabbit)",
    )
    return parser


def main(argv: Optional[Sequence[str]] = None) -> int:
    args = _parser().parse_args(argv)
    directories = []
    for group in args.dir_groups or []:
        directories.extend(group)
    directories.extend(args.single_dirs)
    try:
        if args.timeout_seconds <= 0:
            raise ValueError("timeout must be positive")
        return run_shards(
            args.coderabbit,
            args.base,
            directories,
            args.output,
            timeout_seconds=args.timeout_seconds,
        )
    except ValueError as exc:
        print(f"sharded CodeRabbit review: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
