#!/usr/bin/env python3
"""Regression tests for the scoped Grafana deployment reachability check."""

import subprocess
import unittest
from unittest import mock

import verify_deployment as verifier


class ReachabilityTest(unittest.TestCase):
    def test_broken_sibling_context_does_not_block_healthy_target(self):
        """Only the current context is checked; sibling failures are irrelevant."""
        calls = []

        def fake_run(cmd, cwd=None):
            calls.append(cmd)
            if cmd == ["gcx", "config", "current-context"]:
                return subprocess.CompletedProcess(cmd, 0, stdout="m7kni\n", stderr="")
            if cmd == ["gcx", "config", "check"]:
                # This is the sibling failure that made the old unscoped check
                # reject a healthy target.
                return subprocess.CompletedProcess(cmd, 1, stdout="sibling: keychain locked\n", stderr="")
            if cmd == ["gcx", "config", "check", "--context", "m7kni"]:
                return subprocess.CompletedProcess(cmd, 0, stdout="Connectivity: online\n", stderr="")
            self.fail("unexpected gcx command: %r" % (cmd,))

        with mock.patch.object(verifier.shutil, "which", return_value="/usr/bin/gcx"), \
                mock.patch.object(verifier, "run", side_effect=fake_run):
            self.assertEqual(verifier.check_gcx(), "m7kni")

        self.assertIn(["gcx", "config", "check", "--context", "m7kni"], calls)
        self.assertNotIn(["gcx", "config", "check"], calls)


if __name__ == "__main__":
    unittest.main()
