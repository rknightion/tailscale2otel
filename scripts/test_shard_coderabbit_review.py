#!/usr/bin/env python3
"""Tests for the directory-sharded CodeRabbit review runner."""

import io
import json
import sys
import pathlib
import stat
import subprocess
import tempfile
import unittest
from unittest import mock

if __package__:
    from . import shard_coderabbit_review as review
else:
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
        {"type": "finding", "severity": "major", "message": "first major finding"},
        {"type": "finding", "severity": "major", "message": "second major finding"},
        {"type": "status", "status": "complete", "findings": 2},
    ],
    "internal/mixed": [
        {"type": "finding", "severity": "major", "message": "mixed major finding"},
        {"type": "finding", "severity": "MINOR", "message": "mixed minor finding"},
        {"type": "finding", "message": "finding with no severity"},
        {"type": "status", "status": "complete", "findings": 3},
    ],
    "scripts": [
        {"type": "status", "status": "complete", "findings": 0},
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

    def test_complete_shards_report_finding_counts_and_clean_means_zero(self):
        aggregate = self.root / "review.ndjson"
        status = io.StringIO()

        code = review.run_shards(
            self.command,
            "wave-base",
            ["internal/app", "scripts"],
            str(aggregate),
            status_stream=status,
        )

        # Findings are not transport failures: the exit status stays 0.
        self.assertEqual(code, 0)
        report = status.getvalue()
        self.assertIn("WARNING: --dir hides the rest of the repository", report)
        self.assertIn("[internal/app] COMPLETE: 2 major", report)
        self.assertNotIn("[internal/app] CLEAN", report)
        self.assertIn("[scripts] CLEAN", report)
        summary = report.splitlines()[-1]
        self.assertIn("1 clean, 1 with findings (2 major), 0 failed", summary)
        contents = aggregate.read_text(encoding="utf-8")
        self.assertIn('"message": "first major finding"', contents)
        self.assertIn('"message": "second major finding"', contents)
        self.assertEqual(contents.count('"status": "complete"'), 2)

    def test_mixed_severities_are_totalled_and_unknown_is_not_clean(self):
        status = io.StringIO()

        code = review.run_shards(
            self.command,
            "wave-base",
            ["internal/app", "internal/mixed"],
            str(self.root / "review.ndjson"),
            status_stream=status,
        )

        self.assertEqual(code, 0)
        report = status.getvalue()
        self.assertIn("[internal/mixed] COMPLETE: 1 major, 1 minor, 1 unknown", report)
        self.assertNotIn("CLEAN", report)
        self.assertIn(
            "0 clean, 2 with findings (3 major, 1 minor, 1 unknown), 0 failed",
            report.splitlines()[-1],
        )

    def assert_cli_failure(self, events, reason):
        output = "\n".join(json.dumps(event) for event in events) + "\n"
        command = write_executable(
            self.root, "fake-invalid-coderabbit",
            "#!/usr/bin/env python3\nimport sys\nsys.stdout.write(" + repr(output) + ")\n",
        )
        aggregate = self.root / "invalid.ndjson"
        result = subprocess.run(
            [sys.executable, str(pathlib.Path(review.__file__).resolve()),
             "--base", "wave-base", "--dir", "scripts",
             "--coderabbit", command, "--output", str(aggregate)],
            capture_output=True, text=True, timeout=10, check=False,
        )
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("[scripts] FAILED", result.stderr)
        self.assertNotIn("[scripts] CLEAN", result.stderr)
        self.assertIn(reason, result.stderr)
        self.assertIn("0 clean, 0 with findings, 1 failed", result.stderr)
        self.assertEqual(aggregate.read_text(encoding="utf-8"), output)

    def test_complete_findings_count_mismatch_fails_cli(self):
        for reported, findings in [(1, []), (0, [{"type": "finding"}]),
                                   (2, [{"type": "finding", "severity": "major"}]),
                                   (-1, [])]:
            with self.subTest(reported=reported, findings=findings):
                self.assert_cli_failure(
                    findings + [{"type": "complete", "findings": reported}],
                    f"findings count mismatch: counted {len(findings)}, complete reports {reported}",
                )

    def test_missing_or_non_integer_complete_findings_fails_cli(self):
        for shape in [{"type": "complete"}, {"type": "status", "status": "complete"},
                      {"event": "complete"}]:
            with self.subTest(shape=shape, missing=True):
                self.assert_cli_failure([shape], "missing or non-integer complete findings")
            for value in [None, "0", 0.0, False, True, [], {}]:
                with self.subTest(shape=shape, value=value):
                    self.assert_cli_failure(
                        [dict(shape, findings=value)],
                        "missing or non-integer complete findings",
                    )

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
        self.assertIn("[internal/app] COMPLETE: 2 major", report)
        self.assertIn("[missing] FAILED", report)
        self.assertIn("missing complete line", report)
        self.assertIn(
            "0 clean, 1 with findings (2 major), 1 failed", report.splitlines()[-1]
        )

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
