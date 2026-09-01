#!/usr/bin/env python3
"""Fail-closed verification of Grafana rule evaluation after publication.

The desired-state manifests prove what this repository asks Grafana to create;
they do not prove that Grafana's ruler has run the rules.  This module consumes a
runtime ruler read-back and makes that second proof explicit:

* every shipped rule is inventoried, while only shipped, unpaused rules are a
  publication gate;
* a required rule must have a nonzero, parseable evaluation timestamp at or
  after the publication boundary and inside the bounded freshness window; and
* state, health, last evaluation, and last error are retained for every shipped
  rule, including paused rules and rules whose configured feature has no data.

It deliberately has no Grafana client.  The caller owns authentication and the
read-back function.  ``wait_for_evaluations`` calls that function repeatedly
until every required rule passes or the bounded publication window expires.

The command-line form verifies one JSON snapshot.  A deployment wrapper should
use ``wait_for_evaluations`` so the first transient zero-evaluation snapshot is
neither reported as success nor misdiagnosed as a lasting scheduler outage.
"""

from __future__ import annotations

import argparse
import datetime as _dt
import json
import math
import os
import sys
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Callable, Mapping


UTC = _dt.timezone.utc
EPOCH = _dt.datetime(1970, 1, 1, tzinfo=UTC)
_MISSING = object()


@dataclass(frozen=True)
class ShippedRule:
    """The small part of a shipped manifest needed by the verifier."""

    name: str
    kind: str
    title: str
    paused: bool
    no_data_state: str | None


@dataclass
class VerificationReport:
    """Sanitized, JSON-serializable result of one or more read-back attempts."""

    ok: bool
    checked_at: str
    publication_at: str | None
    max_age_seconds: float
    shipped: int
    required: int
    observed: list[str]
    missing: list[str]
    unmatched: list[str]
    failures: list[dict[str, Any]]
    rules: dict[str, dict[str, Any]]
    attempts: int = 1
    timed_out: bool = False
    readback_error: str | None = None

    def to_dict(self) -> dict[str, Any]:
        """Return the stable output shape used by the CLI and callers."""
        if self.ok:
            outcome = "passed_after_poll" if self.attempts > 1 else "passed"
        elif self.timed_out:
            outcome = "timed_out"
        else:
            outcome = "failed"
        return {
            "ok": self.ok,
            "outcome": outcome,
            "checked_at": self.checked_at,
            "publication_at": self.publication_at,
            "max_age_seconds": self.max_age_seconds,
            "shipped": self.shipped,
            "required": self.required,
            "observed": self.observed,
            "missing": self.missing,
            "unmatched": self.unmatched,
            "failures": self.failures,
            "rules": self.rules,
            "attempts": self.attempts,
            "timed_out": self.timed_out,
            "readback_error": self.readback_error,
        }


def _as_utc(value: _dt.datetime | str | int | float) -> _dt.datetime:
    """Convert a datetime or RFC3339/Unix timestamp to an aware UTC datetime."""
    if isinstance(value, _dt.datetime):
        result = value
        if result.tzinfo is None:
            result = result.replace(tzinfo=UTC)
        return result.astimezone(UTC)

    if isinstance(value, bool):
        raise ValueError("boolean is not a timestamp")

    if isinstance(value, (int, float)):
        if not math.isfinite(float(value)):
            raise ValueError("timestamp is not finite")
        return _dt.datetime.fromtimestamp(float(value), tz=UTC)

    if not isinstance(value, str):
        raise ValueError("timestamp is not a string or number")

    text = value.strip()
    if text.endswith("Z"):
        text = text[:-1] + "+00:00"
    result = _dt.datetime.fromisoformat(text)
    if result.tzinfo is None:
        result = result.replace(tzinfo=UTC)
    return result.astimezone(UTC)


def _timestamp(value: Any) -> tuple[_dt.datetime | None, str | None]:
    """Parse a last-evaluation value and classify malformed/zero values."""
    if value is _MISSING or value is None:
        return None, "missing"

    if isinstance(value, bool):
        return None, "invalid"

    if isinstance(value, (int, float)):
        if not math.isfinite(float(value)):
            return None, "invalid"
        if value <= 0:
            return None, "zero"
        try:
            result = _as_utc(value)
        except (OverflowError, OSError, ValueError):
            return None, "invalid"
        return (None, "zero") if result <= EPOCH else (result, None)

    if not isinstance(value, str) or not value.strip():
        return None, "missing"

    text = value.strip()
    # Some wrappers serialize a Unix timestamp as a JSON string.
    try:
        numeric = float(text)
    except ValueError:
        numeric = None
    if numeric is not None:
        return _timestamp(numeric)

    try:
        result = _as_utc(text)
    except (OverflowError, OSError, ValueError):
        return None, "invalid"
    return (None, "zero") if result <= EPOCH else (result, None)


def _format_time(value: _dt.datetime | None) -> str | None:
    if value is None:
        return None
    return value.astimezone(UTC).isoformat().replace("+00:00", "Z")


def _mapping(value: Any) -> Mapping[str, Any] | None:
    return value if isinstance(value, Mapping) else None


def _field(sources: list[Mapping[str, Any]], names: tuple[str, ...]) -> Any:
    for source in sources:
        for name in names:
            if name in source:
                return source[name]
    return _MISSING


def _status_sources(node: Mapping[str, Any]) -> list[Mapping[str, Any]]:
    """Return runtime/status objects in precedence order."""
    sources: list[Mapping[str, Any]] = [node]
    for key in ("status", "grafana_alert", "grafanaAlert", "rule", "spec"):
        child = _mapping(node.get(key))
        if child is not None:
            sources.append(child)
    return sources


def _runtime_aliases(node: Mapping[str, Any]) -> set[str]:
    """Collect UID/name/title spellings used by Grafana read-back endpoints."""
    aliases: set[str] = set()

    def add(value: Any) -> None:
        if isinstance(value, str) and value:
            aliases.add(value)

    for source in _status_sources(node):
        for key in ("name", "uid", "ruleUid", "rule_uid", "title"):
            add(source.get(key))
        for key in ("metadata", "spec"):
            child = _mapping(source.get(key))
            if child is not None:
                add(child.get("name"))
                add(child.get("uid"))
                add(child.get("title"))
        labels = _mapping(source.get("labels"))
        if labels is not None:
            add(labels.get("grafana_rule_uid"))
            add(labels.get("grafana_rule_name"))

    return aliases


def _runtime_rules(payload: Any) -> list[Mapping[str, Any]]:
    """Extract rule nodes from known Grafana ruler/App Platform envelopes."""
    if isinstance(payload, list):
        return [item for item in payload if isinstance(item, Mapping)]
    if not isinstance(payload, Mapping):
        return []

    for key in ("groups", "items", "rules"):
        value = payload.get(key)
        if isinstance(value, list):
            nodes: list[Mapping[str, Any]] = []
            for item in value:
                if not isinstance(item, Mapping):
                    continue
                if key == "groups":
                    nested = item.get("rules")
                    if isinstance(nested, list):
                        nodes.extend(node for node in nested if isinstance(node, Mapping))
                else:
                    nodes.append(item)
            return nodes

    data = payload.get("data")
    if isinstance(data, Mapping):
        return _runtime_rules(data)
    return []


def _manifest_rule(document: Mapping[str, Any], fallback_name: str | None = None) -> ShippedRule:
    kind = document.get("kind")
    if kind not in ("AlertRule", "RecordingRule"):
        raise ValueError("manifest has unsupported kind %r" % (kind,))
    metadata = _mapping(document.get("metadata")) or {}
    spec = _mapping(document.get("spec")) or {}
    name = metadata.get("name") or fallback_name
    if not isinstance(name, str) or not name:
        raise ValueError("manifest has no metadata.name")
    title = spec.get("title", name)
    if not isinstance(title, str) or not title:
        raise ValueError("%s has no spec.title" % name)
    paused = spec.get("paused", False)
    if not isinstance(paused, bool):
        raise ValueError("%s has non-boolean spec.paused" % name)
    no_data = spec.get("noDataState") if kind == "AlertRule" else None
    if no_data is not None and not isinstance(no_data, str):
        raise ValueError("%s has non-string spec.noDataState" % name)
    return ShippedRule(name, kind, title, paused, no_data)


def load_shipped(directory: str | os.PathLike[str]) -> dict[str, ShippedRule]:
    """Load all shipped alert and recording manifests from a directory."""
    root = Path(directory)
    if not root.is_dir():
        raise ValueError("shipped manifest directory does not exist: %s" % root)
    rules: dict[str, ShippedRule] = {}
    paths = sorted(root.glob("*.json"))
    for path in paths:
        if path.name == "_folder.json":
            continue
        try:
            document = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, ValueError) as exc:
            raise ValueError("cannot read shipped manifest %s: %s" % (path, exc)) from exc
        if not isinstance(document, Mapping):
            raise ValueError("shipped manifest %s is not an object" % path)
        rule = _manifest_rule(document)
        if rule.name in rules:
            raise ValueError("duplicate shipped rule %s" % rule.name)
        rules[rule.name] = rule
    if not rules:
        raise ValueError("no alert or recording manifests under %s" % root)
    return rules


def _coerce_shipped(value: Mapping[str, Any] | str | os.PathLike[str]) -> dict[str, ShippedRule]:
    if isinstance(value, (str, os.PathLike)):
        return load_shipped(value)
    if not isinstance(value, Mapping):
        raise ValueError("shipped rules must be a manifest directory or mapping")
    rules: dict[str, ShippedRule] = {}
    for fallback, document in value.items():
        if isinstance(document, ShippedRule):
            rule = document
        elif isinstance(document, Mapping):
            rule = _manifest_rule(document, str(fallback))
        else:
            raise ValueError("shipped rule %s is not a manifest object" % fallback)
        if rule.name in rules:
            raise ValueError("duplicate shipped rule %s" % rule.name)
        rules[rule.name] = rule
    if not rules:
        raise ValueError("no shipped rules")
    return rules


def _bool_value(value: Any) -> bool | None:
    if isinstance(value, bool):
        return value
    if isinstance(value, str):
        lowered = value.strip().lower()
        if lowered in ("true", "1"):
            return True
        if lowered in ("false", "0"):
            return False
    return None


def _base_record(rule: ShippedRule) -> dict[str, Any]:
    return {
        "name": rule.name,
        "kind": rule.kind,
        "title": rule.title,
        "paused": rule.paused,
        "required": not rule.paused,
        "no_data_state": rule.no_data_state,
        "observed": False,
        "runtime_paused": None,
        "state": None,
        "health": None,
        "last_evaluation": None,
        "last_error": None,
        "last_evaluation_age_seconds": None,
        "post_publication": False,
        "fresh": False,
        "failure_reasons": [],
    }


def _failure(name: str, reason: str, **extra: Any) -> dict[str, Any]:
    result: dict[str, Any] = {"name": name, "reason": reason}
    result.update(extra)
    return result


def verify(
    shipped: Mapping[str, Any] | str | os.PathLike[str],
    payload: Any,
    *,
    now: _dt.datetime | str | int | float | None = None,
    published_at: _dt.datetime | str | int | float | None = None,
    max_age: float = 300,
    clock_skew: float = 5,
) -> VerificationReport:
    """Verify one runtime read-back snapshot.

    ``published_at`` is intentionally part of the proof.  A recent evaluation
    from before the push is not evidence that the newly published resource was
    scheduled.  Callers that do not know when publication completed must fail
    closed rather than silently turning this into a generic health check.
    """
    if max_age < 0:
        raise ValueError("max_age must be non-negative")
    if clock_skew < 0:
        raise ValueError("clock_skew must be non-negative")
    rules = _coerce_shipped(shipped)
    checked = _as_utc(now if now is not None else _dt.datetime.now(tz=UTC))
    publication = None if published_at is None else _as_utc(published_at)
    publication_text = _format_time(publication)
    records = {name: _base_record(rule) for name, rule in sorted(rules.items())}
    required_names = [name for name, rule in sorted(rules.items()) if not rule.paused]
    failures: list[dict[str, Any]] = []

    aliases: dict[str, set[str]] = {}
    for name, rule in rules.items():
        for alias in (name, rule.title):
            aliases.setdefault(alias, set()).add(name)

    observed_nodes: dict[str, Mapping[str, Any]] = {}
    duplicate_names: set[str] = set()
    unmatched: list[str] = []
    for node in _runtime_rules(payload):
        candidates = set()
        for alias in _runtime_aliases(node):
            candidates.update(aliases.get(alias, set()))
        if len(candidates) != 1:
            # Unrelated rules are expected on a shared stack.  Keep only a
            # compact identity for diagnostics; required names are determined
            # by the shipped inventory below.
            names = sorted(_runtime_aliases(node))
            if names:
                unmatched.append(names[0])
            continue
        name = next(iter(candidates))
        if name in observed_nodes:
            duplicate_names.add(name)
        else:
            observed_nodes[name] = node

    for name, rule in sorted(rules.items()):
        record = records[name]
        node = observed_nodes.get(name)
        if node is None:
            continue
        record["observed"] = True
        sources = _status_sources(node)
        state = _field(sources, ("state",))
        health = _field(sources, ("health",))
        last_evaluation = _field(
            sources,
            ("lastEvaluation", "last_evaluation", "lastEvaluationTime", "last_evaluation_time"),
        )
        last_error = _field(sources, ("lastError", "last_error", "error"))
        runtime_paused = _field(sources, ("isPaused", "is_paused", "paused"))
        record["state"] = None if state is _MISSING else state
        record["health"] = None if health is _MISSING else health
        record["last_evaluation"] = None if last_evaluation is _MISSING else last_evaluation
        record["last_error"] = None if last_error is _MISSING else last_error
        record["runtime_paused"] = None if runtime_paused is _MISSING else _bool_value(runtime_paused)

        if rule.paused:
            # Paused rules are deliberately inventory-only.  In particular, a
            # zero timestamp here is expected and must not mask a valid proof
            # for the unpaused starter set.
            continue

        missing_fields = [
            field for field, value in (
                ("state", state),
                ("health", health),
                ("last_evaluation", last_evaluation),
                ("last_error", last_error),
            ) if value is _MISSING
        ]
        if missing_fields:
            reason = "readback_fields_missing"
            record["failure_reasons"].append(reason)
            failures.append(_failure(name, reason, fields=missing_fields))
            # There is no trustworthy evaluation proof to classify further.
            continue

        if record["runtime_paused"] is True:
            reason = "runtime_paused"
            record["failure_reasons"].append(reason)
            failures.append(_failure(name, reason))

        evaluated_at, timestamp_error = _timestamp(last_evaluation)
        if timestamp_error == "missing":
            reason = "last_evaluation_missing"
            record["failure_reasons"].append(reason)
            failures.append(_failure(name, reason))
            continue
        if timestamp_error == "zero":
            reason = "last_evaluation_zero"
            record["failure_reasons"].append(reason)
            failures.append(_failure(name, reason))
            continue
        if timestamp_error == "invalid" or evaluated_at is None:
            reason = "last_evaluation_invalid"
            record["failure_reasons"].append(reason)
            failures.append(_failure(name, reason))
            continue

        age = (checked - evaluated_at).total_seconds()
        record["last_evaluation_age_seconds"] = age
        if publication is not None and evaluated_at < publication:
            reason = "last_evaluation_before_publication"
            record["failure_reasons"].append(reason)
            failures.append(_failure(name, reason))
        else:
            record["post_publication"] = True

            if age > max_age:
                reason = "last_evaluation_stale"
                record["failure_reasons"].append(reason)
                failures.append(_failure(name, reason))
            elif age < -clock_skew:
                reason = "last_evaluation_in_future"
                record["failure_reasons"].append(reason)
                failures.append(_failure(name, reason))

        record["fresh"] = not record["failure_reasons"]

    observed = sorted(observed_nodes)
    missing = sorted(set(rules) - set(observed))
    for name in missing:
        if not rules[name].paused:
            reason = "missing_readback"
            records[name]["failure_reasons"].append(reason)
            failures.append(_failure(name, reason))

    for name in sorted(duplicate_names):
        if not rules[name].paused:
            reason = "duplicate_readback"
            records[name]["failure_reasons"].append(reason)
            failures.append(_failure(name, reason))

    if required_names and publication is None:
        reason = "publication_boundary_missing"
        failures.append(_failure("__publication__", reason))

    # A duplicate status is not a clean inventory even if the first copy was
    # healthy.  Make it visible for paused rules without making paused rules a
    # publication gate.
    for name in duplicate_names:
        records[name]["duplicate_readback"] = True
    for name, record in records.items():
        record.setdefault("duplicate_readback", False)

    # If duplicate aliases refer to more than one shipped rule, all candidates
    # remain missing and therefore fail if any are required.  De-duplicate the
    # list only for stable machine-readable output.
    unmatched = sorted(set(unmatched))
    return VerificationReport(
        ok=not failures,
        checked_at=_format_time(checked) or "",
        publication_at=publication_text,
        max_age_seconds=max_age,
        shipped=len(rules),
        required=len(required_names),
        observed=observed,
        missing=missing,
        unmatched=unmatched,
        failures=failures,
        rules=records,
    )


def wait_for_evaluations(
    shipped: Mapping[str, Any] | str | os.PathLike[str],
    readback: Callable[[], Any],
    *,
    published_at: _dt.datetime | str | int | float,
    timeout: float = 300,
    poll_interval: float = 10,
    max_age: float | None = None,
    clock_skew: float = 5,
    now: Callable[[], _dt.datetime] | None = None,
    monotonic: Callable[[], float] = time.monotonic,
    sleep: Callable[[float], None] = time.sleep,
) -> VerificationReport:
    """Poll a caller-owned read-back function in a bounded window."""
    if timeout < 0:
        raise ValueError("timeout must be non-negative")
    if poll_interval <= 0:
        raise ValueError("poll_interval must be positive")
    if max_age is None:
        max_age = timeout
    if max_age < 0:
        raise ValueError("max_age must be non-negative")

    # Load once before polling.  This prevents a changing working tree from
    # changing the inventory halfway through a publication proof.
    inventory = _coerce_shipped(shipped)
    publication = _as_utc(published_at)
    wall_now = now or (lambda: _dt.datetime.now(tz=UTC))
    start = monotonic()
    deadline = start + timeout
    attempts = 0
    last: VerificationReport | None = None

    while True:
        attempts += 1
        try:
            payload = readback()
            last = verify(
                inventory,
                payload,
                now=wall_now(),
                published_at=publication,
                max_age=max_age,
                clock_skew=clock_skew,
            )
        except Exception as exc:  # a failed read-back is never a pass
            last = verify(
                inventory,
                None,
                now=wall_now(),
                published_at=publication,
                max_age=max_age,
                clock_skew=clock_skew,
            )
            last.readback_error = str(exc)
            last.failures.insert(0, _failure("__readback__", "readback_error"))
            last.ok = False

        last.attempts = attempts
        if last.ok:
            return last

        remaining = deadline - monotonic()
        if remaining <= 0:
            last.timed_out = True
            return last
        sleep(min(poll_interval, remaining))


def parse_readback_json(text: str) -> Any:
    """Parse a direct JSON response, including gcx's optional hint line."""
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        # gcx agent mode may prefix the JSON payload with one structured hint
        # line.  Accept that one known envelope, and reject everything else.
        _head, separator, rest = text.partition("\n")
        if separator and '"class"' in _head:
            return json.loads(rest)
        raise


def _read_json(path: str) -> Any:
    if path == "-":
        text = sys.stdin.read()
    else:
        text = Path(path).read_text(encoding="utf-8")
    return parse_readback_json(text)


def _print_report(report: VerificationReport) -> None:
    data = report.to_dict()
    print("outcome      : %s" % data["outcome"])
    print("shipped      : %d rules (%d required)" % (data["shipped"], data["required"]))
    print("observed     : %d rules" % len(data["observed"]))
    print("missing      : %d" % len(data["missing"]))
    print("failures     : %d" % len(data["failures"]))
    for failure in data["failures"]:
        print("               - %s: %s" % (failure["name"], failure["reason"]))
    for name, record in data["rules"].items():
        print(
            "%s state=%r health=%r lastEvaluation=%r lastError=%r"
            % (name, record["state"], record["health"],
               record["last_evaluation"], record["last_error"])
        )


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--shipped-dir",
        default=os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
                             "grafana-managed"),
        help="directory containing shipped Grafana-managed manifests",
    )
    parser.add_argument(
        "--status-file",
        default="-",
        help="JSON ruler read-back file, or - for stdin",
    )
    parser.add_argument(
        "--published-at",
        required=True,
        help="RFC3339 or Unix timestamp recorded immediately after publication",
    )
    parser.add_argument("--now", help="override verification time (tests/debugging)")
    parser.add_argument("--max-age", type=float, default=300,
                        help="maximum allowed evaluation age in seconds (default: 300)")
    parser.add_argument("--clock-skew", type=float, default=5,
                        help="allowed clock skew in seconds (default: 5)")
    parser.add_argument("--json", action="store_true", help="emit JSON instead of a human summary")
    args = parser.parse_args(argv)

    try:
        shipped = load_shipped(args.shipped_dir)
        payload = _read_json(args.status_file)
        report = verify(
            shipped,
            payload,
            now=args.now,
            published_at=args.published_at,
            max_age=args.max_age,
            clock_skew=args.clock_skew,
        )
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print("verify_evaluations: %s" % exc, file=sys.stderr)
        return 2

    if args.json:
        json.dump(report.to_dict(), sys.stdout, indent=2, sort_keys=True)
        sys.stdout.write("\n")
    else:
        _print_report(report)
    return 0 if report.ok else 1


if __name__ == "__main__":
    raise SystemExit(main())
