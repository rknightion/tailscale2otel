"""Tests for the required-CI reader behind `just ci-status`.

The rows mirror real workflow and check runs read from the GitHub API on 2026-11-20 (ids,
suites, times and conclusions kept), including the pull request whose first CI run was
cancelled by concurrency and still posted a failed `ci-success` after its replacement was
created: the case a timestamp comparison got wrong.
"""

import unittest

import ci_status


def run(id_, name, suite, created, status="completed", conclusion="success"):
    return {"id": id_, "name": name, "check_suite_id": suite, "created_at": created,
            "status": status, "conclusion": conclusion}


def check(id_, name, suite, started, status="completed", conclusion="success"):
    return {"id": id_, "name": name, "check_suite": {"id": suite}, "started_at": started,
            "status": status, "conclusion": conclusion}


# A green push to main: CI and Helm both ran and passed.
MAIN_RUNS = [
    run(44763300325, "CI", 121162211808, "2026-11-20T15:26:30Z"),
    run(44763299994, "Helm", 121162210761, "2026-11-20T15:26:30Z"),
]
MAIN_CHECKS = [
    check(136390407516, "ci-success", 121162211808, "2026-11-20T15:47:36Z"),
    check(136381867451, "helm-success", 121162210761, "2026-11-20T15:30:35Z"),
]

# The superseded CI run on a pull request head: cancelled, and its gate posted failure.
SUPERSEDED_RUN = run(44765763745, "CI", 121168967251, "2026-11-20T15:41:16Z", conclusion="cancelled")
SUPERSEDED_GATE = check(136389868944, "ci-success", 121168967251, "2026-11-20T15:42:34Z", conclusion="failure")
HELM_RUN = run(44765763982, "Helm", 121168967866, "2026-11-20T15:41:16Z")
HELM_GATE = check(136393107480, "helm-success", 121168967866, "2026-11-20T15:47:22Z")
# Its replacement, created three seconds later, still queued when the cancelled gate posted.
REPLACEMENT_QUEUED = run(44765773356, "CI", 121168990590, "2026-11-20T15:41:19Z", status="queued", conclusion=None)


class CiStatusTest(unittest.TestCase):
    def code(self, runs, checks):
        return ci_status.decide(runs, checks)[0]

    def test_green_main_push_is_green(self):
        self.assertEqual(self.code(MAIN_RUNS, MAIN_CHECKS), ci_status.GREEN)

    def test_main_push_without_a_helm_run_is_judged_on_ci_alone(self):
        self.assertEqual(self.code(MAIN_RUNS[:1], MAIN_CHECKS[:1]), ci_status.GREEN)

    def test_jobs_after_the_gate_do_not_hold_it(self):
        # fuzz and coverage are still running, so the CI run is in progress, but its gate passed.
        runs = [dict(MAIN_RUNS[0], status="in_progress", conclusion=None), MAIN_RUNS[1]]
        self.assertEqual(self.code(runs, MAIN_CHECKS), ci_status.GREEN)

    def test_a_failed_gate_on_a_finished_run_is_red(self):
        runs = [dict(MAIN_RUNS[0], conclusion="failure"), MAIN_RUNS[1]]
        checks = [dict(MAIN_CHECKS[0], conclusion="failure"), MAIN_CHECKS[1]]
        self.assertEqual(self.code(runs, checks), ci_status.RED)

    def test_a_failed_helm_gate_is_red(self):
        checks = [MAIN_CHECKS[0], dict(MAIN_CHECKS[1], conclusion="failure")]
        self.assertEqual(self.code(MAIN_RUNS, checks), ci_status.RED)

    def test_queued_replacement_hides_the_cancelled_runs_failed_gate(self):
        runs = [SUPERSEDED_RUN, REPLACEMENT_QUEUED, HELM_RUN]
        checks = [SUPERSEDED_GATE, HELM_GATE]
        self.assertEqual(self.code(runs, checks), ci_status.PENDING)

    def test_newest_run_cancelled_is_not_red(self):
        # The replacement was itself cancelled by a later push and posted a failed gate.
        replacement = dict(REPLACEMENT_QUEUED, status="completed", conclusion="cancelled")
        replacement_gate = check(136394556861, "ci-success", 121168990590, "2026-11-20T15:51:13Z",
                                 conclusion="failure")
        runs = [SUPERSEDED_RUN, replacement, HELM_RUN]
        checks = [SUPERSEDED_GATE, replacement_gate, HELM_GATE]
        self.assertEqual(self.code(runs, checks), ci_status.PENDING)

    def test_replacement_passing_after_a_cancelled_run_is_green(self):
        replacement = dict(REPLACEMENT_QUEUED, status="completed", conclusion="success")
        replacement_gate = check(136394556861, "ci-success", 121168990590, "2026-11-20T15:51:13Z")
        runs = [SUPERSEDED_RUN, replacement, HELM_RUN]
        checks = [SUPERSEDED_GATE, replacement_gate, HELM_GATE]
        self.assertEqual(self.code(runs, checks), ci_status.GREEN)

    def test_gate_still_running_is_pending(self):
        checks = [dict(MAIN_CHECKS[0], status="in_progress", conclusion=None), MAIN_CHECKS[1]]
        self.assertEqual(self.code(MAIN_RUNS, checks), ci_status.PENDING)

    def test_no_ci_run_yet_is_pending(self):
        self.assertEqual(self.code([], []), ci_status.PENDING)


if __name__ == "__main__":
    unittest.main()
