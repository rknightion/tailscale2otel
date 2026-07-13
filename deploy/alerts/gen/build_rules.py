#!/usr/bin/env python3
"""Generate the tailscale2otel **Grafana-managed** alerting + recording rules.

Emits a Grafana *file-provisioning* document (``apiVersion: 1`` + ``groups:``) of
Grafana-managed rules — i.e. rules Grafana itself evaluates (multi-datasource,
``noDataState``/``execErrState``, ``isPaused``), NOT Prometheus/Loki *ruler*
("datasource-managed") rules. The committed sibling file
``tailscale2otel.rules.yaml`` is the datasource-managed equivalent for a
Mimir/Cortex/Prometheus ruler; this generator targets Grafana alerting.

Dashboards-as-code style: edit this generator, regenerate, commit both. Only the
Python standard library is required (PyYAML is intentionally NOT a dependency —
a tiny block-YAML emitter lives in ``yamlify`` below).

Usage:
    python3 build_rules.py --out ../tailscale2otel.grafana-rules.yaml

Conventions baked in here:
  * Every rule is a 3-node Grafana pipeline: A (datasource query, range) ->
    B (reduce, last) -> C (threshold). ``condition: C``. This is exactly what the
    Grafana UI produces, so the rules round-trip cleanly through the API/UI.
  * Metric names are the OTLP->Prometheus *normalized* names (see ../README.md).
  * ``service_version`` is a per-deploy label on every exporter series, so gauge
    reads are wrapped in ``max by (<real dims>)`` to avoid a redeploy doubling
    alert instances (same rationale as the dashboards' sv() helper).
  * Gated/optional signals (posture integrations, log streaming, services,
    tailnet-lock, DERP rollups, node discovery) are ABSENT until the tailnet has
    the data, so their rules use ``noDataState: OK`` — absent => not firing.
  * Datasource UIDs are the portable Grafana Cloud defaults (grafanacloud-prom /
    grafanacloud-logs); swap them for a self-hosted stack.
  * Rules NOT in the recommended starter set ship ``isPaused: true`` (enable in
    the UI when you want them). Starter-set + the explicitly-requested 48h key
    tiers ship enabled.
"""

import argparse
import json

PROM = "grafanacloud-prom"
LOKI = "grafanacloud-logs"
FOLDER = "tailscale2otel"
INTERVAL = "1m"


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


def _query_node(expr, ds_uid, lookback=3600):
    model = {"datasource": _ds(ds_uid), "editorMode": "code", "expr": expr,
             "instant": False, "range": True, "intervalMs": 1000,
             "maxDataPoints": 43200, "refId": "A"}
    if ds_uid == LOKI:
        model["queryType"] = "range"
    return {"refId": "A", "relativeTimeRange": {"from": lookback, "to": 0},
            "datasourceUid": ds_uid, "model": model}


def _reduce_node():
    return {"refId": "B", "relativeTimeRange": {"from": 0, "to": 0},
            "datasourceUid": "__expr__",
            "model": {"datasource": _ds("__expr__"), "expression": "A",
                      "reducer": "last", "type": "reduce", "refId": "B"}}


def _threshold_node(op, thr):
    # op: "gt" | "lt" | "within_range" (thr is [lo, hi] for within_range)
    params = thr if isinstance(thr, list) else [thr]
    cond = {"type": "query", "evaluator": {"type": op, "params": params},
            "operator": {"type": "and"}, "query": {"params": ["C"]},
            "reducer": {"type": "last", "params": []}}
    return {"refId": "C", "relativeTimeRange": {"from": 0, "to": 0},
            "datasourceUid": "__expr__",
            "model": {"datasource": _ds("__expr__"), "expression": "B",
                      "type": "threshold", "conditions": [cond], "refId": "C"}}


def _labels(severity, domain, page, hygiene):
    lbl = {"severity": severity, "service": "tailscale2otel", "domain": domain}
    if page is not None:
        lbl["page"] = "true" if page else "false"
    # worthy of auto-investigation iff critical OR phone-paging OR a (non-hygiene) security rule
    worthy = (severity == "critical") or bool(page) or (domain == "security" and not hygiene)
    if not worthy:
        lbl["skipinvestigation"] = "true"
    return lbl


def alert(uid, title, expr, op, thr, dur, severity, summary, desc,
          ds=PROM, paused=True, nodata="OK", execerr="OK", lookback=3600,
          domain="observability", page=None, hygiene=False):
    return {
        "uid": uid, "title": title, "condition": "C",
        "data": [_query_node(expr, ds, lookback), _reduce_node(), _threshold_node(op, thr)],
        "noDataState": nodata, "execErrState": execerr, "for": dur,
        "labels": _labels(severity, domain, page, hygiene),
        "annotations": {"summary": summary, "description": desc},
        "isPaused": paused,
    }


def record(uid, metric, expr, desc, ds=PROM, paused=True, domain="observability"):
    # Grafana-managed recording rules must resolve to a single value per series, either via an
    # instant query or a range query + Reduce node. A (range) -> B (reduce, last) -> record from B,
    # mirroring alert()'s pipeline; recording from a raw range node (no reduce) is invalid.
    return {
        "uid": uid, "title": metric,
        "data": [_query_node(expr, ds), _reduce_node()],
        "record": {"metric": metric, "from": "B"},
        "labels": {"service": "tailscale2otel", "domain": domain},
        "annotations": {"description": desc},
        "isPaused": paused,
    }


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
              "telemetry, so no Tailscale metrics or logs are flowing. noDataState is Alerting (not "
              "the group default OK) so the series going fully absent — e.g. an OOM-killed pod — also "
              "fires this, matching the datasource-managed ExporterDown semantics "
              "(absent(tailscale2otel_up_ratio) or == 0). Check the \"Up state\" / build-info panels "
              "on the Exporter Health dashboard (uid ts2otel-exporter-health).",
              domain="observability", paused=False, nodata="Alerting"),
        alert("ts2o-collector-scrape-failing", "Collector scrape failing",
              "min by (tailscale_collector) (tailscale2otel_scrape_success_ratio)",
              "lt", 1, "15m", "warning",
              "Collector {{ $labels.tailscale_collector }} scrape failing",
              "tailscale2otel_scrape_success_ratio == 0 for collector {{ $labels.tailscale_collector }} "
              "for 15m — its last scrape failed and has not recovered, so that collector's series are "
              "stale. Complements CollectorScrapeStale (timestamp-based). See the \"Scrape success / "
              "duration / errors by collector\" panels on the Exporter Health dashboard "
              "(uid ts2otel-exporter-health).",
              domain="observability", paused=False),
        alert("ts2o-collector-scrape-stale", "Collector scrape stale",
              "max by (tailscale_collector) (time() - tailscale2otel_scrape_last_timestamp_seconds)",
              "gt", 3600, "10m", "warning",
              "Collector {{ $labels.tailscale_collector }} has not completed a scrape recently",
              "time() - tailscale2otel_scrape_last_timestamp_seconds > 1h for collector "
              "{{ $labels.tailscale_collector }} — it is wedged (the success gauge can stay stale at 1), "
              "so that collector's series are not refreshing. Complements CollectorScrapeFailing.",
              domain="observability", paused=False),
        alert("ts2o-metric-cardinality-capped", "Metric cardinality capped",
              "max(tailscale2otel_series_overflowing_ratio)",
              "gt", 0, "5m", "warning",
              "A tailscale2otel metric hit the per-metric series cap",
              "tailscale2otel_series_overflowing_ratio > 0 — one or more metrics are overflowing the "
              "per-metric series cap (cardinality.metric_limit); excess series are collapsed into "
              "otel_metric_overflow, i.e. SILENT per-series loss. Usually ephemeral source_port. Raise "
              "metric_limit or lower flow cardinality.",
              domain="observability", paused=False),
        alert("ts2o-series-budget-high", "Series budget high",
              "max by (metric_name)(tailscale2otel_series_active) / on() group_left() "
              "max(tailscale2otel_series_limit)",
              "gt", 0.8, "10m", "warning",
              "Busiest tailscale2otel metric approaching the per-metric series cap",
              "A metric family is using >80% of its per-metric series budget "
              "(series_active / series_limit > 0.8); MetricCardinalityCapped fires once it overflows.",
              domain="observability", paused=False),
        alert("ts2o-api-auth-failing", "Tailscale API auth failing",
              'sum(rate(tailscale2otel_api_requests_total{http_response_status_code=~"401|403"}[10m]))',
              "gt", 0, "10m", "critical",
              "Tailscale API returning 401/403 — credentials broken",
              "The exporter is getting 401/403 from the Tailscale API — the OAuth client or API key is "
              "invalid, expired or revoked, so all polling fails and every tailnet metric goes stale.",
              domain="security", paused=False),
        alert("ts2o-api-rate-limited", "Tailscale API rate limited",
              'sum(rate(tailscale2otel_api_requests_total{http_response_status_code="429"}[10m]))',
              "gt", 0, "10m", "warning",
              "Tailscale API returning 429 (rate limited)",
              "Sustained HTTP 429 from the Tailscale API — polling is being throttled. Increase poll "
              "intervals or reduce the number of enabled collectors.",
              domain="observability", paused=True),
        alert("ts2o-api-server-errors", "Tailscale API server errors",
              'sum(rate(tailscale2otel_api_requests_total{http_response_status_code=~"5.."}[10m]))',
              "gt", 0.05, "15m", "warning",
              "Tailscale API returning 5xx",
              "Sustained HTTP 5xx from the Tailscale API (>0.05/s) — Tailscale-side instability; the "
              "exporter retries but data may be delayed.",
              domain="observability", paused=True),
        alert("ts2o-api-retries-elevated", "Tailscale API retries elevated",
              "sum(rate(tailscale2otel_api_retries_total[10m]))", "gt", 0.1, "15m", "warning",
              "Elevated Tailscale API retry rate",
              "Sustained API retry rate (>0.1/s) — flakiness/backoff against the Tailscale API. Break down "
              "by endpoint on the Exporter Diagnostics tab.",
              domain="observability", paused=True),
        alert("ts2o-checkpoint-persist-errors", "Checkpoint persist errors",
              "sum by (tailscale_collector) (rate(tailscale2otel_checkpoint_persist_errors_total[15m]))",
              "gt", 0, "15m", "warning",
              "Collector {{ $labels.tailscale_collector }} cannot persist its checkpoint",
              "rate(tailscale2otel_checkpoint_persist_errors_total) > 0 for {{ $labels.tailscale_collector }} "
              "— the scrape window succeeded but its high-water mark could not be saved, risking replay/"
              "duplicate emission on restart. Check the checkpoint file path/permissions.",
              domain="observability", paused=False),
        alert("ts2o-component-errors", "Component errors",
              "sum by (component) (rate(tailscale2otel_component_errors_total[15m]))",
              "gt", 0, "15m", "warning",
              "tailscale2otel component {{ $labels.component }} erroring",
              "A non-collector subsystem ({{ $labels.component }} — receivers, admin server, streaming "
              "auto-configure) is logging errors. See the Reliability row on the Exporter Diagnostics tab.",
              domain="observability", paused=False),
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
              domain="observability", paused=True),
        alert("ts2o-enrich-cache-stale", "Enrichment cache stale",
              "max(tailscale2otel_enrich_cache_age_seconds)", "gt", 3600, "15m", "warning",
              "Device-enrichment cache is stale",
              "The IP/nodeID->name enrichment cache has not refreshed in over 1h — flow/audit name "
              "resolution is degrading to unknown/external. The devices collector populates this cache; "
              "check it is enabled and scraping.",
              domain="observability", paused=False),
        alert("ts2o-nodemetrics-discovery-failing", "Node-metrics discovery failing",
              "max(tailscale2otel_nodemetrics_discovery_success_ratio)", "lt", 1, "10m", "warning",
              "Node-metrics dynamic discovery is failing",
              "tailscale2otel_nodemetrics_discovery_success_ratio < 1 — the last dynamic target-discovery "
              "refresh failed, so the node-metrics target list is stale. (Absent when discovery is "
              "disabled => this never fires.)",
              domain="infra", paused=True),
        alert("ts2o-admin-auth-rejections-high", "Admin auth rejections high",
              "sum(rate(tailscale2otel_admin_auth_rejected_total[10m]))", "gt", 0.2, "10m", "info",
              "Elevated admin auth rejections",
              "Sustained admin HTTP auth rejections (>0.2/s) on the status page / pprof endpoint — possible "
              "probing or a misconfigured admin token.",
              domain="observability", paused=True),
        alert("ts2o-gc-cpu-fraction-high", "GC CPU fraction high",
              "max(tailscale2otel_runtime_gc_cpu_fraction_ratio)", "gt", 0.25, "15m", "info",
              "Go GC using a high CPU fraction",
              "runtime gc.cpu_fraction > 0.25 — GC is taking a large share of CPU. Note: this exporter is "
              "near-idle, so the fraction can be high against a tiny absolute; check absolute CPU first.",
              domain="observability", paused=True),
        # --- Task 2.2: new self-obs alerts (C2/C3/C9) ---
        alert("ts2o-export-latency-high", "Export latency high",
              "histogram_quantile(0.99, sum by (le, signal) "
              "(rate(tailscale2otel_export_duration_seconds_bucket[10m])))",
              "gt", 2, "10m", "warning",
              "OTLP export p99 latency is high",
              "p99 OTLP export duration > 2s for a signal — the backend is slow or unreachable, so exports "
              "are backing up. Break down by signal on the Exporter Diagnostics tab.",
              domain="observability", paused=True),
        alert("ts2o-export-failures", "Export failures",
              "sum by (outcome) (rate(tailscale2otel_export_duration_seconds_count{outcome=\"failure\"}[10m]))",
              "gt", 0, "10m", "warning",
              "OTLP exports are failing",
              "rate of failed OTLP export attempts > 0 — datapoints/logs are not reaching the backend. "
              "Complements export.failures; check OTLP endpoint/credentials.",
              domain="observability", paused=True),
        alert("ts2o-scrape-staleness-high", "Scrape staleness high",
              "max by (tailscale_collector) (tailscale2otel_scrape_staleness_seconds)",
              "gt", 1800, "10m", "warning",
              "Collector {{ $labels.tailscale_collector }} scrape data is stale",
              "tailscale2otel_scrape_staleness_seconds > 30m for {{ $labels.tailscale_collector }} — its "
              "series have not refreshed recently. Friendlier framing of CollectorScrapeStale.",
              domain="observability", paused=True),
        alert("ts2o-scrape-budget-overrun", "Scrape budget overrun",
              "max by (tailscale_collector) (tailscale2otel_scrape_budget_ratio)",
              "gt", 1, "15m", "warning",
              "Collector {{ $labels.tailscale_collector }} is exceeding its scrape budget",
              "tailscale2otel_scrape_budget_ratio > 1 for {{ $labels.tailscale_collector }} — the scrape is "
              "taking longer than its interval, so it cannot keep up. Increase the interval or reduce work.",
              domain="observability", paused=True),
        alert("ts2o-config-warnings", "Config warnings present",
              "max(tailscale2otel_config_warnings_ratio)", "gt", 0, "15m", "info",
              "tailscale2otel has configuration warnings",
              "tailscale2otel_config_warnings_ratio > 0 — the loaded config produced advisory warnings "
              "(e.g. API-key auth, poll+stream overlap). Review startup logs / the Warnings() output.",
              domain="observability", paused=False),
        alert("ts2o-config-invalid", "Config invalid",
              "max(tailscale2otel_config_valid_ratio)", "lt", 1, "5m", "critical",
              "tailscale2otel config is invalid",
              "tailscale2otel_config_valid_ratio < 1 — the running config failed validation. This normally "
              "fails startup, so seeing it at runtime is rare and serious; inspect the config.",
              domain="observability", paused=True),
        alert("ts2o-checkpoint-stalled", "Checkpoint persist stalled",
              "max(tailscale2otel_checkpoint_persist_age_seconds)", "gt", 1800, "15m", "warning",
              "Checkpoint has not been persisted recently",
              "tailscale2otel_checkpoint_persist_age_seconds > 30m — the high-water-mark checkpoint is not "
              "being saved, risking replay/duplicate emission on restart. Check the checkpoint store.",
              domain="observability", paused=True),
        alert("ts2o-export-volume-high", "Export volume high",
              "rate(tailscale2otel_export_datapoints_total[10m])", "gt", 5000, "15m", "info",
              "Exported datapoint rate is high",
              "Exported datapoints/s exceed the configured budget (default 5000/s here) — an ingest-cost "
              "signal. Tune this threshold to your Grafana Cloud plan; lower flow cardinality if needed.",
              domain="observability", paused=True),
        alert("ts2o-exporter-update-available", "Exporter update available",
              "max(tailscale2otel_update_available_ratio)", "gt", 0, "1h", "info",
              "A tailscale2otel exporter update is available",
              "tailscale2otel_update_available_ratio > 0 — a newer tailscale2otel release is available. "
              "Informational; absent on dev builds.",
              domain="observability", paused=True),
    ]

    security = [
        alert("ts2o-tailnet-lock-errors", "Tailnet-lock errors",
              "max(tailscale_tailnet_lock_errors_ratio)", "gt", 0, "10m", "warning",
              "Devices have tailnet-lock errors",
              "tailscale_tailnet_lock_errors_ratio > 0 — one or more devices have a non-empty tailnet-lock "
              "error (e.g. an unsigned node); a signing node must sign the affected keys. See the Tailnet "
              "lock row on the Security & Audit tab.",
              domain="security", paused=False),
        alert("ts2o-audit-config-change-warn", "Audit config change (WARN)",
              "sum(count_over_time({service_name=\"tailscale2otel\"} | event_name=`tailscale.config.audit` "
              "| severity_text=`WARN` [10m]))",
              "gt", 0, "5m", "warning",
              "Configuration-audit event carried an error",
              "A tailscale.config.audit log was emitted at WARN (the change carried an error) in the last "
              "10m. Inspect the Configuration audit row on the Security & Audit tab.",
              ds=LOKI, domain="security", paused=False),
        alert("ts2o-device-key-expiring-critical", "Device key expiring (<48h)",
              "max by (host_name, host_id, tailscale_user) (tailscale_device_key_expiry_seconds) - time()",
              "within_range", [0, 172800], "1h", "critical",
              "Device node key for {{ $labels.host_name }} expires in <48h",
              "The Tailscale node key for {{ $labels.host_name }} (user {{ $labels.tailscale_user }}) "
              "expires within 48h. When it expires the device drops off the tailnet until re-authed. "
              "Critical tier on top of the 7-day DeviceKeyExpiringSoon warning.",
              domain="security", paused=False),
        alert("ts2o-auth-key-expiring-critical", "Auth/API key expiring (<48h)",
              "max by (tailscale_key_id, tailscale_key_type, tailscale_key_description) "
              "(tailscale_key_expiry_seconds) - time()",
              "within_range", [0, 172800], "1h", "critical",
              "Auth/API key {{ $labels.tailscale_key_id }} expires in <48h",
              "Tailscale key {{ $labels.tailscale_key_id }} ({{ $labels.tailscale_key_type }}) expires "
              "within 48h — rotate it before automation/devices using it lose access. Critical tier on top "
              "of the 7-day AuthKeyExpiringSoon warning.",
              domain="security", paused=False),
        alert("ts2o-posture-autoupdate-low", "Posture: auto-update coverage low",
              "count(max by (host_id) (tailscale_device_posture_ratio{auto_update=\"true\"})) / "
              "clamp_min(count(max by (host_id) (tailscale_device_posture_ratio)), 1)",
              "lt", 0.8, "1h", "warning",
              "Fleet auto-update coverage below 80%",
              "Fewer than 80% of devices report Tailscale client auto-update enabled. Gated by "
              "collect_posture; absent => not firing.",
              domain="security", hygiene=True, paused=False),
        alert("ts2o-posture-encryption-low", "Posture: state-encryption coverage low",
              "count(max by (host_id) (tailscale_device_posture_ratio{encrypted=\"true\"})) / "
              "clamp_min(count(max by (host_id) (tailscale_device_posture_ratio)), 1)",
              "lt", 0.8, "1h", "warning",
              "Fleet state-encryption coverage below 80%",
              "Fewer than 80% of devices report an encrypted local state store. Gated by collect_posture.",
              domain="security", hygiene=True, paused=True),
        alert("ts2o-devices-needing-update", "Many devices need updates",
              "count(max by (host_id) (tailscale_device_update_available_ratio) == 1)",
              "gt", 5, "30m", "info",
              "More than 5 devices have a client update available",
              "count(tailscale_device_update_available_ratio == 1) > 5 — several clients are behind on the "
              "Tailscale client. Informational; surfaces fleet update drift.",
              domain="security", hygiene=True, paused=True),
        alert("ts2o-contact-unverified", "Tailnet contact unverified",
              "max(tailscale_contact_needs_verification_ratio)", "gt", 0, "30m", "warning",
              "A tailnet contact needs verification",
              "tailscale_contact_needs_verification_ratio > 0 — a tailnet contact (account/support/security) "
              "is unverified, so Tailscale security notifications to it may not be delivered. Verify it in "
              "the admin console.",
              domain="security", hygiene=True, paused=False),
        # --- Task 2.3: fleet-hygiene (security) ---
        # --- WU12b: version skew ---
        alert("ts2o-device-version-skew-high", "Devices far behind latest version",
              "max(tailscale_device_version_skew_ratio)",
              "gt", 3, "1h", "info",
              "At least one device is >3 minor versions behind the fleet latest",
              "at least one device is >3 minor versions behind the fleet latest.",
              domain="security", hygiene=True, paused=True),
        alert("ts2o-devices-outdated", "Many devices outdated",
              "max(tailscale_devices_outdated_ratio)", "gt", 5, "1h", "info",
              "More than 5 devices are running an outdated client",
              "tailscale_devices_outdated_ratio > 5 — several clients are N+ minor versions behind the "
              "latest. Informational fleet hygiene; surfaces update drift.",
              domain="security", hygiene=True, paused=True),
        alert("ts2o-device-keys-expiring-7d", "Device keys expiring (<7d)",
              "count((max by (host_id) (tailscale_device_key_expiry_seconds) - time() < 7*86400) and "
              "(max by (host_id) (tailscale_device_key_expiry_seconds) - time() > 0))",
              "gt", 0, "1h", "warning",
              "Device node keys are expiring within 7 days",
              "One or more device node keys expire within 7 days (and are not already expired) — they will "
              "drop off the tailnet until re-authed. Computed from the per-device key-expiry gauge "
              "(expiry - now within (0, 7d]), so it clears after a key is rotated. Mirrors the "
              "ts2o-rec-keys-expiring-7d recording rule (NOT the cumulative key-expiry histogram buckets, "
              "which only grow and latch). Warning tier below ts2o-device-key-expiring-critical (48h).",
              domain="security", paused=False),
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
              domain="security", paused=False),
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
              domain="security", paused=False),
        # --- Task 2.4: security/governance (B1/B2/B7/A1/A2) ---
        alert("ts2o-acl-unrestricted", "Unrestricted ACL rules present",
              "sum(tailscale_acl_unrestricted_rules_ratio)", "gt", 0, "15m", "warning",
              "The tailnet policy has unrestricted (wide-open) rules",
              "sum(tailscale_acl_unrestricted_rules_ratio) > 0 — one or more ACL grants/rules are "
              "unrestricted (e.g. * to *). Review the ACL hygiene row on the Security & Audit tab.",
              domain="security", paused=False),
        alert("ts2o-acl-autoapprove-exit", "ACL auto-approves exit nodes",
              "sum(tailscale_acl_autoapprovers_ratio{tailscale_acl_autoapprover_kind=\"exit_node\"})",
              "gt", 0, "15m", "warning",
              "The tailnet policy auto-approves exit nodes",
              "An ACL autoApprovers stanza auto-approves exit nodes — new exit nodes go live without manual "
              "review. Confirm this is intended.",
              domain="security", paused=True),
        alert("ts2o-secret-scanner-fired", "Secret scanner fired",
              "sum(rate(tailscale_config_audit_changes_total{tailscale_actor_type=\"SECRET_SCANNER\"}[15m]))",
              "gt", 0, "0m", "critical",
              "Tailscale secret scanner detected an exposed credential",
              "An audit change was attributed to the SECRET_SCANNER actor — Tailscale's secret scanner "
              "acted on a leaked credential (e.g. revoked an exposed key). Investigate immediately.",
              domain="security", paused=False),
        alert("ts2o-tailnet-lock-disabled", "Tailnet lock disabled",
              "sum(rate(tailscale_config_audit_changes_total{tailscale_audit_change=\"tailnet_lock\", "
              "tailscale_audit_action=\"DISABLE\"}[15m]))",
              "gt", 0, "0m", "critical",
              "Tailnet lock was disabled",
              "An audit event disabled tailnet lock — node-key signing enforcement is off, weakening the "
              "tailnet's trust model. Confirm this was an authorized change.",
              domain="security", paused=True),
        alert("ts2o-user-role-escalation", "User role change",
              "sum(rate(tailscale_config_audit_changes_total{tailscale_audit_change=\"user_role\"}[15m]))",
              "gt", 0, "5m", "warning",
              "A user role was changed",
              "An audit event changed a user's role (e.g. member -> admin/owner) — a privilege change worth "
              "reviewing. Break down by actor on the Security & Audit tab.",
              domain="security", paused=True),
        alert("ts2o-acl-changed", "ACL changed",
              "sum(rate(tailscale_config_audit_changes_total{tailscale_audit_change=\"acl\"}[15m]))",
              "gt", 0, "5m", "info",
              "The tailnet ACL policy was changed",
              "An audit event modified the ACL/policy file — informational change tracking. Pair with the "
              "config-change row to see who/what changed.",
              domain="security", hygiene=True, paused=True),
        alert("ts2o-key-broad-scope", "Key with broad scope",
              "max(tailscale_key_scopes_ratio)", "gt", 10, "1h", "info",
              "An API/auth key has a very broad scope",
              "A key grants more than 10 scopes — broad credential blast radius. Review the credential "
              "scopes table on the Policy & Config tab and scope it down if possible.",
              domain="security", hygiene=True, paused=True),
        alert("ts2o-device-share-exit-node", "Device share grants exit node",
              "sum(tailscale_device_invites_count_ratio{tailscale_device_invite_allow_exit_node=\"true\"})",
              "gt", 0, "30m", "warning",
              "A device share allows exit-node use",
              "An outstanding device invite/share allows the recipient to use the device as an exit node — "
              "review whether routing the recipient's traffic is intended.",
              domain="security", paused=True),
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
              domain="security", hygiene=True, paused=True),
        alert("ts2o-device-approval-disabled", "Device approval disabled",
              'min(tailscale_setting_enabled_ratio{tailscale_setting_name="devices_approval"})',
              "lt", 1, "30m", "warning",
              "Tailnet device approval is disabled",
              "tailscale_setting_enabled_ratio{tailscale_setting_name=\"devices_approval\"} == 0 — new "
              "devices can join the tailnet without manual admin approval. Ships PAUSED because device "
              "approval is off by default in Tailscale and many tailnets run that way intentionally; "
              "enable this alert if your tailnet requires device approval. Absent (=> not firing) when "
              "the settings collector is disabled.",
              domain="security", hygiene=True, paused=True),
        alert("ts2o-logstream-config-changed", "Log-streaming (SIEM export) config changed",
              'sum(rate(tailscale_config_audit_changes_total{tailscale_audit_change="logstream_endpoint"}[15m]))',
              "gt", 0, "0m", "warning",
              "A configuration-log / SIEM streaming endpoint was changed",
              "An audit event changed a LOGSTREAM_ENDPOINT setting — a tailnet log-streaming (SIEM) sink "
              "was added, reconfigured, or removed. Removing/disabling it is a forensics/compliance gap. "
              "Audit-driven, so it fires on the change itself (catching a disable even if quickly "
              "reverted); pair with ts2o-logstream-delivery-failing (delivery health). Ships PAUSED — "
              "enable where log-export changes must be reviewed.",
              domain="security", hygiene=True, paused=True),
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
              domain="security", paused=False),
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
              domain="security", paused=False),
        alert("ts2o-logstream-delivery-failing", "Log-stream delivery failing",
              "sum by (tailscale_logstream_type) (rate(tailscale_logstream_requests_failed_total[15m]))",
              "gt", 0, "15m", "warning",
              "Log streaming to the SIEM sink is failing ({{ $labels.tailscale_logstream_type }})",
              "rate(tailscale_logstream_requests_failed_total) > 0 for {{ $labels.tailscale_logstream_type }} "
              "logs — Tailscale is failing to deliver to the configured SIEM sink (a compliance/forensics "
              "gap). Absent until a log stream is configured.",
              domain="infra", paused=False),
        alert("ts2o-logstream-stalled", "Log-stream stalled",
              "max by (tailscale_logstream_type) (time() - tailscale_logstream_last_activity_seconds)",
              "gt", 3600, "30m", "warning",
              "Log stream {{ $labels.tailscale_logstream_type }} has no recent delivery activity",
              "No log-stream delivery activity for over 1h while a stream is configured — delivery has "
              "stalled. Absent until a log stream is configured.",
              domain="infra", paused=True),
        alert("ts2o-logstream-backpressure", "Log-stream backpressure",
              "sum by (tailscale_logstream_type) (rate(tailscale_logstream_max_body_requests_total[15m]))",
              "gt", 0, "15m", "info",
              "Log stream {{ $labels.tailscale_logstream_type }} hitting max body size",
              "Delivery requests are hitting the maximum body size — SIEM backpressure. Informational.",
              domain="infra", paused=True),
        alert("ts2o-logstream-spoofed", "Log-stream spoofed entries",
              "sum by (tailscale_logstream_type) (rate(tailscale_logstream_spoofed_entries_total[15m]))",
              "gt", 0, "15m", "warning",
              "Log stream {{ $labels.tailscale_logstream_type }} rejecting spoofed entries",
              "Tailscale is rejecting log entries as spoofed — investigate the source of the spoofed log "
              "traffic to the streaming endpoint.",
              domain="security", paused=True),
        # --- WU12b: receiver health ---
        alert("ts2o-receiver-rejections", "Receiver rejecting ingest",
              '(sum(rate(tailscale_stream_rejected_total[10m])) or vector(0)) + '
              '(sum(rate(tailscale_webhook_rejected_total[10m])) or vector(0))',
              "gt", 0, "10m", "warning",
              "Stream/webhook receiver is rejecting inbound ingest",
              "The stream or webhook receiver is rejecting inbound events (spoofed/oversized/decode errors) "
              "— investigate the sender or HEC/webhook config.",
              domain="infra", paused=True, nodata="OK"),
        alert("ts2o-receiver-latency-high", "Receiver request latency high (p99)",
              "histogram_quantile(0.99, sum by (le) (rate(tailscale_stream_request_duration_seconds_bucket[10m])))",
              "gt", 5, "10m", "warning",
              "Stream-receiver p99 request latency is above 5s",
              "p99 stream-receiver request latency above 5s — receiver backpressure.",
              domain="observability", paused=True, nodata="OK"),
        # --- WU12b: posture integration match rate ---
        alert("ts2o-posture-match-low", "Posture integration match rate low",
              "min(tailscale_posture_integration_matched_ratio / "
              "(tailscale_posture_integration_possible_matched_ratio > 0))",
              "lt", 0.8, "1h", "warning",
              "A posture integration is matching <80% of possible devices",
              "An MDM/EDR posture integration is matching <80% of possible devices — devices may be "
              "bypassing posture gates.",
              domain="security", paused=True, nodata="OK"),
    ]

    network = [
        alert("ts2o-high-derp-relay-usage", "High DERP relay usage",
              _derp_byte_fraction(),
              "gt", 0.5, "30m", "warning",
              "Most tailnet traffic is relayed via DERP, not direct",
              "Over 50% of fleet bytes are relayed via DERP rather than sent peer-to-peer for 30m — a "
              "NAT-traversal/connectivity problem adding latency. Requires the node-metrics scraper "
              "(absent => not firing).",
              domain="infra", paused=False),
        alert("ts2o-derp-region-latency-high", "DERP region latency high",
              "max by (tailscale_derp_region) (tailscale_derp_region_latency_min_seconds)",
              "gt", 0.15, "15m", "info",
              "Best latency to DERP region {{ $labels.tailscale_derp_region }} is high",
              "Even the best device->DERP latency for region {{ $labels.tailscale_derp_region }} exceeds "
              "150ms — poor connectivity to that region. Gated by cardinality.derp_region_rollup.",
              domain="infra", paused=True),
        alert("ts2o-no-flow-data", "No flow data",
              "sum(rate(tailscale_network_flows_total[30m]))", "lt", 0.0001, "1h", "info",
              "No network flow records for an hour",
              "sum(rate(tailscale_network_flows_total[30m])) ~ 0 for 1h while flow logging is on — the flow "
              "pipeline may be stalled (or the tailnet is genuinely idle; tune/disable as needed). Absent "
              "when flow logging is off.",
              domain="infra", paused=True),
        # --- Task 2.3: routing (infra) ---
        alert("ts2o-subnet-routes-unapproved", "Unapproved subnet routes",
              "max(tailscale_subnet_routes_unapproved)", "gt", 0, "30m", "warning",
              "Subnet routes are advertised but unapproved",
              "tailscale_subnet_routes_unapproved > 0 — a device is advertising subnet routes that an admin "
              "has not approved, so those subnets are not reachable. Approve or reject in the admin console.",
              domain="infra", paused=True),
        alert("ts2o-exit-node-no-failover", "Subnet route has no failover",
              "count(tailscale_subnet_routes_routers_ratio == 1)", "gt", 0, "30m", "info",
              "A subnet/CIDR is served by a single router (no failover)",
              "count(tailscale_subnet_routes_routers_ratio == 1) > 0 — one or more CIDRs are advertised by "
              "exactly one router, so there is no failover if it goes offline. Add a second subnet router.",
              domain="infra", paused=True),
        # --- WU12b: NAT / dropped-packets / HA ---
        alert("ts2o-hard-nat-high", "Fleet hard-NAT fraction high",
              "sum(tailscale_devices_hard_nat_ratio) / sum(tailscale_devices_count_ratio)",
              "gt", 0.5, "30m", "info",
              "More than 50% of fleet devices are behind hard NAT",
              ">50% of devices behind hard NAT — relay pressure / connectivity degradation.",
              domain="infra", paused=True),
        alert("ts2o-node-dropped-packets", "tailscaled dropping outbound packets",
              "sum(rate(tailscaled_outbound_dropped_packets_total[10m]))",
              "gt", 0, "15m", "info",
              "Nodes are dropping outbound packets (sustained)",
              "nodes are dropping outbound packets (sustained) — a connectivity-degradation signal.",
              domain="infra", paused=True, nodata="OK"),
        alert("ts2o-vip-service-no-ha", "VIP service has no host redundancy",
              "count(sum by (tailscale_service_name) (tailscale_service_hosts_ratio) == 1)",
              "gt", 0, "30m", "info",
              "One or more VIP services are backed by a single host (no HA)",
              "one or more Tailscale (VIP) services are backed by a single host (no HA).",
              domain="infra", paused=True, nodata="OK"),
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
              domain="infra", paused=True, nodata="OK"),
        # --- Task 2.5: per-tailnet API errors (F) ---
        alert("ts2o-tailnet-api-errors", "Per-tailnet API errors",
              "sum by (tailscale_tailnet) (rate(tailscale2otel_api_requests_total"
              "{http_response_status_code=~\"4..|5..\", tailscale_tailnet!=\"\"}[10m]))",
              "gt", 0, "15m", "warning",
              "Tailscale API errors for tailnet {{ $labels.tailscale_tailnet }}",
              "Per-tailnet 4xx/5xx API error rate > 0 for {{ $labels.tailscale_tailnet }} — one tailnet's "
              "polling is failing without masking the others (MSP/multi-tailnet). Check that tailnet's "
              "credentials.",
              domain="observability", paused=True),
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


def build():
    grps = [{"orgId": 1, "name": name, "folder": FOLDER, "interval": INTERVAL, "rules": rules}
            for (name, rules) in groups()]
    return {"apiVersion": 1, "groups": grps}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", required=True)
    ap.add_argument("--json", action="store_true", help="emit JSON instead (for validation)")
    args = ap.parse_args()
    doc = build()
    with open(args.out, "w") as f:
        if args.json:
            json.dump(doc, f, indent=2)
        else:
            f.write("# GENERATED by deploy/alerts/gen/build_rules.py — do not edit by hand.\n")
            f.write("# Grafana-managed alerting + recording rules (file provisioning).\n")
            f.write(yamlify(doc) + "\n")
    n_alert = sum(1 for _, rs in groups() for r in rs if "record" not in r)
    n_rec = sum(1 for _, rs in groups() for r in rs if "record" in r)
    n_paused = sum(1 for _, rs in groups() for r in rs if r["isPaused"])
    print("wrote %s  (%d alert rules, %d recording rules, %d paused)" % (args.out, n_alert, n_rec, n_paused))


if __name__ == "__main__":
    main()
