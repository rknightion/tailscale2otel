#!/usr/bin/env python3
"""Generate the tailscale2otel **Grafana-managed** alerting + recording rules.

Emits one ``rules.alerting.grafana.app/v0alpha1`` manifest per rule (plus a
``folder.grafana.app/v1beta1`` folder manifest) into ``../grafana-managed/``. That
is the resource form ``gcx resources push`` consumes. Grafana evaluates these
itself (multi-datasource, ``noDataState``/``execErrState``, ``paused``); they are
NOT Prometheus/Loki *ruler* ("datasource-managed") rules, and this repo
deliberately no longer ships a ruler-compatible file.

Dashboards-as-code style: edit this generator, regenerate, commit the manifests.
Only the Python standard library is required (PyYAML is intentionally NOT a
dependency — a tiny block-YAML emitter lives in ``yamlify`` below, used solely by
the throwaway ``--prom-out`` rendering).

Usage:
    python3 build_rules.py --out ../grafana-managed          # the shipped artifact
    python3 build_rules.py --prom-out /tmp/prom-rules.yaml   # TEST-ONLY, never committed

Deploy with (folder first, so the rules resolve it)::

    gcx resources push -p deploy/alerts/grafana-managed

**The two format traps this file exists to get right.** The manifest format is
NOT the ``apiVersion: 1`` file-provisioning format an earlier version of this
generator emitted, and the differences are silent ones:

  * ``noDataState`` spells its OK value ``"Ok"``; ``execErrState`` spells its own
    ``"OK"``. The asymmetry is real. The provisioning format used ``"OK"`` for
    both, so a mechanical port produces manifests every local check accepts and
    the API rejects at push time.
  * Durations are Go-style strings (``"30m0s"``, ``"0s"``, ``"1h0m0s"``), not the
    integer seconds ``relativeTimeRange`` took in the provisioning format.
  * Panel links are the paired ``__dashboardUid__``/``__panelId__`` ANNOTATIONS
    (``__panelId__`` a string). The top-level ``dashboardUid``/``panelId`` fields
    are provisioning-only, and this API declares ``additionalProperties: false``,
    so emitting them is a hard rejection.
  * ``expressions`` is a MAP keyed by refId, and the node the rule reads carries
    ``"source": true`` — there is no separate ``condition`` field.
  * ``RecordingRule`` has no ``annotations``, ``for``, ``condition``,
    ``noDataState`` or ``execErrState`` at all. Its required spec is
    ``{title, trigger, metric, expressions, targetDatasourceUID}``.

Conventions baked in here:
  * Every rule is a 3-node Grafana pipeline: A (datasource query, range) ->
    B (reduce, last) -> C (threshold), C marked ``source``. This is exactly what
    the Grafana UI produces, so the rules round-trip cleanly through the UI.
  * Metric names are the OTLP->Prometheus *normalized* names (see ../README.md).
  * Gauge reads are wrapped in ``max by (<real dims>)`` so an alert instance is
    keyed only by the dimensions it is about, aggregating away the exporter's
    infra labels (job/instance/service_*). The exporter no longer emits a
    per-deploy ``service_version`` label (#187 — the version lives on
    ``tailscale2otel_build_info``), so a redeploy no longer forks these series;
    the ``max by`` is still required for the remaining infra labels.
  * Every alert declares an evaluation ``policy=`` (see POLICY below) which fixes
    its ``noDataState``/``execErrState`` pair. There is no global default: a rule
    that cannot say what absence and a query error mean is a rule nobody has
    thought about (#388).
  * Every alert declares a ``runbook=`` slug resolving to an anchored section of
    ``docs/runbooks.md``, and may declare a ``panel=`` title resolving to a panel
    in the generated flagship dashboard. Both are validated at generation time —
    a bad slug or an ambiguous panel title FAILS the build rather than emitting a
    dead link (#387).
  * Datasource UIDs are the portable Grafana Cloud defaults (grafanacloud-prom /
    grafanacloud-logs); swap them for a self-hosted stack.
  * Rules NOT in the recommended starter set ship ``paused: true`` (enable in
    the UI when you want them). Starter-set + the explicitly-requested 48h key
    tiers ship enabled.

``--prom-out`` renders the SAME catalogue as a plain Prometheus rule file. It is
a **test fixture, not a deliverable**: it is never committed and never shipped.
It exists because ``promtool test rules`` is the only thing in this repo that
EXECUTES a rule against synthetic series, and promtool understands no other
format. Loki-backed rules are omitted from it (promtool cannot parse LogQL).
"""

import argparse
import json
import os
import re

PROM = "grafanacloud-prom"
LOKI = "grafanacloud-logs"
FOLDER = "tailscale2otel"
INTERVAL = "1m"

# Repo-relative inputs this generator VALIDATES against. Both are read at
# generation time, so a rename on either side fails the build instead of shipping
# a link that 404s (runbook) or points at whatever panel inherited the id
# (dashboard). Resolved from __file__, never from the CWD.
_GEN_DIR = os.path.dirname(os.path.abspath(__file__))
_REPO_ROOT = os.path.dirname(os.path.dirname(os.path.dirname(_GEN_DIR)))
RUNBOOK_DOC = os.path.join(_REPO_ROOT, "docs", "runbooks.md")
DASHBOARD_DOC = os.path.join(_REPO_ROOT, "deploy", "grafana", "tailscale2otel.json")

# The public docs site (zensical `site_url` + the runbooks page). Deliberately the
# PROJECT's own docs, not the operator's Grafana stack — an annotation must never
# carry a tenant-specific host, org id or stack slug.
RUNBOOK_BASE = "https://m7kni.io/tailscale2otel/runbooks/#"


# Evaluation policy: how a rule behaves when its query returns nothing, and when
# the query itself fails. (noDataState, execErrState).
#
# CASING. rules.alerting.grafana.app/v0alpha1 spells the no-data OK state "Ok"
# and the exec-error OK state "OK". That is not a typo below and must not be
# "tidied": the provisioning format this generator used to emit spelled both
# "OK", and pushing that spelling makes every affected rule un-deployable while
# every offline check still passes. test_rules.py asserts the exact bytes.
POLICY = {
    # The rule's entire job is to notice that something stopped. Absence IS the
    # alert, and a query error must never read as healthy.
    "coverage_critical": ("Alerting", "Alerting"),
    # The signal is always emitted whenever the exporter is running, so its
    # absence is genuinely abnormal and should surface as a distinct NoData
    # alert rather than silence. A query error surfaces as Error, not as OK.
    "core":             ("NoData",   "Error"),
    # Gated on an optional collector, source or provider capability. Absence
    # means "not configured", which is not a fault -- but a datasource error
    # still is.
    "optional":         ("Ok",       "Error"),
    # Hygiene/advisory. Neither absence nor a transient error is actionable at
    # this rule's severity, and surfacing them would be pure noise.
    "advisory":         ("Ok",       "OK"),
}

# uid -> declared policy name. Populated by alert() as the catalogue is built, so
# tests can assert the emitted pair against what the source DECLARED rather than
# just against the closed set of valid pairs. Not part of the emitted document.
POLICY_BY_UID = {}

# `optional` reads slightly wider than its name: it is the policy for any rule
# whose series is legitimately ABSENT in a healthy deployment. That covers the
# gated collectors it is named for, and also every rule keyed off a counter or a
# state-filtered series that simply has not happened yet — `rate(...errors...)`
# with no errors, `{state="credential_rejected"}` with a working credential. Both
# want (OK, Error): absence is fine, a datasource error is not.


# ---------------------------------------------------------------------------
# tiny stdlib block-YAML emitter (no PyYAML). All string scalars are double
# quoted + escaped, which is always valid YAML and sidesteps special-char rules.
# ---------------------------------------------------------------------------

def _scalar(v):
    if isinstance(v, bool):
        return "true" if v else "false"
    if v is None:
        return "null"
    if isinstance(v, (int, float)):
        return repr(v) if isinstance(v, float) else str(v)
    s = str(v).replace("\\", "\\\\").replace('"', '\\"').replace("\n", "\\n")
    return '"%s"' % s


def yamlify(obj, indent=0):
    pad = "  " * indent
    if isinstance(obj, dict):
        lines = []
        for k, v in obj.items():
            if isinstance(v, dict) and v:
                lines.append("%s%s:" % (pad, k))
                lines.append(yamlify(v, indent + 1))
            elif isinstance(v, list) and v:
                lines.append("%s%s:" % (pad, k))
                lines.append(yamlify(v, indent + 1))
            elif isinstance(v, dict):
                lines.append("%s%s: {}" % (pad, k))
            elif isinstance(v, list):
                lines.append("%s%s: []" % (pad, k))
            else:
                lines.append("%s%s: %s" % (pad, k, _scalar(v)))
        return "\n".join(lines)
    if isinstance(obj, list):
        lines = []
        for item in obj:
            if isinstance(item, (dict, list)) and item:
                block = yamlify(item, indent + 1).split("\n")
                stripped = block[0][(indent + 1) * 2:]
                block = ["%s- %s" % (pad, stripped)] + block[1:]
                lines.extend(block)
            else:
                lines.append("%s- %s" % (pad, _scalar(item)))
        return "\n".join(lines)
    return "%s%s" % (pad, _scalar(obj))


# ---------------------------------------------------------------------------
# rule builders
# ---------------------------------------------------------------------------

def _ds(uid):
    return {"type": ("loki" if uid == LOKI else ("__expr__" if uid == "__expr__" else "prometheus")), "uid": uid}


# --- durations -------------------------------------------------------------
# rules.alerting.grafana.app takes Go duration STRINGS everywhere the
# provisioning format took integer seconds or a bare Prometheus duration. "5m"
# is not accepted; "5m0s" is. Round-tripping through seconds keeps the catalogue
# authored in the short form a human reads.

_DUR_UNITS = {"s": 1, "m": 60, "h": 3600, "d": 86400}
_DUR_RE = re.compile(r"^(\d+)([smhd])$")


def _seconds(dur):
    """Seconds for a short Prometheus-style duration string ("5m", "1h", "0m")."""
    if isinstance(dur, (int, float)):
        return int(dur)
    m = _DUR_RE.match(dur)
    if not m:
        raise ValueError("cannot parse duration %r; use <int><s|m|h|d>" % (dur,))
    return int(m.group(1)) * _DUR_UNITS[m.group(2)]


def godur(dur):
    """Go duration string, formatted the way Grafana itself writes them."""
    total = _seconds(dur)
    if total <= 0:
        return "0s"
    h, rem = divmod(total, 3600)
    m, s = divmod(rem, 60)
    if h:
        return "%dh%dm%ds" % (h, m, s)
    if m:
        return "%dm%ds" % (m, s)
    return "%ds" % s


def _query_node(expr, ds_uid, lookback=3600):
    model = {"datasource": _ds(ds_uid), "editorMode": "code", "expr": expr,
             "instant": False, "range": True, "intervalMs": 1000,
             "maxDataPoints": 43200, "refId": "A"}
    if ds_uid == LOKI:
        model["queryType"] = "range"
    return {"datasourceUID": ds_uid,
            "relativeTimeRange": {"from": godur(lookback), "to": "0s"},
            "model": model}


def _reduce_node():
    # Server-side expression nodes carry no datasourceUID or relativeTimeRange of
    # their own — the __expr__ datasource inside the model is the whole story.
    return {"model": {"datasource": _ds("__expr__"), "expression": "A",
                      "reducer": "last", "type": "reduce", "refId": "B",
                      "intervalMs": 1000, "maxDataPoints": 43200}}


def _threshold_node(op, thr):
    # op: "gt" | "lt" | "within_range" (thr is [lo, hi] for within_range)
    params = list(thr) if isinstance(thr, list) else [thr]
    cond = {"type": "query", "evaluator": {"type": op, "params": params},
            "operator": {"type": "and"}, "query": {"params": []},
            "reducer": {"type": "last", "params": []}}
    # `source: true` marks the node whose result IS the rule's verdict. This API
    # has no separate `condition` field, so without it the rule imports with no
    # condition at all.
    return {"model": {"datasource": _ds("__expr__"), "expression": "B",
                      "type": "threshold", "conditions": [cond], "refId": "C",
                      "intervalMs": 1000, "maxDataPoints": 43200},
            "source": True}


def _labels(severity, domain, page, hygiene):
    lbl = {"severity": severity, "service": "tailscale2otel", "domain": domain}
    if page is not None:
        lbl["page"] = "true" if page else "false"
    # worthy of auto-investigation iff critical OR phone-paging OR a (non-hygiene) security rule
    worthy = (severity == "critical") or bool(page) or (domain == "security" and not hygiene)
    if not worthy:
        lbl["skipinvestigation"] = "true"
    return lbl


# --- generation-time validation of the two cross-artifact links ------------

_RUNBOOK_ANCHOR = re.compile(r"^##\s+.*\{#([a-z0-9-]+)\}\s*$", re.MULTILINE)
_runbook_slugs_cache = None
_panel_index_cache = None


def runbook_slugs():
    """Anchored level-2 section slugs in docs/runbooks.md.

    Only ``## Heading {#slug}`` counts. Prose sections (the intro, the
    datasource/ruler-health caveat) deliberately carry no explicit anchor so they
    are not required to back a rule — zensical's toc still auto-slugs them for
    human navigation, they just are not part of this contract.
    """
    global _runbook_slugs_cache
    if _runbook_slugs_cache is None:
        try:
            with open(RUNBOOK_DOC, encoding="utf-8") as f:
                body = f.read()
        except OSError as err:
            raise ValueError("cannot read the runbook page %s: %s" % (RUNBOOK_DOC, err))
        _runbook_slugs_cache = set(_RUNBOOK_ANCHOR.findall(body))
        if not _runbook_slugs_cache:
            raise ValueError("%s declares no `## Heading {#slug}` sections" % RUNBOOK_DOC)
    return _runbook_slugs_cache


def runbook_url(slug):
    if slug not in runbook_slugs():
        raise ValueError(
            "runbook slug %r has no `## ... {#%s}` section in docs/runbooks.md. Add the section "
            "(or fix the slug) — every alert must link somewhere a human can act on." % (slug, slug))
    return RUNBOOK_BASE + slug


def _panel_index():
    """title -> [panel id, ...] over the GENERATED flagship dashboard.

    Resolution is by TITLE, never by a hard-coded id: ids come from a monotonic
    counter in deploy/grafana/gen/build.py and renumber whenever a panel is
    inserted, so a literal id in this file would rot silently into a link to
    whatever panel inherited the number.
    """
    global _panel_index_cache
    if _panel_index_cache is None:
        try:
            with open(DASHBOARD_DOC, encoding="utf-8") as f:
                doc = json.load(f)
        except (OSError, ValueError) as err:
            raise ValueError("cannot read the generated dashboard %s: %s" % (DASHBOARD_DOC, err))
        index = {}
        for element in doc["spec"]["elements"].values():
            if element.get("kind") != "Panel":
                continue
            index.setdefault(element["spec"]["title"], []).append(element["spec"]["id"])
        if not index:
            raise ValueError("%s contains no panels" % DASHBOARD_DOC)
        _panel_index_cache = (doc["metadata"]["name"], index)
    return _panel_index_cache


def panel_ref(title):
    """(dashboardUid, panelId) for exactly one panel titled `title`.

    Ambiguity is an ERROR, not a pick-first: several titles are reused across
    tabs ("Updates available" appears three times), and silently linking the
    first match sends an on-call engineer to the wrong tab.
    """
    uid, index = _panel_index()
    ids = index.get(title, [])
    if not ids:
        raise ValueError(
            "no panel titled %r in the generated dashboard. Panel titles are the link key, so a "
            "retitled panel must be updated here too." % title)
    if len(ids) > 1:
        raise ValueError(
            "panel title %r is used by %d panels (ids %s). Pick a title unique to the canonical "
            "panel, or qualify the panel's title in deploy/grafana/gen/build.py." %
            (title, len(ids), ", ".join(str(i) for i in ids)))
    return uid, ids[0]


def alert(uid, title, expr, op, thr, dur, severity, summary, desc, *,
          policy, runbook, panel=None,
          ds=PROM, paused=True, lookback=3600,
          domain="observability", page=None, hygiene=False):
    # `policy` is required and has no default on purpose (#388): the old global
    # fail-open default (execErrState: OK everywhere) is exactly the bug, and a
    # default would let the next rule inherit it without anyone deciding.
    if policy not in POLICY:
        raise ValueError("unknown evaluation policy %r for %s; valid: %s"
                         % (policy, uid, ", ".join(sorted(POLICY))))
    nodata, execerr = POLICY[policy]
    POLICY_BY_UID[uid] = policy
    annotations = {"summary": summary, "description": desc,
                   "runbook_url": runbook_url(runbook)}
    if panel is not None:
        # This API has no top-level dashboardUid/panelId — those are the
        # apiVersion: 1 provisioning form, and `additionalProperties: false`
        # rejects them outright. The paired __dashboardUid__/__panelId__
        # annotations are the mechanism here, and __panelId__ must be a STRING:
        # annotation values are typed as strings, so an int is dropped silently.
        dash_uid, panel_id = panel_ref(panel)
        annotations["__dashboardUid__"] = dash_uid
        annotations["__panelId__"] = str(panel_id)
    return {
        # `uid` and `_prom` are GENERATOR-INTERNAL. `uid` becomes metadata.name;
        # `_prom` carries what the test-only Prometheus rendering needs and what
        # `rule_expr()` reads. Neither is emitted into a manifest.
        "uid": uid,
        "title": title,
        "trigger": {"interval": INTERVAL},
        "noDataState": nodata, "execErrState": execerr,
        "for": godur(dur),
        "labels": _labels(severity, domain, page, hygiene),
        "annotations": annotations,
        "paused": paused,
        "expressions": {"A": _query_node(expr, ds, lookback),
                        "B": _reduce_node(),
                        "C": _threshold_node(op, thr)},
        "_prom": {"expr": expr, "op": op, "thr": thr, "ds": ds,
                  "dur": dur, "policy": policy},
    }


def record(uid, metric, expr, desc, ds=PROM, paused=True, domain="observability"):
    # Grafana-managed recording rules must resolve to a single value per series, either via an
    # instant query or a range query + Reduce node. A (range) -> B (reduce, last) -> record B,
    # mirroring alert()'s pipeline; recording from a raw range node (no reduce) is invalid.
    #
    # The v0alpha1 RecordingRule spec is `additionalProperties: false` over exactly
    # {title, trigger, metric, expressions, targetDatasourceUID, labels, paused}.
    # So: no condition, no noDataState/execErrState, no notificationSettings, no
    # `for` — and, unlike the provisioning format, no `annotations` either. The
    # written reason for each recorded metric therefore lives in the internal
    # `_desc` key: it stays in the source (and in test_rules.py's checks) but is
    # never emitted.
    reduce_node = _reduce_node()
    reduce_node["source"] = True
    return {
        "uid": uid,
        "title": metric,
        "metric": metric,
        "targetDatasourceUID": ds,
        "trigger": {"interval": INTERVAL},
        "labels": {"service": "tailscale2otel", "domain": domain},
        "paused": paused,
        "expressions": {"A": _query_node(expr, ds), "B": reduce_node},
        "_desc": desc,
        "_prom": {"expr": expr, "ds": ds},
    }


def is_recording(rule):
    """True for a RecordingRule entry in the catalogue."""
    return "metric" in rule


def rules_by_uid():
    """uid -> catalogue entry, across every group. The stable read API for other
    generators' tests, which must not have to know how a rule is shaped."""
    return {r["uid"]: r for _, group in groups() for r in group}


def rule_expr(rule):
    """The datasource expression a rule queries (the A node's PromQL/LogQL)."""
    return rule["_prom"]["expr"]


def _derp_byte_fraction(win="10m"):
    """Fleet fraction of bytes relayed via DERP, robust to asymmetric inbound-/outbound-only series.

    `rate(in)+rate(out)` is a one-to-one join on all shared labels, so a node/path present in only
    one direction (asymmetric relay traffic) is silently dropped before the outer sum runs. Instead
    union the two directions (disambiguated with a synthetic `dir` label via label_replace) and then
    sum, so no series is lost. Numerator restricts to path="derp"; denominator is all paths."""
    def _u(sel):
        return ('sum(label_replace(rate(tailscaled_inbound_bytes_total%s[%s]), "dir", "in", "", "") '
                'or label_replace(rate(tailscaled_outbound_bytes_total%s[%s]), "dir", "out", "", ""))'
                % (sel, win, sel, win))
    return '%s / clamp_min(%s, 1)' % (_u('{path="derp"}'), _u(''))


# ---------------------------------------------------------------------------
# the catalogue (star = recommended starter set -> enabled; rest paused)
# ---------------------------------------------------------------------------

def groups():
    health = [
        alert("ts2o-exporter-down", "Exporter down",
              "tailscale2otel_up_ratio",
              "lt", 1, "5m", "critical",
              "tailscale2otel exporter is down",
              "tailscale2otel_up_ratio is 0 for 5m — the exporter is not running or not emitting "
              "telemetry, so no Tailscale metrics or logs are flowing. This is the pack's only "
              "coverage_critical rule: BOTH noDataState and execErrState are Alerting, so the series "
              "going fully absent — e.g. an OOM-killed pod — also fires it, matching the "
              "datasource-managed ExporterDown semantics "
              "(absent(tailscale2otel_up_ratio) or == 0). Check the \"Up state\" / build-info panels "
              "on the Exporter Health dashboard (uid ts2otel-exporter-health).",
              domain="observability", paused=False,
              policy="coverage_critical", runbook="exporter-down", panel="Uptime"),
        alert("ts2o-collector-scrape-failing", "Collector scrape failing",
              "min by (tailscale_collector) (tailscale2otel_scrape_success_ratio)",
              "lt", 1, "15m", "warning",
              "Collector {{ $labels.tailscale_collector }} scrape failing",
              "tailscale2otel_scrape_success_ratio == 0 for collector {{ $labels.tailscale_collector }} "
              "for 15m — its last scrape failed and has not recovered, so that collector's series are "
              "stale. Complements CollectorScrapeStale (timestamp-based). See the \"Scrape success / "
              "duration / errors by collector\" panels on the Exporter Health dashboard "
              "(uid ts2otel-exporter-health).",
              domain="observability", paused=False,
              policy="core", runbook="collector-scrape-health", panel="Scrape success by collector"),
        alert("ts2o-collector-scrape-stale", "Collector scrape stale",
              "max by (tailscale_collector) (time() - tailscale2otel_scrape_last_timestamp_seconds)",
              "gt", 3600, "10m", "warning",
              "Collector {{ $labels.tailscale_collector }} has not completed a scrape recently",
              "time() - tailscale2otel_scrape_last_timestamp_seconds > 1h for collector "
              "{{ $labels.tailscale_collector }} — it is wedged (the success gauge can stay stale at 1), "
              "so that collector's series are not refreshing. Complements CollectorScrapeFailing.",
              domain="observability", paused=False,
              policy="core", runbook="collector-scrape-health", panel="Last scrape age"),
        # The flapping half of collector scrape health. NOT redundant with
        # ts2o-collector-scrape-failing above: that rule reads the LAST scrape's
        # success gauge, so a collector alternating fail/succeed presents a `1`
        # to most evaluations and never sustains the 15m `for` — while half its
        # scrapes are being lost. This counter only moves on a failure, so it
        # sees the flap, and `error_type` says what kind. Restored from the
        # deleted datasource-managed CollectorScrapeErrorRateHigh, which shipped
        # alongside CollectorScrapeFailing for exactly this reason.
        alert("ts2o-collector-scrape-error-rate", "Collector scrape error rate high",
              "sum by (tailscale_collector) (rate(tailscale2otel_scrape_errors_total[5m]))",
              "gt", 0, "15m", "warning",
              "Collector {{ $labels.tailscale_collector }} scrape errors elevated",
              "rate(tailscale2otel_scrape_errors_total[5m]) > 0 for collector "
              "{{ $labels.tailscale_collector }} for 15m — it is logging scrape errors. Distinct from "
              "ts2o-collector-scrape-failing, which reads the last-scrape success gauge and therefore "
              "goes quiet for a collector that fails and recovers on alternating scrapes; this one "
              "catches that flap. Break down by error_type on \"Scrape errors/s by collector / type\". "
              "The counter is absent until the first error, so the policy is optional, not core.",
              domain="observability", paused=False,
              policy="optional", runbook="collector-scrape-health",
              panel="Scrape errors/s by collector / type"),
        alert("ts2o-metric-cardinality-capped", "Metric cardinality capped",
              "max(tailscale2otel_series_overflowing_ratio)",
              "gt", 0, "5m", "warning",
              "A tailscale2otel metric hit the per-metric series cap",
              "tailscale2otel_series_overflowing_ratio > 0 — one or more metrics are overflowing the "
              "per-metric series cap (cardinality.metric_limit); excess series are collapsed into "
              "otel_metric_overflow, i.e. SILENT per-series loss. Usually ephemeral source_port. Raise "
              "metric_limit or lower flow cardinality.",
              domain="observability", paused=False,
              policy="optional", runbook="cardinality-and-series-budget", panel="Metrics overflowing now"),
        alert("ts2o-series-budget-high", "Series budget high",
              "max by (metric_name)(tailscale2otel_series_active) / on() group_left() "
              "max(tailscale2otel_series_limit)",
              "gt", 0.8, "10m", "warning",
              "Busiest tailscale2otel metric approaching the per-metric series cap",
              "A metric family is using >80% of its per-metric series budget "
              "(series_active / series_limit > 0.8); MetricCardinalityCapped fires once it overflows.",
              domain="observability", paused=False,
              policy="optional", runbook="cardinality-and-series-budget", panel="Per-metric headroom (top-N)"),
        # Keyed off the classified availability state, not the raw status code
        # (#420). The old rule fired critical on ANY 401 or 403 and declared "all
        # polling fails" — but a legitimate 403 is how upstream reports an optional
        # feature the tailnet does not have, so a tailnet simply lacking premium
        # flow logs paged someone. 401 (credential rejected) is the tailnet-wide
        # emergency; 403 (scope denied) is a real but narrower misconfiguration.
        alert("ts2o-api-credential-rejected", "Tailscale API credential rejected",
              'max(tailscale2otel_api_availability_ratio{tailscale_api_state="credential_rejected"})',
              "gt", 0, "10m", "critical",
              "Tailscale API returning 401 — the credential is invalid, expired or revoked",
              "The Tailscale API is rejecting the exporter's credential outright (HTTP 401), so polling "
              "fails tailnet-wide and every metric goes stale. Rotate or re-issue the OAuth client or "
              "API key. This is distinct from a scope denial — see ts2o-api-scope-denied.",
              domain="security", paused=False,
              policy="optional", runbook="tailscale-api-health", panel="API requests/s by status"),
        alert("ts2o-api-scope-denied", "Tailscale API operation denied by scope",
              'max(tailscale2otel_api_availability_ratio{tailscale_api_state="scope_denied"})',
              "gt", 0, "30m", "warning",
              "An API operation is returning 403 — the credential lacks its scope",
              "The credential is valid but is being refused a specific operation (HTTP 403), so that "
              "collector's signals are silently missing while everything else keeps working. Widen the "
              "OAuth scope or disable the collector. Note this is NOT the same as a feature the tailnet "
              "does not have — that reports as `disabled` and is expected, not alertable.",
              domain="security", paused=False,
              policy="optional", runbook="tailscale-api-health", panel="API requests/s by status"),
        alert("ts2o-api-rate-limited", "Tailscale API rate limited",
              'sum(rate(tailscale2otel_api_requests_total{http_response_status_code="429"}[10m]))',
              "gt", 0, "10m", "warning",
              "Tailscale API returning 429 (rate limited)",
              "Sustained HTTP 429 from the Tailscale API — polling is being throttled. Increase poll "
              "intervals or reduce the number of enabled collectors.",
              domain="observability", paused=True,
              policy="optional", runbook="tailscale-api-health", panel="API 429 / retries"),
        alert("ts2o-api-server-errors", "Tailscale API server errors",
              'sum(rate(tailscale2otel_api_requests_total{http_response_status_code=~"5.."}[10m]))',
              "gt", 0.05, "15m", "warning",
              "Tailscale API returning 5xx",
              "Sustained HTTP 5xx from the Tailscale API (>0.05/s) — Tailscale-side instability; the "
              "exporter retries but data may be delayed.",
              domain="observability", paused=True,
              policy="optional", runbook="tailscale-api-health", panel="API requests/s by status"),
        alert("ts2o-api-retries-elevated", "Tailscale API retries elevated",
              "sum(rate(tailscale2otel_api_retries_total[10m]))", "gt", 0.1, "15m", "warning",
              "Elevated Tailscale API retry rate",
              "Sustained API retry rate (>0.1/s) — flakiness/backoff against the Tailscale API. Break down "
              "by endpoint on the Exporter Diagnostics tab.",
              domain="observability", paused=True,
              policy="optional", runbook="tailscale-api-health", panel="API retries/s by endpoint"),
        alert("ts2o-checkpoint-persist-errors", "Checkpoint persist errors",
              "sum by (tailscale_collector) (rate(tailscale2otel_checkpoint_persist_errors_total[15m]))",
              "gt", 0, "15m", "warning",
              "Collector {{ $labels.tailscale_collector }} cannot persist its checkpoint",
              "rate(tailscale2otel_checkpoint_persist_errors_total) > 0 for {{ $labels.tailscale_collector }} "
              "— the scrape window succeeded but its high-water mark could not be saved, risking replay/"
              "duplicate emission on restart. Check the checkpoint file path/permissions.",
              domain="observability", paused=False,
              policy="optional", runbook="checkpoint-health", panel="Checkpoint persist errors/s"),
        alert("ts2o-component-errors", "Component errors",
              "sum by (component) (rate(tailscale2otel_component_errors_total[15m]))",
              "gt", 0, "15m", "warning",
              "tailscale2otel component {{ $labels.component }} erroring",
              "A non-collector subsystem ({{ $labels.component }} — receivers, admin server, streaming "
              "auto-configure) is logging errors. See the Reliability row on the Exporter Diagnostics tab.",
              domain="observability", paused=False,
              policy="optional", runbook="exporter-internal-errors", panel="Component errors/s"),
        alert("ts2o-dedup-set-saturated", "Dedup set saturated",
              "sum by (dedup_set) (rate(tailscale2otel_dedup_evictions_total[15m]))",
              "gt", 0, "15m", "warning",
              "Cross-source dedup set {{ $labels.dedup_set }} is evicting",
              "Cross-source dedup set {{ $labels.dedup_set }} is evicting keys. NOTE: steady-state "
              "evictions are normal — dedup keys are effectively unique, so a full fixed-size set evicts "
              "one key per insert forever even in a healthy deployment. This rule ships PAUSED precisely "
              "because a raw evictions rate > 0 is not actionable on its own. The real overflow signal is "
              "evictions approaching the set's capacity within a single poll interval (overlap keys aged "
              "out before the next poll dedups against them → boundary double-counting); only enable this "
              "with a threshold tuned to your poll interval and set size.",
              domain="observability", paused=True,
              policy="advisory", runbook="exporter-internal-errors", panel="Dedup set fill"),
        alert("ts2o-enrich-cache-stale", "Enrichment cache stale",
              "max(tailscale2otel_enrich_cache_age_seconds)", "gt", 3600, "15m", "warning",
              "Device-enrichment cache is stale",
              "The IP/nodeID->name enrichment cache has not refreshed in over 1h — flow/audit name "
              "resolution is degrading to unknown/external. The devices collector populates this cache; "
              "check it is enabled and scraping.",
              domain="observability", paused=False,
              policy="optional", runbook="enrichment-and-discovery", panel="Enrich cache age"),
        alert("ts2o-nodemetrics-discovery-failing", "Node-metrics discovery failing",
              "max(tailscale2otel_nodemetrics_discovery_success_ratio)", "lt", 1, "10m", "warning",
              "Node-metrics dynamic discovery is failing",
              "tailscale2otel_nodemetrics_discovery_success_ratio < 1 — the last dynamic target-discovery "
              "refresh failed, so the node-metrics target list is stale. (Absent when discovery is "
              "disabled => this never fires.)",
              domain="infra", paused=True,
              policy="optional", runbook="enrichment-and-discovery", panel="Discovery OK"),
        alert("ts2o-admin-auth-rejections-high", "Admin auth rejections high",
              "sum(rate(tailscale2otel_admin_auth_rejected_total[10m]))", "gt", 0.2, "10m", "info",
              "Elevated admin auth rejections",
              "Sustained admin HTTP auth rejections (>0.2/s) on the status page / pprof endpoint — possible "
              "probing or a misconfigured admin token.",
              domain="observability", paused=True,
              policy="advisory", runbook="exporter-internal-errors", panel="Admin auth rejected/s"),
        alert("ts2o-gc-cpu-fraction-high", "GC CPU fraction high",
              "max(tailscale2otel_runtime_gc_cpu_fraction_ratio)", "gt", 0.25, "15m", "info",
              "Go GC using a high CPU fraction",
              "runtime gc.cpu_fraction > 0.25 — GC is taking a large share of CPU. Note: this exporter is "
              "near-idle, so the fraction can be high against a tiny absolute; check absolute CPU first.",
              domain="observability", paused=True,
              policy="advisory", runbook="exporter-internal-errors", panel="GC CPU fraction"),
        # --- Task 2.2: new self-obs alerts (C2/C3/C9) ---
        alert("ts2o-export-latency-high", "Export latency high",
              "histogram_quantile(0.99, sum by (le, signal) "
              "(rate(tailscale2otel_export_duration_seconds_bucket[10m])))",
              "gt", 2, "10m", "warning",
              "OTLP export p99 latency is high",
              "p99 OTLP export duration > 2s for a signal — the backend is slow or unreachable, so exports "
              "are backing up. Break down by signal on the Exporter Diagnostics tab.",
              domain="observability", paused=True,
              policy="core", runbook="otlp-export-health", panel="Export latency p50/p95/p99 by signal"),
        alert("ts2o-export-failures", "Export failures",
              "sum by (outcome) (rate(tailscale2otel_export_duration_seconds_count{outcome=\"failure\"}[10m]))",
              "gt", 0, "10m", "warning",
              "OTLP exports are failing",
              "rate of failed OTLP export attempts > 0 — datapoints/logs are not reaching the backend. "
              "Complements export.failures; check OTLP endpoint/credentials.",
              domain="observability", paused=True,
              policy="optional", runbook="otlp-export-health", panel="Export failures/s by type"),
        # The SDK-handler half of export health, restored from the deleted
        # datasource-managed OTLPExportFailures. ts2o-export-failures above reads
        # the export DECORATORS (export.duration{outcome="failure"}, keyed by
        # signal); this reads tailscale2otel.export.failures, incremented from
        # the OTEL global error handler, so it also sees failures that never came
        # from a decorated Export() call and classifies them by error_type
        # (timeout vs export) rather than by signal. selfobs.go is written
        # against the assumption that alerts watch THIS counter. Neither is a
        # superset of the other, so this is an addition, not a widening.
        alert("ts2o-otlp-export-failures", "OTLP export failures",
              "sum by (error_type) (rate(tailscale2otel_export_failures_total[10m]))",
              "gt", 0, "15m", "warning",
              "tailscale2otel OTLP export failures ({{ $labels.error_type }})",
              "rate(tailscale2otel_export_failures_total[10m]) > 0 for 15m — the exporter is failing "
              "to push OTLP to the collector/backend (error_type {{ $labels.error_type }}: `timeout` "
              "is a deadline, `export` is everything else), so metrics and logs are being lost in "
              "transit. This counter is fed by the OTEL SDK's global error handler, which deliberately "
              "does NOT count ErrInstrumentName (nothing is dropped there). Ships ENABLED: silent "
              "telemetry loss has no other symptom, and the counter is absent until the first failure, "
              "so a healthy deployment never sees it.",
              domain="observability", paused=False,
              policy="optional", runbook="otlp-export-health", panel="Export failures/s"),
        alert("ts2o-scrape-staleness-high", "Scrape staleness high",
              "max by (tailscale_collector) (tailscale2otel_scrape_staleness_seconds)",
              "gt", 1800, "10m", "warning",
              "Collector {{ $labels.tailscale_collector }} scrape data is stale",
              "tailscale2otel_scrape_staleness_seconds > 30m for {{ $labels.tailscale_collector }} — its "
              "series have not refreshed recently. Friendlier framing of CollectorScrapeStale.",
              domain="observability", paused=True,
              policy="core", runbook="collector-scrape-health", panel="Scrape staleness"),
        alert("ts2o-scrape-budget-overrun", "Scrape budget overrun",
              "max by (tailscale_collector) (tailscale2otel_scrape_budget_ratio)",
              "gt", 1, "15m", "warning",
              "Collector {{ $labels.tailscale_collector }} is exceeding its scrape budget",
              "tailscale2otel_scrape_budget_ratio > 1 for {{ $labels.tailscale_collector }} — the scrape is "
              "taking longer than its interval, so it cannot keep up. Increase the interval or reduce work.",
              domain="observability", paused=True,
              policy="core", runbook="collector-scrape-health", panel="Scrape budget headroom"),
        alert("ts2o-config-warnings", "Config warnings present",
              "max(tailscale2otel_config_warnings_ratio)", "gt", 0, "15m", "info",
              "tailscale2otel has configuration warnings",
              "tailscale2otel_config_warnings_ratio > 0 — the loaded config produced advisory warnings "
              "(e.g. API-key auth, poll+stream overlap). Review startup logs / the Warnings() output.",
              domain="observability", paused=False,
              policy="core", runbook="exporter-config-health", panel="Config warnings"),
        alert("ts2o-config-invalid", "Config invalid",
              "max(tailscale2otel_config_valid_ratio)", "lt", 1, "5m", "critical",
              "tailscale2otel config is invalid",
              "tailscale2otel_config_valid_ratio < 1 — the running config failed validation. This normally "
              "fails startup, so seeing it at runtime is rare and serious; inspect the config.",
              domain="observability", paused=True,
              policy="core", runbook="exporter-config-health", panel="Config valid"),
        alert("ts2o-checkpoint-stalled", "Checkpoint persist stalled",
              "max(tailscale2otel_checkpoint_persist_age_seconds)", "gt", 1800, "15m", "warning",
              "Checkpoint has not been persisted recently",
              "tailscale2otel_checkpoint_persist_age_seconds > 30m — the high-water-mark checkpoint is not "
              "being saved, risking replay/duplicate emission on restart. Check the checkpoint store.",
              domain="observability", paused=True,
              policy="optional", runbook="checkpoint-health", panel="Checkpoint persist age"),
        alert("ts2o-export-volume-high", "Export volume high",
              "rate(tailscale2otel_export_datapoints_total[10m])", "gt", 5000, "15m", "info",
              "Exported datapoint rate is high",
              "Exported datapoints/s exceed the configured budget (default 5000/s here) — an ingest-cost "
              "signal. Tune this threshold to your Grafana Cloud plan; lower flow cardinality if needed.",
              domain="observability", paused=True,
              policy="advisory", runbook="cardinality-and-series-budget", panel="Ingest vs export cost (per minute)"),
        alert("ts2o-exporter-update-available", "Exporter update available",
              "max(tailscale2otel_update_available_ratio)", "gt", 0, "1h", "info",
              "A tailscale2otel exporter update is available",
              "tailscale2otel_update_available_ratio > 0 — a newer tailscale2otel release is available. "
              "Informational; absent on dev builds.",
              domain="observability", paused=True,
              policy="advisory", runbook="exporter-config-health", panel="Exporter update available"),
    ]

    security = [
        alert("ts2o-tailnet-lock-errors", "Tailnet-lock errors",
              "max(tailscale_tailnet_lock_errors_ratio)", "gt", 0, "10m", "warning",
              "Devices have tailnet-lock errors",
              "tailscale_tailnet_lock_errors_ratio > 0 — one or more devices have a non-empty tailnet-lock "
              "error (e.g. an unsigned node); a signing node must sign the affected keys. See the Tailnet "
              "lock row on the Security & Audit tab.",
              domain="security", paused=False,
              policy="optional", runbook="tailnet-lock", panel="Nodes with tailnet-lock errors"),
        alert("ts2o-audit-config-change-warn", "Audit config change (WARN)",
              "sum(count_over_time({service_name=\"tailscale2otel\"} | event_name=`tailscale.config.audit` "
              "| severity_text=`WARN` [10m]))",
              "gt", 0, "5m", "warning",
              "Configuration-audit event carried an error",
              "A tailscale.config.audit log was emitted at WARN (the change carried an error) in the last "
              "10m. Inspect the Configuration audit row on the Security & Audit tab.",
              ds=LOKI, domain="security", paused=False,
              policy="optional", runbook="audit-events", panel="Failed changes — WARN (range)"),
        alert("ts2o-device-key-expiring-critical", "Tagged device key expiring (<48h)",
              "max by (host_name, host_id, tailscale_user, tailscale_tags) "
              "(tailscale_device_key_expiry_seconds{tailscale_tags!=\"\"}) - time()",
              "within_range", [0, 172800], "1h", "critical",
              "Tagged device node key for {{ $labels.host_name }} expires in <48h",
              "The Tailscale node key for the TAGGED device {{ $labels.host_name }} "
              "(tags {{ $labels.tailscale_tags }}) expires within 48h. When it expires the device drops "
              "off the tailnet until re-authed, and a tagged node is typically headless/unattended — "
              "nobody is sitting in front of it to answer the client's reauth prompt, so this needs a "
              "human to go and re-auth it. Deliberately restricted to tagged devices: Tailscale disables "
              "key expiry on tagged devices by default, so a tagged device with a live expiry has had it "
              "re-enabled explicitly and is a real impending outage. UNTAGGED (user-owned) devices are "
              "excluded on purpose — there the Tailscale client warns the signed-in user and the fix is "
              "an interactive browser reauth, which is routine rather than pageable; they are covered by "
              "the warning-tier DeviceKeysExpiring7d instead. Note the underlying gauge is only emitted "
              "for devices WITHOUT key expiry disabled, so devices in the normal tagged default state "
              "never produce a series at all.",
              domain="security", paused=False,
              policy="optional", runbook="credential-expiry", panel="Device key expiry (time until)"),
        alert("ts2o-auth-key-expiring-critical", "Auth/API key expiring (<48h)",
              "max by (tailscale_key_id, tailscale_key_type, tailscale_key_description) "
              "(tailscale_key_expiry_seconds) - time()",
              "within_range", [0, 172800], "1h", "critical",
              "Auth/API key {{ $labels.tailscale_key_id }} expires in <48h",
              "Tailscale key {{ $labels.tailscale_key_id }} ({{ $labels.tailscale_key_type }}) expires "
              "within 48h — rotate it before automation/devices using it lose access. Critical tier on top "
              "of the 7-day AuthKeyExpiringSoon warning.",
              domain="security", paused=False,
              policy="optional", runbook="credential-expiry", panel="Key expiry (time until)"),
        alert("ts2o-posture-autoupdate-low", "Posture: auto-update coverage low",
              "count(max by (host_id) (tailscale_device_posture_ratio{auto_update=\"true\"})) / "
              "clamp_min(count(max by (host_id) (tailscale_device_posture_ratio)), 1)",
              "lt", 0.8, "1h", "warning",
              "Fleet auto-update coverage below 80%",
              "Fewer than 80% of devices report Tailscale client auto-update enabled. Gated by "
              "collect_posture; absent => not firing.",
              domain="security", hygiene=True, paused=False,
              policy="optional", runbook="device-posture-coverage", panel="Auto-update coverage"),
        alert("ts2o-posture-encryption-low", "Posture: state-encryption coverage low",
              "count(max by (host_id) (tailscale_device_posture_ratio{encrypted=\"true\"})) / "
              "clamp_min(count(max by (host_id) (tailscale_device_posture_ratio)), 1)",
              "lt", 0.8, "1h", "warning",
              "Fleet state-encryption coverage below 80%",
              "Fewer than 80% of devices report an encrypted local state store. Gated by collect_posture.",
              domain="security", hygiene=True, paused=True,
              policy="optional", runbook="device-posture-coverage", panel="State-encryption coverage"),
        alert("ts2o-devices-needing-update", "Many devices need updates",
              "count(max by (host_id) (tailscale_device_update_available_ratio) == 1)",
              "gt", 5, "30m", "info",
              "More than 5 devices have a client update available",
              "count(tailscale_device_update_available_ratio == 1) > 5 — several clients are behind on the "
              "Tailscale client. Informational; surfaces fleet update drift.",
              domain="security", hygiene=True, paused=True,
              policy="advisory", runbook="fleet-version-hygiene", panel="Devices needing update"),
        alert("ts2o-contact-unverified", "Tailnet contact unverified",
              "max(tailscale_contact_needs_verification_ratio)", "gt", 0, "30m", "warning",
              "A tailnet contact needs verification",
              "tailscale_contact_needs_verification_ratio > 0 — a tailnet contact (account/support/security) "
              "is unverified, so Tailscale security notifications to it may not be delivered. Verify it in "
              "the admin console.",
              domain="security", hygiene=True, paused=False,
              policy="optional", runbook="tailnet-settings-drift", panel="Contact needs verification"),
        # --- Task 2.3: fleet-hygiene (security) ---
        # --- WU12b: version skew ---
        alert("ts2o-device-version-skew-high", "Devices far behind latest version",
              "max(tailscale_device_version_skew_ratio)",
              "gt", 3, "1h", "info",
              "At least one device is >3 minor versions behind the fleet latest",
              "at least one device is >3 minor versions behind the fleet latest.",
              domain="security", hygiene=True, paused=True,
              policy="advisory", runbook="fleet-version-hygiene", panel="Most-behind devices (top-N)"),
        alert("ts2o-devices-outdated", "Many devices outdated",
              "max(tailscale_devices_outdated_ratio)", "gt", 5, "1h", "info",
              "More than 5 devices are running an outdated client",
              "tailscale_devices_outdated_ratio > 5 — several clients are N+ minor versions behind the "
              "latest. Informational fleet hygiene; surfaces update drift.",
              domain="security", hygiene=True, paused=True,
              policy="advisory", runbook="fleet-version-hygiene", panel="Outdated (≥N behind)"),
        alert("ts2o-device-keys-expiring-7d", "Device keys expiring (<7d)",
              "count((max by (host_id) (tailscale_device_key_expiry_seconds) - time() < 7*86400) and "
              "(max by (host_id) (tailscale_device_key_expiry_seconds) - time() > 0))",
              "gt", 0, "1h", "warning",
              "Device node keys are expiring within 7 days",
              "One or more device node keys expire within 7 days (and are not already expired) — they will "
              "drop off the tailnet until re-authed. Computed from the per-device key-expiry gauge "
              "(expiry - now within (0, 7d]), so it clears after a key is rotated. Mirrors the "
              "ts2o-rec-keys-expiring-7d recording rule (NOT the cumulative key-expiry histogram buckets, "
              "which only grow and latch). Counts BOTH tagged and untagged devices — this is the only "
              "tier that covers untagged (user-owned) devices, whose reauth is an interactive prompt to "
              "the signed-in user and so is deliberately not pageable; the critical 48h tier "
              "(ts2o-device-key-expiring-critical) is restricted to tagged devices.",
              domain="security", paused=False,
              policy="optional", runbook="credential-expiry", panel="Devices by days-to-expiry bucket"),
        alert("ts2o-device-attribute-expiring-14d", "Device posture attribute expiring (<14d)",
              "count((max by (host_id, attribute) (tailscale_device_attribute_expiry_seconds) - time() < 14*86400) and "
              "(max by (host_id, attribute) (tailscale_device_attribute_expiry_seconds) - time() > 0))",
              "gt", 0, "1h", "warning",
              "Device posture attributes are expiring within 14 days",
              "One or more device posture attributes (set with an expiry, e.g. a custom: namespace "
              "attribute) expire within 14 days (and are not already expired) — an expired attribute "
              "silently breaks posture-based ACLs. Matches the tailscale.device.attribute.expiring WARN "
              "log lead. Computed from the per-device-attribute expiry gauge (expiry - now within "
              "(0, 14d]), so it clears once the attribute is refreshed. Gated by collect_posture and "
              "attribute_namespaces; absent when no attribute carries an expiry.",
              domain="security", paused=False,
              policy="optional", runbook="credential-expiry"),
        alert("ts2o-auth-keys-expiring-7d", "Auth/API keys expiring (<7d)",
              "count((max by (tailscale_key_id) (tailscale_key_expiry_seconds) - time() < 7*86400) and "
              "(max by (tailscale_key_id) (tailscale_key_expiry_seconds) - time() > 0))",
              "gt", 0, "1h", "warning",
              "Auth/API keys are expiring within 7 days",
              "One or more auth/API keys expire within 7 days (and are not already expired) — rotate them "
              "before automation/devices using them lose access. Computed from the per-key expiry gauge "
              "(expiry - now within (0, 7d]), so it clears after a key is rotated. Warning tier below "
              "ts2o-auth-key-expiring-critical (48h) — the Grafana-managed equivalent of the "
              "datasource-managed AuthKeyExpiringSoon rule.",
              domain="security", paused=False,
              policy="optional", runbook="credential-expiry", panel="API/auth keys ≤7d"),
        # --- Task 2.4: security/governance (B1/B2/B7/A1/A2) ---
        alert("ts2o-acl-unrestricted", "Unrestricted ACL rules present",
              "sum(tailscale_acl_unrestricted_rules_ratio)", "gt", 0, "15m", "warning",
              "The tailnet policy has unrestricted (wide-open) rules",
              "sum(tailscale_acl_unrestricted_rules_ratio) > 0 — one or more ACL grants/rules are "
              "unrestricted (e.g. * to *). Review the ACL hygiene row on the Security & Audit tab.",
              domain="security", paused=False,
              policy="optional", runbook="acl-policy-hygiene", panel="Unrestricted ACL rules"),
        alert("ts2o-acl-autoapprove-exit", "ACL auto-approves exit nodes",
              "sum(tailscale_acl_autoapprovers_ratio{tailscale_acl_autoapprover_kind=\"exit_node\"})",
              "gt", 0, "15m", "warning",
              "The tailnet policy auto-approves exit nodes",
              "An ACL autoApprovers stanza auto-approves exit nodes — new exit nodes go live without manual "
              "review. Confirm this is intended.",
              domain="security", paused=True,
              policy="optional", runbook="acl-policy-hygiene", panel="Auto-approvers by kind"),
        alert("ts2o-secret-scanner-fired", "Secret scanner fired",
              "sum(rate(tailscale_config_audit_changes_total{tailscale_actor_type=\"SECRET_SCANNER\"}[15m]))",
              "gt", 0, "0m", "critical",
              "Tailscale secret scanner detected an exposed credential",
              "An audit change was attributed to the SECRET_SCANNER actor — Tailscale's secret scanner "
              "acted on a leaked credential (e.g. revoked an exposed key). Investigate immediately.",
              domain="security", paused=False,
              policy="optional", runbook="audit-events", panel="Changes by actor type"),
        alert("ts2o-tailnet-lock-disabled", "Tailnet lock disabled",
              "sum(rate(tailscale_config_audit_changes_total{tailscale_audit_change=\"tailnet_lock\", "
              "tailscale_audit_action=\"DISABLE\"}[15m]))",
              "gt", 0, "0m", "critical",
              "Tailnet lock was disabled",
              "An audit event disabled tailnet lock — node-key signing enforcement is off, weakening the "
              "tailnet's trust model. Confirm this was an authorized change.",
              domain="security", paused=True,
              policy="optional", runbook="tailnet-lock", panel="Security/lifecycle changes/s"),
        alert("ts2o-user-role-escalation", "User role change",
              "sum(rate(tailscale_config_audit_changes_total{tailscale_audit_change=\"user_role\"}[15m]))",
              "gt", 0, "5m", "warning",
              "A user role was changed",
              "An audit event changed a user's role (e.g. member -> admin/owner) — a privilege change worth "
              "reviewing. Break down by actor on the Security & Audit tab.",
              domain="security", paused=True,
              policy="optional", runbook="audit-events", panel="Top $topn actors over time"),
        alert("ts2o-acl-changed", "ACL changed",
              "sum(rate(tailscale_config_audit_changes_total{tailscale_audit_change=\"acl\"}[15m]))",
              "gt", 0, "5m", "info",
              "The tailnet ACL policy was changed",
              "An audit event modified the ACL/policy file — informational change tracking. Pair with the "
              "config-change row to see who/what changed.",
              domain="security", hygiene=True, paused=True,
              policy="advisory", runbook="acl-policy-hygiene", panel="ACL last changed"),
        # Scope COUNT was the wrong signal and inverted the answer (#415): a single
        # `all` scope grants unrestricted read+write over the whole tailnet,
        # including APIs that do not exist yet, and scored 1 — never firing. Eleven
        # narrow `*:read` scopes scored 11 and fired, despite being far safer. Key
        # off the privilege CLASS instead; `all` is the only class that is both
        # tailnet-wide and write-capable.
        alert("ts2o-key-broad-scope", "Key with unrestricted write scope",
              'max(tailscale_key_scope_class_ratio{tailscale_key_scope_class="all"})',
              "gt", 0, "1h", "info",
              "A credential holds the unrestricted `all` scope",
              "A credential grants the `all` scope — unrestricted read AND write across the entire "
              "tailnet, including APIs Tailscale adds in future. This is the maximum blast radius a "
              "credential can have. Review the credential scopes table on the Policy & Config tab and "
              "scope it down; `all:read` is the least-privilege equivalent for a read-only integration.",
              domain="security", hygiene=True, paused=True,
              policy="advisory", runbook="credential-scope-hygiene", panel="Credential scopes (top-N)"),
        # Companion hygiene signal: a credential that can create auth keys with no
        # tag restriction can mint a node with any tags at all (#416).
        alert("ts2o-key-unrestricted-tags", "Trust credential has no tag restriction",
              'max(tailscale_key_tag_scope_ratio{tailscale_key_tag_scope="none"})',
              "gt", 0, "1h", "info",
              "An OAuth client's trust credential is unrestricted by tags",
              "An OAuth client carries no top-level tag restriction, so auth keys it mints are not "
              "confined to a tag it owns. Scope it to the tags it actually needs.",
              domain="security", hygiene=True, paused=True,
              policy="advisory", runbook="credential-scope-hygiene", panel="Preauthorized auth keys"),
        alert("ts2o-device-share-exit-node", "Device share grants exit node",
              "sum(tailscale_device_invites_count_ratio{tailscale_device_invite_allow_exit_node=\"true\"})",
              "gt", 0, "30m", "warning",
              "A device share allows exit-node use",
              "An outstanding device invite/share allows the recipient to use the device as an exit node — "
              "review whether routing the recipient's traffic is intended.",
              domain="security", paused=True,
              policy="optional", runbook="device-sharing", panel="Exit-node-granting shares"),
        # --- #172: config-change / posture-state detection (audit + feature/setting gauges) ---
        alert("ts2o-flow-logging-disabled", "Network flow logging disabled",
              'min(tailscale_feature_enabled_ratio{tailscale_feature="network_flow_logging"})',
              "lt", 1, "30m", "warning",
              "Tailnet network flow logging is disabled",
              "tailscale_feature_enabled_ratio{tailscale_feature=\"network_flow_logging\"} == 0 — the "
              "tailnet is not exporting network flow logs, so flow-based forensics/audit data is not "
              "being captured. Ships PAUSED because many tailnets legitimately run without flow logging "
              "(a paid feature); enable it if flow logging is expected for this tailnet. Absent "
              "(=> not firing) when the flowlogs collector is disabled entirely. Grafana-managed "
              "equivalent of the datasource-managed FlowLoggingDisabled rule.",
              domain="security", hygiene=True, paused=True,
              policy="optional", runbook="tailnet-settings-drift", panel="Flow logging"),
        alert("ts2o-device-approval-disabled", "Device approval disabled",
              'min(tailscale_setting_enabled_ratio{tailscale_setting_name="devices_approval"})',
              "lt", 1, "30m", "warning",
              "Tailnet device approval is disabled",
              "tailscale_setting_enabled_ratio{tailscale_setting_name=\"devices_approval\"} == 0 — new "
              "devices can join the tailnet without manual admin approval. Ships PAUSED because device "
              "approval is off by default in Tailscale and many tailnets run that way intentionally; "
              "enable this alert if your tailnet requires device approval. Absent (=> not firing) when "
              "the settings collector is disabled.",
              domain="security", hygiene=True, paused=True,
              policy="optional", runbook="tailnet-settings-drift", panel="Tailnet scorecard"),
        alert("ts2o-logstream-config-changed", "Log-streaming (SIEM export) config changed",
              'sum(rate(tailscale_config_audit_changes_total{tailscale_audit_change="logstream_endpoint"}[15m]))',
              "gt", 0, "0m", "warning",
              "A configuration-log / SIEM streaming endpoint was changed",
              "An audit event changed a LOGSTREAM_ENDPOINT setting — a tailnet log-streaming (SIEM) sink "
              "was added, reconfigured, or removed. Removing/disabling it is a forensics/compliance gap. "
              "Audit-driven, so it fires on the change itself (catching a disable even if quickly "
              "reverted); pair with ts2o-logstream-delivery-failing (delivery health). Ships PAUSED — "
              "enable where log-export changes must be reviewed.",
              domain="security", hygiene=True, paused=True,
              policy="optional", runbook="tailnet-settings-drift", panel="Streams configured"),
    ]

    integrations = [
        alert("ts2o-posture-integration-stale", "Posture integration sync stale",
              "max by (tailscale_posture_provider, tailscale_posture_integration) "
              "(time() - tailscale_posture_integration_last_sync_seconds)",
              "gt", 86400, "1h", "warning",
              "Posture integration {{ $labels.tailscale_posture_provider }} sync is stale",
              "A device-posture (MDM/EDR) integration has not synced in over 24h "
              "({{ $labels.tailscale_posture_provider }}/{{ $labels.tailscale_posture_integration }}) — "
              "posture data from it is stale. Absent until an integration exists. NOTE: Tailscale updates "
              "last_sync on every sync *attempt*, including failures, so this alert cannot detect a "
              "persistently-failing-but-still-retrying integration (bad/revoked credentials) — see "
              "ts2o-posture-integration-error for that case (#99).",
              domain="security", paused=False,
              policy="optional", runbook="posture-integrations", panel="Oldest sync age"),
        alert("ts2o-posture-integration-error", "Posture integration sync failing",
              "max by (tailscale_posture_provider, tailscale_posture_integration) "
              "(tailscale_posture_integration_error_ratio)",
              "gt", 0, "15m", "warning",
              "Posture integration {{ $labels.tailscale_posture_provider }} sync is failing",
              "The device-posture (MDM/EDR) integration {{ $labels.tailscale_posture_provider }}/"
              "{{ $labels.tailscale_posture_integration }} reported a sync error (e.g. revoked credentials, "
              "an expired OAuth grant) on its status.error field. Unlike the staleness alert, this fires "
              "even while Tailscale keeps retrying and refreshing last_sync, so it catches a "
              "broken-but-retrying integration the staleness alert structurally cannot (#99). Absent until "
              "an integration exists AND the collector decodes/emits status.error as this gauge.",
              domain="security", paused=False,
              policy="optional", runbook="posture-integrations", panel="Integration sync detail"),
        alert("ts2o-logstream-delivery-failing", "Log-stream delivery failing",
              "sum by (tailscale_logstream_type) (rate(tailscale_logstream_requests_failed_total[15m]))",
              "gt", 0, "15m", "warning",
              "Log streaming to the SIEM sink is failing ({{ $labels.tailscale_logstream_type }})",
              "rate(tailscale_logstream_requests_failed_total) > 0 for {{ $labels.tailscale_logstream_type }} "
              "logs — Tailscale is failing to deliver to the configured SIEM sink (a compliance/forensics "
              "gap). Absent until a log stream is configured.",
              domain="infra", paused=False,
              policy="optional", runbook="log-streaming", panel="Failed requests/s by type"),
        alert("ts2o-logstream-stalled", "Log-stream stalled",
              "max by (tailscale_logstream_type) (time() - tailscale_logstream_last_activity_seconds)",
              "gt", 3600, "30m", "warning",
              "Log stream {{ $labels.tailscale_logstream_type }} has no recent delivery activity",
              "No log-stream delivery activity for over 1h while a stream is configured — delivery has "
              "stalled. Absent until a log stream is configured.",
              domain="infra", paused=True,
              policy="optional", runbook="log-streaming", panel="Last activity age by type"),
        alert("ts2o-logstream-backpressure", "Log-stream backpressure",
              "sum by (tailscale_logstream_type) (rate(tailscale_logstream_max_body_requests_total[15m]))",
              "gt", 0, "15m", "info",
              "Log stream {{ $labels.tailscale_logstream_type }} hitting max body size",
              "Delivery requests are hitting the maximum body size — SIEM backpressure. Informational.",
              domain="infra", paused=True,
              policy="advisory", runbook="log-streaming", panel="Backpressure: spoofed & max-body/s"),
        alert("ts2o-logstream-spoofed", "Log-stream spoofed entries",
              "sum by (tailscale_logstream_type) (rate(tailscale_logstream_spoofed_entries_total[15m]))",
              "gt", 0, "15m", "warning",
              "Log stream {{ $labels.tailscale_logstream_type }} rejecting spoofed entries",
              "Tailscale is rejecting log entries as spoofed — investigate the source of the spoofed log "
              "traffic to the streaming endpoint.",
              domain="security", paused=True,
              policy="optional", runbook="log-streaming", panel="Delivery errors"),
        # --- WU12b: receiver health ---
        alert("ts2o-receiver-rejections", "Receiver rejecting ingest",
              '(sum(rate(tailscale_stream_rejected_total[10m])) or vector(0)) + '
              '(sum(rate(tailscale_webhook_rejected_total[10m])) or vector(0))',
              "gt", 0, "10m", "warning",
              "Stream/webhook receiver is rejecting inbound ingest",
              "The stream or webhook receiver is rejecting inbound events (spoofed/oversized/decode errors) "
              "— investigate the sender or HEC/webhook config.",
              domain="infra", paused=True,
              policy="optional", runbook="ingest-receivers", panel="Receiver rejected/s"),
        alert("ts2o-receiver-latency-high", "Receiver request latency high (p99)",
              "histogram_quantile(0.99, sum by (le) (rate(tailscale_stream_request_duration_seconds_bucket[10m])))",
              "gt", 5, "10m", "warning",
              "Stream-receiver p99 request latency is above 5s",
              "p99 stream-receiver request latency above 5s — receiver backpressure.",
              domain="observability", paused=True,
              policy="optional", runbook="ingest-receivers", panel="Receiver latency p50/p95/p99 (stream)"),
        alert("ts2o-ingest-data-stale", "Accepted ingest data stale",
              "clamp_min(time() - max by (source, signal) "
              "(last_over_time(tailscale2otel_ingest_last_event_timestamp_seconds[30d])), 0)",
              "gt", 3600, "15m", "warning",
              "Accepted {{ $labels.source }}/{{ $labels.signal }} events are over 1h stale",
              "The receiver or poller may still be running, but the newest accepted event timestamp is "
              "over one hour old. This rule ships paused because quiet tailnets and sparse webhook/audit "
              "sources are legitimately idle; enable it only for source/signal pairs expected to deliver "
              "continuously, and add label filters or tune the threshold for that workload.",
              domain="observability", paused=True, lookback=2592000,
              policy="optional", runbook="ingest-receivers", panel="Accepted event freshness"),
        # --- WU12b: posture integration match rate ---
        alert("ts2o-posture-match-low", "Posture integration match rate low",
              "min(tailscale_posture_integration_matched_ratio / "
              "(tailscale_posture_integration_possible_matched_ratio > 0))",
              "lt", 0.8, "1h", "warning",
              "A posture integration is matching <80% of possible devices",
              "An MDM/EDR posture integration is matching <80% of possible devices — devices may be "
              "bypassing posture gates.",
              domain="security", paused=True,
              policy="optional", runbook="device-posture-coverage", panel="Posture match rate"),
    ]

    network = [
        alert("ts2o-flow-reporter-mismatch", "Flow reporter identity mismatch",
              'sum(rate(tailscale_network_reporter_observations_total{consistency="mismatch"}[10m]))',
              "gt", 0, "15m", "warning",
              "Verified flow reporter disagrees with embedded source identity",
              "Flow records are arriving where the Tailscale-verified reporter node ID differs from "
              "the unverified embedded source reference. This bounded diagnostic ships paused: "
              "enable it where reporter/source agreement is expected.",
              domain="security", paused=True,
              policy="optional", runbook="flow-data-pipeline", panel="Reporter trust & consistency"),
        alert("ts2o-audit-schema-drift", "Audit schema drift detected",
              'sum by (field) (rate(tailscale_config_audit_schema_drift_total{status="unknown"}[10m]))',
              "gt", 0, "15m", "warning",
              "Unknown configuration-audit {{ $labels.field }} values are arriving",
              "The current Tailscale audit stream contains enum values this collector version does "
              "not classify. Metrics remain bounded and raw values stay out of labels; inspect the "
              "once-per-value digest warning and update the vendored API contract.",
              domain="observability", paused=True,
              policy="optional", runbook="audit-events", panel="Audit schema drift"),
        alert("ts2o-high-derp-relay-usage", "High DERP relay usage",
              _derp_byte_fraction(),
              "gt", 0.5, "30m", "warning",
              "Most tailnet traffic is relayed via DERP, not direct",
              "Over 50% of fleet bytes are relayed via DERP rather than sent peer-to-peer for 30m — a "
              "NAT-traversal/connectivity problem adding latency. Requires the node-metrics scraper "
              "(absent => not firing).",
              domain="infra", paused=False,
              policy="optional", runbook="derp-and-connectivity", panel="Fleet DERP share (now)"),
        alert("ts2o-derp-region-latency-high", "DERP region latency high",
              "max by (tailscale_derp_region) (tailscale_derp_region_latency_min_seconds)",
              "gt", 0.15, "15m", "info",
              "Best latency to DERP region {{ $labels.tailscale_derp_region }} is high",
              "Even the best device->DERP latency for region {{ $labels.tailscale_derp_region }} exceeds "
              "150ms — poor connectivity to that region. Gated by cardinality.derp_region_rollup.",
              domain="infra", paused=True,
              policy="advisory", runbook="derp-and-connectivity", panel="Best latency per DERP region"),
        alert("ts2o-no-flow-data", "No flow data",
              "sum(rate(tailscale_network_flows_total[30m]))", "lt", 0.0001, "1h", "info",
              "No network flow records for an hour",
              "sum(rate(tailscale_network_flows_total[30m])) ~ 0 for 1h while flow logging is on — the flow "
              "pipeline may be stalled (or the tailnet is genuinely idle; tune/disable as needed). Absent "
              "when flow logging is off.",
              domain="infra", paused=True,
              policy="advisory", runbook="flow-data-pipeline", panel="Flows/s (now)"),
        # --- Task 2.3: routing (infra) ---
        alert("ts2o-subnet-routes-unapproved", "Unapproved subnet routes",
              "max(tailscale_subnet_routes_unapproved)", "gt", 0, "30m", "warning",
              "Subnet routes are advertised but unapproved",
              "tailscale_subnet_routes_unapproved > 0 — a device is advertising subnet routes that an admin "
              "has not approved, so those subnets are not reachable. Approve or reject in the admin console.",
              domain="infra", paused=True,
              policy="optional", runbook="subnet-routing-and-services", panel="Subnet routes — advertised vs enabled"),
        alert("ts2o-exit-node-no-failover", "Subnet route has no failover",
              "count(tailscale_subnet_routes_routers_ratio == 1)", "gt", 0, "30m", "info",
              "A subnet/CIDR is served by a single router (no failover)",
              "count(tailscale_subnet_routes_routers_ratio == 1) > 0 — one or more CIDRs are advertised by "
              "exactly one router, so there is no failover if it goes offline. Add a second subnet router.",
              domain="infra", paused=True,
              policy="advisory", runbook="subnet-routing-and-services", panel="Subnet-route redundancy by CIDR"),
        # --- WU12b: NAT / dropped-packets / HA ---
        alert("ts2o-hard-nat-high", "Fleet hard-NAT fraction high",
              "sum(tailscale_devices_hard_nat_ratio) / sum(tailscale_devices_count_ratio)",
              "gt", 0.5, "30m", "info",
              "More than 50% of fleet devices are behind hard NAT",
              ">50% of devices behind hard NAT — relay pressure / connectivity degradation.",
              domain="infra", paused=True,
              policy="advisory", runbook="derp-and-connectivity", panel="Hard-NAT %"),
        alert("ts2o-node-dropped-packets", "tailscaled dropping outbound packets",
              "sum(rate(tailscaled_outbound_dropped_packets_total[10m]))",
              "gt", 0, "15m", "info",
              "Nodes are dropping outbound packets (sustained)",
              "nodes are dropping outbound packets (sustained) — a connectivity-degradation signal.",
              domain="infra", paused=True,
              policy="advisory", runbook="derp-and-connectivity", panel="Outbound dropped packets/s by node"),
        alert("ts2o-vip-service-no-ha", "VIP service has no host redundancy",
              "count(sum by (tailscale_service_name) (tailscale_service_hosts_ratio) == 1)",
              "gt", 0, "30m", "info",
              "One or more VIP services are backed by a single host (no HA)",
              "one or more Tailscale (VIP) services are backed by a single host (no HA).",
              domain="infra", paused=True,
              policy="advisory", runbook="subnet-routing-and-services", panel="Backing hosts by service"),
        # --- #172: curated client health (tailscale_node_* family) ---
        alert("ts2o-node-health-warnings", "Node client health warnings",
              "max by (tailscale_node, tailscale_health_type) (tailscale_node_health_messages_ratio)",
              "gt", 0, "15m", "warning",
              "Node {{ $labels.tailscale_node }} reporting health warnings ({{ $labels.tailscale_health_type }})",
              "tailscale_node_health_messages_ratio > 0 — the tailscaled client on "
              "{{ $labels.tailscale_node }} is self-reporting one or more active health warnings of type "
              "{{ $labels.tailscale_health_type }} (e.g. no-DERP-connection, key-expiry, network-down). "
              "Curated from the node-metrics scraper (absent => not firing). See the Client health row on "
              "the Node Metrics tab.",
              domain="infra", paused=True,
              policy="optional", runbook="node-client-health", panel="Active health warnings by type"),
        # Restored from the deleted datasource-managed NodeMetricsTargetDown.
        # Nothing else in the pack covers per-target scrape reachability:
        # ts2o-nodemetrics-discovery-failing covers the target LIST, and
        # ts2o-node-health-warnings covers tailscaled's opinion of ITSELF, which
        # a node cannot report at all when we cannot reach it. Keyed on
        # tailscale_node, NOT `instance` — the deleted rule templated
        # {{ $labels.instance }}, which this metric does not carry (see
        # docs/metrics.md), so its summary would have rendered blank.
        alert("ts2o-nodemetrics-target-down", "Node-metrics target down",
              "min by (tailscale_node) (tailscale_node_up_ratio)",
              "lt", 1, "10m", "warning",
              "Node-metrics target {{ $labels.tailscale_node }} is down",
              "tailscale_node_up_ratio == 0 for target {{ $labels.tailscale_node }} for 10m — the "
              "node-metrics scraper could not reach tailscaled's metrics endpoint on that node, so "
              "every forwarded tailscaled_* series from it is frozen at its last value. Ships PAUSED: "
              "a sleeping laptop in the target list would fire this every night, so enable it once "
              "your targets are hosts expected to be up continuously. Gated on the node-metrics "
              "scraper (off by default) => the gauge is absent and the rule cannot fire without it.",
              domain="infra", paused=True,
              policy="optional", runbook="nodemetrics-scrape-targets", panel="Node up"),
        # Restored from the deleted datasource-managed FlowLogsDropped.
        alert("ts2o-flow-logs-dropped", "Flow logs dropped",
              "sum(rate(tailscale_network_flow_logs_dropped_total[10m]))",
              "gt", 0, "10m", "warning",
              "Tailscale flow log records being dropped by the volume guard",
              "rate(tailscale_network_flow_logs_dropped_total[10m]) > 0 for 10m — the per-window "
              "flow-log volume guard (collectors.flowlogs.max_log_records_per_window) is truncating "
              "flow LOG records. Metrics are NEVER capped, so throughput panels stay complete and "
              "correct while the log stream silently loses records; this rule is the only thing that "
              "makes that visible. Ships ENABLED for the same reason as the cardinality cap: the loss "
              "is silent and the counter is absent until the guard actually truncates. Raise the cap "
              "or narrow the flow-log scope (per_connection vs per_record) if this is unexpected.",
              domain="infra", paused=False,
              policy="optional", runbook="flow-data-pipeline", panel="Flow log records dropped/s"),
        # Restored from the deleted datasource-managed NodeIPForwardingMisconfigured.
        # No other rule reads tailscale_webhook_events_total: ts2o-receiver-rejections
        # counts webhooks we REFUSED, which is the opposite population.
        alert("ts2o-node-ip-forwarding", "Node IP forwarding misconfigured",
              "sum by (tailscale_webhook_type) (rate(tailscale_webhook_events_total"
              '{tailscale_webhook_type=~"exitNodeIPForwardingNotEnabled|'
              'subnetIPForwardingNotEnabled"}[15m]))',
              "gt", 0, "5m", "warning",
              "Tailscale node IP forwarding misconfigured ({{ $labels.tailscale_webhook_type }})",
              "Tailscale delivered {{ $labels.tailscale_webhook_type }} webhook events in the last "
              "15m — a node that should route traffic (exit node or subnet router) has IP forwarding "
              "disabled on the host OS, so that routing is silently broken until it is fixed "
              "(net.ipv4.ip_forward=1 / net.ipv6.conf.all.forwarding=1 on the node). This signal "
              "exists ONLY as a webhook — there is no log-streaming or polling equivalent — and it is "
              "emitted at INFO severity, so this rule is the only thing that surfaces it. Ships "
              "ENABLED: the webhook receiver is off by default, so the series is absent and the rule "
              "is inert for anyone not using it. It clears once the events stop arriving.",
              domain="infra", paused=False,
              policy="optional", runbook="subnet-routing-and-services",
              panel="Webhook events/s by type"),
        # --- Task 2.5: per-tailnet API errors (F) ---
        alert("ts2o-tailnet-api-errors", "Per-tailnet API errors",
              "sum by (tailscale_tailnet) (rate(tailscale2otel_api_requests_total"
              "{http_response_status_code=~\"4..|5..\", tailscale_tailnet!=\"\"}[10m]))",
              "gt", 0, "15m", "warning",
              "Tailscale API errors for tailnet {{ $labels.tailscale_tailnet }}",
              "Per-tailnet 4xx/5xx API error rate > 0 for {{ $labels.tailscale_tailnet }} — one tailnet's "
              "polling is failing without masking the others (MSP/multi-tailnet). Check that tailnet's "
              "credentials.",
              domain="observability", paused=True,
              policy="optional", runbook="tailscale-api-health", panel="Per-tailnet API errors"),
    ]

    recording = [
        record("ts2o-rec-devices-online", "tailscale:devices_online:count",
               "count(max by (host_id) (tailscale_device_online_ratio) == 1)",
               "Fleet devices currently online (deploy-stable count).", domain="infra", paused=True),
        record("ts2o-rec-posture-autoupdate", "tailscale:posture_autoupdate:ratio",
               "count(max by (host_id) (tailscale_device_posture_ratio{auto_update=\"true\"})) / "
               "clamp_min(count(max by (host_id) (tailscale_device_posture_ratio)), 1)",
               "Fraction of devices with client auto-update enabled (feeds PostureAutoUpdateLow + the Security tab).",
               domain="security", paused=False),
        record("ts2o-rec-posture-encrypted", "tailscale:posture_encrypted:ratio",
               "count(max by (host_id) (tailscale_device_posture_ratio{encrypted=\"true\"})) / "
               "clamp_min(count(max by (host_id) (tailscale_device_posture_ratio)), 1)",
               "Fraction of devices reporting an encrypted local state store.",
               domain="security", paused=False),
        record("ts2o-rec-derp-byte-fraction", "tailscale:derp_relay:byte_fraction",
               _derp_byte_fraction(),
               "Fleet fraction of bytes relayed via DERP (precomputes the heavy 4-rate dashboard/alert query).",
               domain="infra", paused=False),
        record("ts2o-rec-flow-throughput", "tailscale:flow_throughput:bytes:rate5m",
               "sum(rate(tailscale_network_io_rollup_bytes_total[5m])) or "
               "sum(rate(tailscale_network_io_bytes_total[5m]))",
               "Total flow throughput (rollup if present, else raw).", domain="infra", paused=True),
        record("ts2o-rec-series-active-sum", "tailscale2otel:series_active:sum",
               "sum(max by (metric_name) (tailscale2otel_series_active))",
               "Total active series across all tailscale2otel metrics — an ingest-cost proxy.",
               domain="observability", paused=False),
        record("ts2o-rec-ingest-freshness", "tailscale2otel:ingest_event_freshness_seconds",
               "clamp_min(time() - max by (source, signal) "
               "(last_over_time(tailscale2otel_ingest_last_event_timestamp_seconds[30d])), 0)",
               "Seconds since the greatest accepted event timestamp per source/signal. Use only with "
               "workload-specific staleness thresholds; sparse sources can be legitimately idle.",
               domain="observability", paused=True),
        record("ts2o-rec-keys-expiring-7d", "tailscale:device_keys_expiring_7d:count",
               "count((max by (host_id) (tailscale_device_key_expiry_seconds) - time() < 7*86400) and "
               "(max by (host_id) (tailscale_device_key_expiry_seconds) - time() > 0))",
               "Device node keys expiring within 7 days (and not already expired).",
               domain="security", paused=True),
        # --- WU12b: new recording rules ---
        record("ts2o-rec-series-by-group", "tailscale2otel:series_active:by_group",
               "sum by (metric_group) (tailscale2otel_series_by_group)",
               "Active series per metric group — the cardinality/cost driver view.",
               domain="observability", paused=True),
        record("ts2o-rec-hard-nat-fraction", "tailscale:devices_hard_nat:fraction",
               "sum(tailscale_devices_hard_nat_ratio) / sum(tailscale_devices_count_ratio)",
               "Fraction of fleet devices behind hard NAT.",
               domain="infra", paused=True),
        record("ts2o-rec-api-error-ratio", "tailscale2otel:api_requests:error_ratio",
               'sum(rate(tailscale2otel_api_requests_total{http_response_status_code=~"5.."}[5m])) / '
               "clamp_min(sum(rate(tailscale2otel_api_requests_total[5m])), 1)",
               "Tailscale API 5xx error ratio (5m).",
               domain="observability", paused=True),
        record("ts2o-rec-node-dropped-packets", "tailscale:node_dropped_packets:rate5m",
               "sum by (tailscale_node) (rate(tailscaled_outbound_dropped_packets_total[5m]))",
               "Per-node outbound dropped-packet rate (5m).",
               domain="infra", paused=True),
    ]

    return [
        ("tailscale2otel-health", health),
        ("tailscale2otel-security", security),
        ("tailscale2otel-integrations", integrations),
        ("tailscale2otel-network", network),
        ("tailscale2otel-recording", recording),
    ]


def check_runbook_coverage():
    """The reverse of runbook_url()'s check: a section nobody links is dead weight
    that reads as coverage. Renaming or deleting a rule is the usual way one
    appears, and only this direction catches it."""
    used = set()
    for _, group in groups():
        for rule in group:
            url = rule.get("annotations", {}).get("runbook_url")
            if url:
                used.add(url[len(RUNBOOK_BASE):])
    orphans = sorted(runbook_slugs() - used)
    if orphans:
        raise ValueError(
            "docs/runbooks.md has %d section(s) no rule links to: %s. Either a rule was renamed or "
            "removed, or the section should go." % (len(orphans), ", ".join(orphans)))


# ---------------------------------------------------------------------------
# emit: the shipped artifact — one manifest per rule under grafana-managed/
# ---------------------------------------------------------------------------

RULE_API = "rules.alerting.grafana.app/v0alpha1"
FOLDER_API = "folder.grafana.app/v1beta1"
FOLDER_TITLE = "tailscale2otel Alerts"

# Keys in a catalogue entry that are generator-internal and must never be emitted.
_INTERNAL = ("uid", "_prom", "_desc")


def _manifest(rule):
    kind = "RecordingRule" if is_recording(rule) else "AlertRule"
    spec = {k: v for k, v in rule.items() if k not in _INTERNAL}
    return {
        "apiVersion": RULE_API,
        "kind": kind,
        "metadata": {
            "name": rule["uid"],
            # The folder is addressed twice on purpose: gcx routes on the label,
            # the API stores the annotation. Emitting only one lands the rule in
            # the default folder without complaint.
            "annotations": {"grafana.app/folder": FOLDER},
            "labels": {"grafana.app/folder": FOLDER},
        },
        "spec": spec,
    }


def _write_json(path, doc):
    # sort_keys + a trailing newline: two runs must be byte-identical, and a
    # file without a final newline makes every future diff touch the last line.
    with open(path, "w", encoding="utf-8") as f:
        json.dump(doc, f, indent=2, sort_keys=True)
        f.write("\n")


def emit_grafana_managed(outdir):
    """Write the manifest directory. Returns the list of paths written."""
    check_runbook_coverage()
    os.makedirs(outdir, exist_ok=True)
    # Clear first, so a renamed or deleted rule cannot linger as a stale file
    # that `gcx resources push` would happily keep re-creating.
    for stale in os.listdir(outdir):
        if stale.endswith(".json"):
            os.remove(os.path.join(outdir, stale))

    written = []
    folder_path = os.path.join(outdir, "_folder.json")
    _write_json(folder_path, {
        "apiVersion": FOLDER_API, "kind": "Folder",
        "metadata": {"name": FOLDER},
        "spec": {"title": FOLDER_TITLE},
    })
    written.append(folder_path)

    for _, group in groups():
        for rule in group:
            path = os.path.join(outdir, "%s.json" % rule["uid"])
            _write_json(path, _manifest(rule))
            written.append(path)
    return written


# ---------------------------------------------------------------------------
# emit: the TEST-ONLY Prometheus rendering (--prom-out)
# ---------------------------------------------------------------------------
#
# NEVER COMMITTED, NEVER SHIPPED. The project ships Grafana-managed rules only.
# This rendering exists for exactly one reason: `promtool test rules` is the only
# thing in the repo that EXECUTES a rule against synthetic series, and promtool
# understands no format but Prometheus's. It has already earned its keep — it is
# what proved that dropping the `> 0` guard on a key-expiry rule makes every
# long-dead host alert forever.

_WORD = re.compile(r"[A-Za-z0-9]+")


def prom_alertname(title):
    """CamelCase alert name for a rule title.

    promtool fixtures address a rule by name, and the manifest `title` ("Exporter
    down") is prose. Derived rather than hand-listed so a retitled rule cannot
    quietly stop being covered by its fixture — the fixture fails to match instead.
    """
    parts = []
    for word in _WORD.findall(title):
        parts.append(word if word[:1].isupper() else word[:1].upper() + word[1:])
    name = "".join(parts)
    if not name or not name[0].isalpha():
        raise ValueError("cannot derive a promtool alert name from %r" % (title,))
    return name


def _num(v):
    return str(int(v)) if isinstance(v, (int, float)) and float(v).is_integer() else repr(v)


def _prom_expr(rule):
    p = rule["_prom"]
    expr, op, thr = p["expr"], p["op"], p["thr"]
    if op == "gt":
        out = "(%s) > %s" % (expr, _num(thr))
    elif op == "lt":
        out = "(%s) < %s" % (expr, _num(thr))
    elif op == "within_range":
        lo, hi = thr
        out = "((%s) > %s) < %s" % (expr, _num(lo), _num(hi))
    else:
        raise ValueError("no Prometheus rendering for threshold op %r" % (op,))
    if p["policy"] == "coverage_critical":
        # Grafana says "absence is the alert" with noDataState: Alerting, which
        # Prometheus has no equivalent for. `or absent(...)` is the faithful
        # translation; without it the rendering silently drops the semantics the
        # absent() fixture case exists to prove.
        out = "(%s) or absent(%s)" % (out, expr)
    return out


def build_prom_rules():
    check_runbook_coverage()
    out = []
    for name, group in groups():
        rendered = []
        for rule in group:
            if rule["_prom"]["ds"] != PROM:
                continue  # LogQL: promtool cannot parse it
            if is_recording(rule):
                rendered.append({"record": rule["metric"],
                                 "expr": rule["_prom"]["expr"],
                                 "labels": rule["labels"]})
            else:
                # Only `summary` and `runbook_url` are carried. promtool's
                # exp_annotations is an EXACT match over the whole map, so every
                # annotation here has to be restated verbatim in every fixture:
                #  * the panel links renumber whenever a panel is inserted into
                #    the generated dashboard, which would break every fixture on
                #    an unrelated dashboard edit;
                #  * `description` is a paragraph of prose, and pasting it into a
                #    fixture buys nothing the summary does not already prove
                #    (both carry the same `{{ $labels.* }}` templating).
                # The shipped manifests keep all of it; this file is a fixture.
                annotations = {k: v for k, v in rule["annotations"].items()
                               if k in ("summary", "runbook_url")}
                rendered.append({"alert": prom_alertname(rule["title"]),
                                 "expr": _prom_expr(rule),
                                 "for": rule["_prom"]["dur"],
                                 "labels": rule["labels"],
                                 "annotations": annotations})
        if rendered:
            out.append({"name": name, "rules": rendered})
    return {"groups": out}


_PROM_HEADER = """\
# TEST FIXTURE — GENERATED, NEVER COMMITTED, NEVER SHIPPED.
#
# tailscale2otel ships Grafana-managed rule manifests only
# (deploy/alerts/grafana-managed/, pushed with `gcx resources push`). This file
# is a throwaway Prometheus rendering of the same catalogue, produced by
#   python3 deploy/alerts/gen/build_rules.py --prom-out <path>
# purely so `promtool test rules` can EXECUTE the expressions. Do not load it
# into a ruler, do not add it to git, do not link to it from the docs.
#
# Loki-backed rules are omitted: promtool cannot parse LogQL.
"""


def emit_prom_rules(path):
    doc = build_prom_rules()
    with open(path, "w", encoding="utf-8") as f:
        f.write(_PROM_HEADER)
        f.write(yamlify(doc) + "\n")
    return doc


def main():
    ap = argparse.ArgumentParser(
        description="Generate the tailscale2otel Grafana-managed rule manifests.")
    ap.add_argument("--out", metavar="DIR",
                    help="directory to write the rules.alerting.grafana.app manifests into "
                         "(one JSON per rule + _folder.json). Existing *.json are deleted first.")
    ap.add_argument("--prom-out", metavar="FILE",
                    help="ALSO render a TEST-ONLY Prometheus rule file to FILE. This artifact is "
                         "never committed and never shipped — it exists only so `promtool test "
                         "rules` can execute the expressions. Loki rules are omitted from it.")
    args = ap.parse_args()
    if not args.out and not args.prom_out:
        ap.error("nothing to do: pass --out (the shipped manifests) and/or --prom-out (test fixture)")

    n_alert = sum(1 for _, rs in groups() for r in rs if not is_recording(r))
    n_rec = sum(1 for _, rs in groups() for r in rs if is_recording(r))
    n_paused = sum(1 for _, rs in groups() for r in rs if r["paused"])

    if args.out:
        written = emit_grafana_managed(args.out)
        n_panel = sum(1 for _, rs in groups() for r in rs
                      if "__panelId__" in r.get("annotations", {}))
        tally = {name: 0 for name in POLICY}
        for policy in POLICY_BY_UID.values():
            tally[policy] += 1
        print("wrote %d manifests to %s  (%d alert rules, %d recording rules, %d paused)"
              % (len(written), args.out, n_alert, n_rec, n_paused))
        print("  policy: %s" % ", ".join("%s=%d" % (k, tally[k]) for k in POLICY))
        print("  links:  runbook=%d/%d over %d sections, panel=%d/%d"
              % (n_alert, n_alert, len(runbook_slugs()), n_panel, n_alert))
        print("  push:   gcx resources push -p %s" % args.out)

    if args.prom_out:
        doc = emit_prom_rules(args.prom_out)
        n = sum(len(g["rules"]) for g in doc["groups"])
        print("wrote %s  (%d rules, TEST-ONLY — not shipped, not committed)"
              % (args.prom_out, n))


if __name__ == "__main__":
    main()
