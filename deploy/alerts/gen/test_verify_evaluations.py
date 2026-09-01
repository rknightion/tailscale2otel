#!/usr/bin/env python3
"""Tests for the post-publication Grafana rule evaluation verifier."""

import datetime as dt
import unittest

from verify_evaluations import verify, wait_for_evaluations


UTC = dt.timezone.utc
PUBLISHED = dt.datetime(2026, 9, 1, 19, 0, 0, tzinfo=UTC)
CHECKED = dt.datetime(2026, 9, 1, 19, 1, 0, tzinfo=UTC)


def rule(name, *, paused=False, kind="AlertRule", no_data="Ok"):
    spec = {
        "title": name.replace("-", " "),
        "paused": paused,
        "trigger": {"interval": "1m0s"},
    }
    if kind == "AlertRule":
        spec["noDataState"] = no_data
    return {"kind": kind, "metadata": {"name": name}, "spec": spec}


def status(name, *, state="inactive", health="ok", last_evaluation=None,
           last_error="", paused=None):
    item = {
        "name": name,
        "state": state,
        "health": health,
        "lastEvaluation": last_evaluation,
        "lastError": last_error,
    }
    if paused is not None:
        item["isPaused"] = paused
    return item


def ruler(*items):
    return {"groups": [{"name": "tailscale2otel", "rules": list(items)}]}


class SnapshotVerificationTest(unittest.TestCase):
    def test_healthy_evaluation_records_all_runtime_fields(self):
        shipped = {"ts2o-healthy": rule("ts2o-healthy")}
        payload = ruler(status(
            "ts2o-healthy",
            state="inactive",
            health="ok",
            last_evaluation="2026-09-01T19:00:45Z",
            last_error="",
        ))

        report = verify(shipped, payload, now=CHECKED, published_at=PUBLISHED,
                        max_age=300)

        self.assertTrue(report.ok, report.failures)
        self.assertEqual(1, report.required)
        self.assertEqual({"ts2o-healthy"}, set(report.observed))
        record = report.rules["ts2o-healthy"]
        self.assertEqual("inactive", record["state"])
        self.assertEqual("ok", record["health"])
        self.assertEqual("2026-09-01T19:00:45Z", record["last_evaluation"])
        self.assertEqual("", record["last_error"])

    def test_recording_rule_status_can_use_app_platform_items(self):
        shipped = {"ts2o-recording": rule("ts2o-recording", kind="RecordingRule")}
        payload = {
            "items": [{
                "metadata": {"name": "ts2o-recording"},
                "status": {
                    "state": "inactive",
                    "health": "ok",
                    "lastEvaluation": "2026-09-01T19:00:45Z",
                    "lastError": "",
                },
            }],
        }

        report = verify(shipped, payload, now=CHECKED, published_at=PUBLISHED,
                        max_age=300)

        self.assertTrue(report.ok, report.failures)
        self.assertIsNone(report.rules["ts2o-recording"]["no_data_state"])

    def test_app_platform_pause_is_not_hidden_by_a_recent_timestamp(self):
        shipped = {"ts2o-live-paused": rule("ts2o-live-paused")}
        payload = {
            "items": [{
                "metadata": {"name": "ts2o-live-paused"},
                "spec": {"paused": True},
                "status": {
                    "state": "inactive",
                    "health": "ok",
                    "lastEvaluation": "2026-09-01T19:00:30Z",
                    "lastError": "",
                },
            }],
        }

        report = verify(shipped, payload, now=CHECKED, published_at=PUBLISHED,
                        max_age=300)

        self.assertFalse(report.ok)
        self.assertEqual(["runtime_paused"],
                         [failure["reason"] for failure in report.failures])

    def test_zero_last_evaluation_fails_closed(self):
        shipped = {"ts2o-zero": rule("ts2o-zero")}
        payload = ruler(status(
            "ts2o-zero",
            last_evaluation="0001-01-01T00:00:00Z",
        ))

        report = verify(shipped, payload, now=CHECKED, published_at=PUBLISHED,
                        max_age=300)

        self.assertFalse(report.ok)
        self.assertEqual(["last_evaluation_zero"],
                         [failure["reason"] for failure in report.failures])
        self.assertEqual("inactive", report.rules["ts2o-zero"]["state"])
        self.assertEqual("ok", report.rules["ts2o-zero"]["health"])
        self.assertEqual("", report.rules["ts2o-zero"]["last_error"])

    def test_missing_unpaused_rule_fails_closed(self):
        shipped = {"ts2o-missing": rule("ts2o-missing")}

        report = verify(shipped, ruler(), now=CHECKED, published_at=PUBLISHED,
                        max_age=300)

        self.assertFalse(report.ok)
        self.assertEqual(["missing_readback"],
                         [failure["reason"] for failure in report.failures])
        self.assertIsNone(report.rules["ts2o-missing"]["state"])
        self.assertIsNone(report.rules["ts2o-missing"]["health"])
        self.assertIsNone(report.rules["ts2o-missing"]["last_evaluation"])
        self.assertIsNone(report.rules["ts2o-missing"]["last_error"])

    def test_stale_last_evaluation_fails_closed(self):
        shipped = {"ts2o-stale": rule("ts2o-stale")}
        payload = ruler(status(
            "ts2o-stale",
            last_evaluation="2026-09-01T19:01:00Z",
        ))

        report = verify(shipped, payload,
                        now=dt.datetime(2026, 9, 1, 19, 10, 0, tzinfo=UTC),
                        published_at=PUBLISHED,
                        max_age=300)

        self.assertFalse(report.ok)
        self.assertEqual(["last_evaluation_stale"],
                         [failure["reason"] for failure in report.failures])

    def test_paused_zero_timestamp_is_recorded_but_not_required(self):
        shipped = {
            "ts2o-paused": rule("ts2o-paused", paused=True),
            "ts2o-healthy": rule("ts2o-healthy"),
        }
        payload = ruler(
            status("ts2o-paused", last_evaluation="0001-01-01T00:00:00Z"),
            status("ts2o-healthy", last_evaluation="2026-09-01T19:00:45Z"),
        )

        report = verify(shipped, payload, now=CHECKED, published_at=PUBLISHED,
                        max_age=300)

        self.assertTrue(report.ok, report.failures)
        self.assertEqual(1, report.required)
        self.assertEqual("0001-01-01T00:00:00Z",
                         report.rules["ts2o-paused"]["last_evaluation"])
        self.assertFalse(report.rules["ts2o-paused"]["fresh"])

    def test_fresh_no_data_state_is_evidence_of_evaluation(self):
        shipped = {"ts2o-disabled-feature": rule("ts2o-disabled-feature", no_data="Ok")}
        payload = ruler(status(
            "ts2o-disabled-feature",
            state="NoData",
            health="unknown",
            last_evaluation="2026-09-01T19:00:30Z",
            last_error="",
        ))

        report = verify(shipped, payload, now=CHECKED, published_at=PUBLISHED,
                        max_age=300)

        self.assertTrue(report.ok, report.failures)
        record = report.rules["ts2o-disabled-feature"]
        self.assertEqual("NoData", record["state"])
        self.assertEqual("unknown", record["health"])
        self.assertEqual("Ok", record["no_data_state"])

    def test_runtime_pause_fails_even_when_old_evaluation_is_recent(self):
        shipped = {"ts2o-live-paused": rule("ts2o-live-paused")}
        payload = ruler(status(
            "ts2o-live-paused",
            last_evaluation="2026-09-01T19:00:30Z",
            paused=True,
        ))

        report = verify(shipped, payload, now=CHECKED, published_at=PUBLISHED,
                        max_age=300)

        self.assertFalse(report.ok)
        self.assertEqual(["runtime_paused"],
                         [failure["reason"] for failure in report.failures])

    def test_pre_publication_evaluation_is_not_new_proof(self):
        shipped = {"ts2o-old": rule("ts2o-old")}
        payload = ruler(status(
            "ts2o-old",
            last_evaluation="2026-09-01T18:59:59Z",
        ))

        report = verify(shipped, payload, now=CHECKED, published_at=PUBLISHED,
                        max_age=300)

        self.assertFalse(report.ok)
        self.assertEqual(["last_evaluation_before_publication"],
                         [failure["reason"] for failure in report.failures])

    def test_missing_publication_boundary_fails_closed(self):
        shipped = {"ts2o-no-boundary": rule("ts2o-no-boundary")}
        payload = ruler(status(
            "ts2o-no-boundary",
            last_evaluation="2026-09-01T19:00:45Z",
        ))

        report = verify(shipped, payload, now=CHECKED, max_age=300)

        self.assertFalse(report.ok)
        self.assertEqual(["publication_boundary_missing"],
                         [failure["reason"] for failure in report.failures])


class PollingTest(unittest.TestCase):
    def test_transient_zero_window_is_retried_until_healthy(self):
        shipped = {"ts2o-eventually": rule("ts2o-eventually")}
        snapshots = [
            ruler(status("ts2o-eventually", last_evaluation="0001-01-01T00:00:00Z")),
            ruler(status("ts2o-eventually", last_evaluation="2026-09-01T19:00:15Z")),
        ]
        calls = []
        wall = [PUBLISHED]
        mono = [0.0]

        def fetch():
            calls.append(len(calls))
            return snapshots.pop(0)

        def now():
            return wall[0]

        def monotonic():
            return mono[0]

        def sleep(seconds):
            mono[0] += seconds
            wall[0] += dt.timedelta(seconds=seconds)

        report = wait_for_evaluations(
            shipped,
            fetch,
            published_at=PUBLISHED,
            timeout=30,
            poll_interval=10,
            max_age=30,
            now=now,
            monotonic=monotonic,
            sleep=sleep,
        )

        self.assertTrue(report.ok, report.failures)
        self.assertEqual(2, report.attempts)
        self.assertEqual([0, 1], calls)

    def test_zero_window_times_out_and_fails_closed(self):
        shipped = {"ts2o-never": rule("ts2o-never")}
        calls = []
        mono = [0.0]
        wall = [PUBLISHED]

        def fetch():
            calls.append(len(calls))
            return ruler(status("ts2o-never", last_evaluation="0001-01-01T00:00:00Z"))

        def now():
            return wall[0]

        def monotonic():
            return mono[0]

        def sleep(seconds):
            mono[0] += seconds
            wall[0] += dt.timedelta(seconds=seconds)

        report = wait_for_evaluations(
            shipped,
            fetch,
            published_at=PUBLISHED,
            timeout=20,
            poll_interval=10,
            max_age=20,
            now=now,
            monotonic=monotonic,
            sleep=sleep,
        )

        self.assertFalse(report.ok)
        self.assertEqual(3, report.attempts)
        self.assertEqual(20.0, mono[0])
        self.assertEqual(["last_evaluation_zero"],
                         [failure["reason"] for failure in report.failures])


if __name__ == "__main__":
    unittest.main()
