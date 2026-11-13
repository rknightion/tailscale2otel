"""Offline safety coverage for pruning Grafana rules; never invokes gcx."""

import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location(
    "grafana_prune_rules", Path(__file__).with_name("grafana-prune-rules.py"))
prune = importlib.util.module_from_spec(spec)
spec.loader.exec_module(prune)


def rule(name, folder="example-folder", source="annotations"):
    meta = {"name": name}
    if folder is not None:
        meta[source] = {"grafana.app/folder": folder}
    return {"metadata": meta}


def listing(kind, items=(), hint=False):
    output = json.dumps({"items": list(items)})
    if hint:
        output = '{"class":"hint"}\n' + output
    return (["api", f"/apis/{prune.GROUP}/namespaces/stacks-0/{kind}", "-o", "json"], output)


def deletion(kind, name, output=""):
    return (["resources", "delete", f"{prune.DELETE_KIND[kind]}/{name}"], output)


class PruneSafetyTest(unittest.TestCase):
    def drive(self, expected=(), keep="kept-rule\n", extra=(), stack=True, context=None):
        pending = list(expected)
        calls = []

        def fake(args, actual_context):
            calls.append((args, actual_context))
            self.assertTrue(pending, f"unexpected gcx call: {args}")
            wanted, result = pending.pop(0)
            self.assertEqual(args, wanted)
            self.assertEqual(actual_context, context)
            if isinstance(result, Exception):
                raise result
            return result

        with tempfile.TemporaryDirectory() as directory:
            keep_file = Path(directory) / "keep.txt"
            keep_file.write_text(keep, encoding="utf-8")
            argv = ["prune", "--folder", "example-folder", "--keep-file", str(keep_file)]
            if stack:
                argv += ["--stack-id", "0"]
            argv += list(extra)
            output, errors = io.StringIO(), io.StringIO()
            with patch.object(prune, "gcx", side_effect=fake), \
                    patch.object(sys, "argv", argv), patch.dict(os.environ, {}, clear=True), \
                    contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
                code = prune.main()
        self.assertFalse(pending, f"missing gcx calls: {pending}")
        return code, output.getvalue(), errors.getvalue(), calls

    def test_empty_keep_refuses_without_calls(self):
        self.assertEqual(self.drive(keep="")[0], 2)

    def test_blank_keep_refuses_without_calls(self):
        self.assertEqual(self.drive(keep="\n  \n\t\n")[0], 2)

    def test_missing_stack_refuses_without_calls(self):
        self.assertEqual(self.drive(stack=False)[0], 2)

    def test_only_requested_folder_is_deleted(self):
        self.assertEqual(self.drive([
            listing("alertrules", [rule("rule-a"), rule("rule-b", "other-folder")]),
            listing("recordingrules"), deletion("alertrules", "rule-a")])[0], 0)

    def test_folder_in_label_is_in_scope(self):
        self.assertEqual(self.drive([
            listing("alertrules", [rule("rule-a", source="labels")]),
            listing("recordingrules"), deletion("alertrules", "rule-a")])[0], 0)

    def test_rule_without_folder_is_preserved(self):
        self.assertEqual(self.drive([
            listing("alertrules", [rule("rule-a", None)]), listing("recordingrules")])[0], 0)

    def test_kept_rule_is_preserved(self):
        self.assertEqual(self.drive([
            listing("alertrules", [rule("rule-a")]), listing("recordingrules")], keep="rule-a\n")[0], 0)

    def test_alert_and_recording_delete_kinds(self):
        self.assertEqual(self.drive([
            listing("alertrules", [rule("rule-a")]), listing("recordingrules", [rule("rule-b")]),
            deletion("alertrules", "rule-a"), deletion("recordingrules", "rule-b")])[0], 0)

    def test_dry_run_reports_without_deleting(self):
        code, output, _, _ = self.drive([
            listing("alertrules", [rule("rule-a")]), listing("recordingrules")], extra=["--dry-run"])
        self.assertEqual(code, 0)
        self.assertIn("would delete alertrules/rule-a", output)

    def test_hint_line_before_json_preserves_folder_scope(self):
        self.assertEqual(self.drive([
            listing("alertrules", [rule("rule-a"), rule("rule-b", "other-folder")], hint=True),
            listing("recordingrules", hint=True), deletion("alertrules", "rule-a")])[0], 0)

    def test_unparseable_output_stops_without_deleting(self):
        args, _ = listing("alertrules")
        self.assertEqual(self.drive([(args, "not json")])[0], 1)

    def test_failed_delete_stops_before_next_rule(self):
        self.assertEqual(self.drive([
            listing("alertrules", [rule("rule-a"), rule("rule-b")]), listing("recordingrules"),
            deletion("alertrules", "rule-a", prune.PruneError("synthetic failure"))])[0], 1)

    def test_context_reaches_every_call(self):
        self.assertEqual(self.drive([
            listing("alertrules", [rule("rule-a")]), listing("recordingrules"),
            deletion("alertrules", "rule-a")], extra=["--context", "example"], context="example")[0], 0)

    def test_context_precedes_resource_arguments(self):
        # Exercise the command adapter too, with subprocess stubbed: no real gcx.
        args = ["resources", "delete", "example-kind/rule-a"]
        result = subprocess.CompletedProcess([], 0, stdout="ok", stderr="")
        with patch.object(prune.subprocess, "run", return_value=result) as run:
            self.assertEqual(prune.gcx(args, "example"), "ok")
        run.assert_called_once_with(["gcx", "--context", "example"] + args,
                                    check=False, capture_output=True, text=True)


if __name__ == "__main__":
    unittest.main()
