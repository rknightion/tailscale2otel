#!/usr/bin/env python3
"""Validate the generated Grafana-managed manifests under deploy/alerts/grafana-managed/.

Exits 1 if any file is not well-formed JSON, carries the wrong apiVersion/kind, or
violates one of the field rules below — so a hand-edited or stale manifest cannot
ship silently. Wired into deploy/alerts/gen/test_rules.py, which CI already runs
via `python3 -m unittest discover -s deploy/alerts/gen`.

Expected shapes:
  * ``_folder.json``   -> folder.grafana.app/v1beta1, kind Folder
  * every other *.json -> rules.alerting.grafana.app/v0alpha1,
                          kind AlertRule or RecordingRule

The rules worth having a validator for at all — each one is a mistake that every
other local check accepts and the API rejects at push time, or worse, accepts and
then ignores:

  * **``noDataState`` casing.** The API spells the OK no-data state ``"Ok"``, and
    ``execErrState`` spells its own ``"OK"``. The apiVersion: 1 provisioning
    format used ``"OK"`` for both, so a mechanical port of a rule pack produces
    manifests that fail to deploy. This exact mistake once made every rule in a
    sibling repo un-deployable and slipped past that repo's validator.
  * **``metadata.name`` must be a legal Grafana UID** — <=40 chars, ``[A-Za-z0-9_-]``.
    An over-long or oddly-punctuated slug is rejected per-resource, so a push
    "succeeds" having quietly skipped one rule.
  * **``__dashboardUid__``/``__panelId__`` come as a pair or not at all**, and
    ``__panelId__`` is a STRING. A lone half produces a dead "view panel" link.
  * **RecordingRule carries none of the alert-only fields.** Its v0alpha1 spec is
    ``additionalProperties: false`` over exactly
    ``{title, trigger, metric, expressions, targetDatasourceUID, labels, paused}``
    — notably it has no ``annotations`` and no ``for``.

Run standalone:  python3 deploy/alerts/gen/validate_manifests.py [DIR]
"""

import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_DIR = os.path.join(os.path.dirname(HERE), "grafana-managed")

FOLDER_API = "folder.grafana.app/v1beta1"
RULE_API = "rules.alerting.grafana.app/v0alpha1"
RULE_KINDS = ("AlertRule", "RecordingRule")

NODATA_STATES = {"NoData", "Ok", "Alerting", "KeepLast"}
EXECERR_STATES = {"Error", "Alerting", "OK", "KeepLast"}

# Grafana UID rules. 40 is a hard cap server-side.
UID_RE = re.compile(r"^[A-Za-z0-9_-]{1,40}$")

# Go duration strings, the only form this API accepts for `for` and for
# relativeTimeRange bounds.
GODUR_RE = re.compile(r"^(?:0s|(?:\d+h)?(?:\d+m)?\d+s)$")

ALERT_SPEC_KEYS = {"title", "trigger", "noDataState", "execErrState", "expressions",
                   "for", "keepFiringFor", "labels", "annotations", "paused",
                   "missingSeriesEvalsToResolve", "notificationSettings", "panelRef"}
RECORDING_SPEC_KEYS = {"title", "trigger", "metric", "expressions",
                       "targetDatasourceUID", "labels", "paused"}
# Fields that mean "this is an alert" and are therefore nonsense on a recording
# rule. Listed explicitly (rather than relying on the key-set check alone) so the
# error message names the actual mistake.
ALERT_ONLY = ("condition", "noDataState", "execErrState", "for", "annotations",
              "notificationSettings", "keepFiringFor")


def _check_rule_spec(name, kind, spec, errors):
    keys = ALERT_SPEC_KEYS if kind == "AlertRule" else RECORDING_SPEC_KEYS
    unknown = sorted(set(spec) - keys)
    if unknown:
        errors.append("%s: %s spec carries unknown field(s) %s — the v0alpha1 schema is "
                      "additionalProperties:false and rejects the whole resource"
                      % (name, kind, ", ".join(unknown)))
    if not spec.get("title"):
        errors.append("%s: spec.title is empty" % name)
    trigger = spec.get("trigger")
    if not isinstance(trigger, dict) or not trigger.get("interval"):
        errors.append("%s: spec.trigger.interval is missing — the rule would never evaluate" % name)
    exprs = spec.get("expressions")
    if not isinstance(exprs, dict) or not exprs:
        errors.append("%s: spec.expressions must be a non-empty map keyed by refId "
                      "(the provisioning format's `data` ARRAY is a different schema)" % name)
        return
    for ref, node in exprs.items():
        if not isinstance(node, dict) or "model" not in node:
            errors.append("%s: expression %s has no model" % (name, ref))
            continue
        if node["model"].get("refId") != ref:
            errors.append("%s: expression %s has model.refId %r — the map key and the refId "
                          "must agree" % (name, ref, node["model"].get("refId")))
        rtr = node.get("relativeTimeRange")
        if rtr is not None:
            for bound in ("from", "to"):
                value = rtr.get(bound)
                if not isinstance(value, str) or not GODUR_RE.match(value):
                    errors.append("%s: expression %s relativeTimeRange.%s = %r is not a Go "
                                  "duration string (this API does not take integer seconds)"
                                  % (name, ref, bound, value))
    sources = [ref for ref, node in exprs.items() if isinstance(node, dict) and node.get("source")]
    if len(sources) != 1:
        errors.append("%s: exactly one expression must carry \"source\": true (found %d) — "
                      "this API has no separate `condition` field, so without it the rule "
                      "imports with nothing to evaluate" % (name, len(sources)))


def _check_alert(name, spec, errors):
    nds = spec.get("noDataState")
    if nds not in NODATA_STATES:
        errors.append("%s: invalid spec.noDataState %r, must be one of %s. Note the casing: "
                      "\"Ok\", NOT \"OK\" — \"OK\" is the apiVersion:1 provisioning spelling "
                      "and makes the rule un-deployable." % (name, nds, sorted(NODATA_STATES)))
    ees = spec.get("execErrState")
    if ees not in EXECERR_STATES:
        errors.append("%s: invalid spec.execErrState %r, must be one of %s (note this one IS "
                      "\"OK\", not \"Ok\" — the two fields disagree)"
                      % (name, ees, sorted(EXECERR_STATES)))
    dur = spec.get("for")
    if dur is not None and not GODUR_RE.match(str(dur)):
        errors.append("%s: spec.for = %r is not a Go duration string (\"5m\" is not accepted; "
                      "\"5m0s\" is)" % (name, dur))

    annotations = spec.get("annotations") or {}
    dash, panel = annotations.get("__dashboardUid__"), annotations.get("__panelId__")
    if (dash is None) != (panel is None):
        errors.append("%s: __dashboardUid__ and __panelId__ must appear together or not at all "
                      "(got __dashboardUid__=%r, __panelId__=%r); a lone half is a dead link"
                      % (name, dash, panel))
    if panel is not None and not isinstance(panel, str):
        errors.append("%s: __panelId__ = %r must be a STRING; annotation values are typed as "
                      "strings and a number is dropped" % (name, panel))
    if not annotations.get("summary"):
        errors.append("%s: no summary annotation — the notification would have no subject" % name)
    if not annotations.get("runbook_url"):
        errors.append("%s: no runbook_url annotation" % name)
    for key, value in annotations.items():
        if not isinstance(value, str):
            errors.append("%s: annotation %s is %r, not a string" % (name, key, value))


def _check_recording(name, spec, errors):
    present = [f for f in ALERT_ONLY if f in spec]
    if present:
        errors.append("%s: RecordingRule carries alert-only field(s) %s. A recording rule has no "
                      "alert state, no runbook and no notification, and the v0alpha1 spec has no "
                      "`annotations` at all." % (name, ", ".join(present)))
    if not spec.get("metric"):
        errors.append("%s: RecordingRule has no spec.metric" % name)
    if not spec.get("targetDatasourceUID"):
        errors.append("%s: RecordingRule has no spec.targetDatasourceUID (required — it is where "
                      "the recorded series is written)" % name)


def validate_dir(directory):
    """Return a list of human-readable problems. Empty list means valid."""
    directory = str(directory)
    try:
        names = sorted(n for n in os.listdir(directory) if n.endswith(".json"))
    except OSError as err:
        return ["cannot read %s: %s" % (directory, err)]
    if not names:
        return ["no manifests found in %s — an empty directory must not read as a pass" % directory]

    errors = []
    saw_folder = False
    seen_uids = {}
    for name in names:
        path = os.path.join(directory, name)
        try:
            with open(path, encoding="utf-8") as f:
                doc = json.load(f)
        except (OSError, ValueError) as err:
            errors.append("%s: not well-formed JSON: %s" % (name, err))
            continue
        if not isinstance(doc, dict):
            errors.append("%s: top level is %s, not an object" % (name, type(doc).__name__))
            continue

        api, kind = doc.get("apiVersion"), doc.get("kind")
        metadata = doc.get("metadata") or {}
        uid = metadata.get("name")

        if name == "_folder.json":
            saw_folder = True
            if api != FOLDER_API or kind != "Folder":
                errors.append("%s: expected apiVersion %s / kind Folder, got %s / %s"
                              % (name, FOLDER_API, api, kind))
            if not (doc.get("spec") or {}).get("title"):
                errors.append("%s: folder has no spec.title" % name)
            if not uid or not UID_RE.match(str(uid)):
                errors.append("%s: metadata.name %r is not a legal folder UID" % (name, uid))
            continue

        if api != RULE_API:
            errors.append("%s: expected apiVersion %s, got %r" % (name, RULE_API, api))
        if kind not in RULE_KINDS:
            errors.append("%s: expected kind in %s, got %r" % (name, list(RULE_KINDS), kind))
            continue

        if not uid or not UID_RE.match(str(uid)):
            errors.append("%s: metadata.name %r is not a legal Grafana UID (<=40 chars, "
                          "[A-Za-z0-9_-])" % (name, uid))
        else:
            if uid in seen_uids:
                errors.append("%s: metadata.name %r is already used by %s" % (name, uid, seen_uids[uid]))
            seen_uids[uid] = name
            if name != "%s.json" % uid:
                errors.append("%s: filename does not match metadata.name %r; the emitter names "
                              "files after the uid so a rename cannot leave a stale twin"
                              % (name, uid))
        for where in ("annotations", "labels"):
            if (metadata.get(where) or {}).get("grafana.app/folder") is None:
                errors.append("%s: metadata.%s is missing grafana.app/folder — the rule would "
                              "land in the default folder" % (name, where))

        spec = doc.get("spec")
        if not isinstance(spec, dict):
            errors.append("%s: spec is missing or not an object" % name)
            continue
        _check_rule_spec(name, kind, spec, errors)
        if kind == "AlertRule":
            _check_alert(name, spec, errors)
        else:
            _check_recording(name, spec, errors)

    if not saw_folder:
        errors.append("_folder.json is missing — the folder manifest must be present, and it must "
                      "be pushed before the rules that reference it")
    return errors


def main(argv):
    directory = argv[1] if len(argv) > 1 else DEFAULT_DIR
    errors = validate_dir(directory)
    if errors:
        print("Grafana-managed manifest validation failed:", file=sys.stderr)
        for e in errors:
            print("  - %s" % e, file=sys.stderr)
        return 1
    count = len([n for n in os.listdir(directory) if n.endswith(".json")])
    print("validated %d grafana-managed manifests in %s: OK" % (count, directory))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
