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
    return {
        "uid": uid, "title": metric,
        "data": [_query_node(expr, ds)],
        "record": {"metric": metric, "from": "A"},
        "labels": {"service": "tailscale2otel", "domain": domain},
        "annotations": {"description": desc},
        "isPaused": paused,
    }


# ---------------------------------------------------------------------------
# the catalogue (star = recommended starter set -> enabled; rest paused)
# ---------------------------------------------------------------------------

def groups():
    health = [
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
              "tailscale2otel_series_limit",
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
              "Sustained evictions from dedup set {{ $labels.dedup_set }} — it is undersized, so the "
              "cross-source de-duplication failsafe may be letting duplicates through (double counting). "
              "Increase the dedup set size or fix the poll-vs-stream overlap.",
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
        alert("ts2o-devices-outdated", "Many devices outdated",
              "max(tailscale_devices_outdated_ratio)", "gt", 5, "1h", "info",
              "More than 5 devices are running an outdated client",
              "tailscale_devices_outdated_ratio > 5 — several clients are N+ minor versions behind the "
              "latest. Informational fleet hygiene; surfaces update drift.",
              domain="security", hygiene=True, paused=True),
        alert("ts2o-device-keys-expiring-7d", "Device keys expiring (<7d)",
              "sum(tailscale_devices_key_expiry_days_bucket{le=\"7\"}) - "
              "sum(tailscale_devices_key_expiry_days_bucket{le=\"0\"})",
              "gt", 0, "1h", "warning",
              "Device node keys are expiring within 7 days",
              "One or more device node keys expire within 7 days (and are not already expired) — they will "
              "drop off the tailnet until re-authed. Computed from the key-expiry histogram (le=7 minus "
              "le=0 buckets).",
              domain="security", paused=True),
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
    ]

    integrations = [
        alert("ts2o-posture-integration-stale", "Posture integration sync stale",
              "max by (tailscale_posture_provider, tailscale_posture_integration) "
              "(time() - tailscale_posture_integration_last_sync_seconds)",
              "gt", 86400, "1h", "warning",
              "Posture integration {{ $labels.tailscale_posture_provider }} sync is stale",
              "A device-posture (MDM/EDR) integration has not synced in over 24h "
              "({{ $labels.tailscale_posture_provider }}/{{ $labels.tailscale_posture_integration }}) — "
              "posture data from it is stale. Absent until an integration exists.",
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
    ]

    network = [
        alert("ts2o-high-derp-relay-usage", "High DERP relay usage",
              "sum(rate(tailscaled_inbound_bytes_total{path=\"derp\"}[10m]) + "
              "rate(tailscaled_outbound_bytes_total{path=\"derp\"}[10m])) / "
              "clamp_min(sum(rate(tailscaled_inbound_bytes_total[10m]) + "
              "rate(tailscaled_outbound_bytes_total[10m])), 1)",
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
        # --- Task 2.5: per-tailnet API errors (F) ---
        alert("ts2o-tailnet-api-errors", "Per-tailnet API errors",
              "sum by (tailscale_tailnet) (rate(tailscale2otel_api_requests_total"
              "{http_response_status_code=~\"4..|5..\"}[10m]) * on(job,instance) "
              "group_left(tailscale_tailnet) target_info)",
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
               "sum(rate(tailscaled_inbound_bytes_total{path=\"derp\"}[10m]) + "
               "rate(tailscaled_outbound_bytes_total{path=\"derp\"}[10m])) / "
               "clamp_min(sum(rate(tailscaled_inbound_bytes_total[10m]) + "
               "rate(tailscaled_outbound_bytes_total[10m])), 1)",
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
