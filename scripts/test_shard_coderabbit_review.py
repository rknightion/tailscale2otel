#!/usr/bin/env python3
"""Tests for the directory-sharded CodeRabbit review runner."""

import io
import pathlib
import stat
import subprocess
import tempfile
import unittest
from unittest import mock

import shard_coderabbit_review as review


FAKE_CODERABBIT = r"""#!/usr/bin/env python3
import json
import sys

args = sys.argv[1:]
assert args[:2] == ["review", "--agent"], args
base_index = args.index("--base")
assert args[base_index + 1] == "wave-base", args
directory_index = args.index("--dir")
directory = args[directory_index + 1]

events = {
    "internal/app": [
        {"type": "finding", "message": "a finding is still clean when complete"},
        {"type": "status", "status": "complete"},
    ],
    "scripts": [
        {"type": "status", "status": "complete"},
    ],
    "missing": [
        {"type": "status", "status": "running"},
        {"type": "finding", "message": "review complete is not a completion event"},
    ],
}
for event in events[directory]:
    print(json.dumps(event))
"""


def write_executable(directory, name, body):
    path = pathlib.Path(directory) / name
    path.write_text(body, encoding="utf-8")
    path.chmod(path.stat().st_mode | stat.S_IXUSR)
    return str(path)


class ShardedReviewTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.temp.name)
        self.command = write_executable(self.root, "fake-coderabbit", FAKE_CODERABBIT)

    def tearDown(self):
        self.temp.cleanup()

    def test_complete_shards_are_clean_and_findings_are_aggregated(self):
        aggregate = self.root / "review.ndjson"
        status = io.StringIO()

        code = review.run_shards(
            self.command,
            "wave-base",
            ["internal/app", "scripts"],
            str(aggregate),
            status_stream=status,
        )

        self.assertEqual(code, 0)
        report = status.getvalue()
        self.assertIn("WARNING: --dir hides the rest of the repository", report)
        self.assertIn("[internal/app] CLEAN", report)
        self.assertIn("[scripts] CLEAN", report)
        contents = aggregate.read_text(encoding="utf-8")
        self.assertIn('"message": "a finding is still clean when complete"', contents)
        self.assertEqual(contents.count('"status": "complete"'), 2)

    def test_missing_complete_event_fails_the_review(self):
        aggregate = self.root / "review.ndjson"
        status = io.StringIO()

        code = review.run_shards(
            self.command,
            "wave-base",
            ["internal/app", "missing"],
            str(aggregate),
            status_stream=status,
        )

        self.assertEqual(code, 1)
        report = status.getvalue()
        self.assertIn("[internal/app] CLEAN", report)
        self.assertIn("[missing] FAILED", report)
        self.assertIn("missing complete line", report)

    def test_only_a_completion_event_counts(self):
        self.assertTrue(review.has_complete_line('{"type":"status","status":"complete"}'))
        self.assertTrue(review.has_complete_line('{"type":"complete"}'))
        self.assertFalse(review.has_complete_line('{"message":"review complete"}'))

    def test_timeout_fails_the_shard_and_continues(self):
        aggregate = self.root / "review.ndjson"
        status = io.StringIO()

        def timeout_runner(*args, **kwargs):
            raise subprocess.TimeoutExpired(args[0], kwargs["timeout"])

        code = review.run_shards(
            self.command,
            "wave-base",
            ["internal/app"],
            str(aggregate),
            timeout_seconds=7,
            status_stream=status,
            runner=timeout_runner,
        )

        self.assertEqual(code, 1)
        self.assertIn("timed out after 7s", status.getvalue())

    def test_timeout_preserves_byte_output_as_text(self):
        aggregate = self.root / "review.ndjson"
        status = io.StringIO()

        def timeout_runner(*args, **kwargs):
            raise subprocess.TimeoutExpired(
                args[0],
                kwargs["timeout"],
                output=b'{"type":"status","status":"reviewing"}\n',
                stderr=b"partial diagnostic\n",
            )

        code = review.run_shards(
            self.command,
            "wave-base",
            ["internal/app"],
            str(aggregate),
            timeout_seconds=7,
            status_stream=status,
            runner=timeout_runner,
        )

        self.assertEqual(code, 1)
        self.assertEqual(
            aggregate.read_text(encoding="utf-8"),
            '{"type":"status","status":"reviewing"}\n',
        )
        self.assertIn("partial diagnostic", status.getvalue())

    def test_omitted_output_uses_a_unique_secure_temporary_file(self):
        status = io.StringIO()
        with mock.patch.object(tempfile, "tempdir", str(self.root)):
            code = review.run_shards(
                self.command,
                "wave-base",
                ["scripts"],
                None,
                status_stream=status,
            )

        self.assertEqual(code, 0)
        output = status.getvalue().split("aggregate=", 1)[1].splitlines()[0]
        output_path = pathlib.Path(output)
        self.assertEqual(output_path.parent, self.root)
        self.assertTrue(output_path.is_file())
        self.assertFalse(output_path.is_symlink())


if __name__ == "__main__":
    unittest.main()
