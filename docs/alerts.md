---
title: Alerts
description: Grafana-managed alert and recording rules shipped with tailscale2otel
tags:
  - Alerting
---

# Alerts

tailscale2otel ships ready-to-use alert and recording rules in
[`deploy/alerts/grafana-managed/`](https://github.com/rknightion/tailscale2otel/tree/main/deploy/alerts/grafana-managed)
as **`rules.alerting.grafana.app` manifests** — one JSON file per rule, plus a folder manifest.
Push the whole directory with [`gcx`](https://github.com/grafana/gcx):

```bash
gcx resources push -p deploy/alerts/grafana-managed
```

Grafana evaluates these itself, so one ruleset can span Prometheus and Loki and each rule carries
its own `noDataState` / `execErrState` / `paused`.

!!! note "One delivery model, on purpose"
    Earlier versions also shipped a hand-maintained Prometheus-ruler file
    (`tailscale2otel.rules.yaml`) and a Grafana *file-provisioning* document
    (`tailscale2otel.grafana-rules.yaml`, `apiVersion: 1`). Both are gone. File provisioning is
    not what `gcx resources push` consumes, and a second ruler-compatible copy of the same
    catalogue drifted more than it helped. If you run a Mimir/Cortex/Prometheus ruler and want
    datasource-managed rules, render them from the generator yourself — see
    [the test-only Prometheus rendering](#executing-the-rules-locally) — and own the result.

## Generated, not hand-written

The manifests are **generated** by
[`gen/build_rules.py`](https://github.com/rknightion/tailscale2otel/blob/main/deploy/alerts/gen/build_rules.py).
Edit the generator, not the JSON, and regenerate with:

```bash
python3 deploy/alerts/gen/build_rules.py --out deploy/alerts/grafana-managed
```

Every `*.json` in the output directory is deleted before writing, so a renamed or removed rule
cannot linger as a stale file that keeps getting pushed. Output is sorted-key JSON with a trailing
newline — two runs are byte-identical, which is what makes the CI drift gate meaningful.

Every rule follows the canonical Grafana 3-node pipeline (A query → B reduce → C threshold),
expressed as the `spec.expressions` map keyed by refId with `C` marked `"source": true`. Datasource
UIDs default to the portable Grafana Cloud defaults (`grafanacloud-prom` / `grafanacloud-logs`);
swap them for a self-hosted stack.

Rules are organised into five families:

- **`tailscale2otel-health`** — exporter self-health (scrape staleness, cardinality cap, API auth
  failures, checkpoint errors, enrichment cache age, and more)
- **`tailscale2otel-security`** — tailnet security and governance (tailnet-lock errors, key expiry,
  posture coverage, unverified contacts). The ACL risk-scoring gauges and the curated
  `tailscale.config.audit.changes` counter are natural additions here — e.g.
  `tailscale_acl_unrestricted_rules_ratio > 0` (any-to-any non-deny rules),
  `tailscale_acl_ssh_wildcard_ratio > 0` (wildcard SSH rules), or
  `increase(tailscale_config_audit_changes_total{tailscale_audit_change="auth_provider"}[1h]) > 0`.
- **`tailscale2otel-integrations`** — MDM/EDR posture sync, log-stream delivery health, and a
  paused accepted-event staleness rule for continuously active source/signal pairs
- **`tailscale2otel-network`** — DERP relay usage, region latency, flow data presence and
  flow-log truncation, per-target node-metrics scrape health, and the webhook-only node IP-forwarding
  fault
- **`tailscale2otel-recording`** — precomputed recording rules (DERP byte fraction, posture ratios,
  total active series)

!!! danger "Format traps — three of these fail silently"
    The manifest format is **not** the `apiVersion: 1` provisioning format, and the differences do
    not announce themselves. `gen/validate_manifests.py` enforces all of them offline.

    | trap | the rule |
    | --- | --- |
    | state casing | `noDataState` spells its OK value **`"Ok"`**; `execErrState` spells its own **`"OK"`**. The asymmetry is real. `"OK"` in `noDataState` makes the rule un-deployable while every local check passes. |
    | durations | Go-style strings — `"30m0s"`, `"1h0m0s"`, `"0s"`. `"5m"` is rejected, and `relativeTimeRange` bounds are durations here, not integer seconds. |
    | panel links | the paired `__dashboardUid__` / `__panelId__` **annotations**, `__panelId__` a **string**. Top-level `dashboardUid`/`panelId` are provisioning-only and `additionalProperties: false` rejects them. |
    | recording rules | no `annotations`, `for`, `condition`, `noDataState` or `execErrState`. The spec is exactly `{title, trigger, metric, expressions, targetDatasourceUID, labels, paused}`. |

!!! tip "Limit-agnostic cardinality alerting"
    Prefer `count(tailscale2otel_series_overflowing_ratio == 1) > 0` for a cardinality-overflow
    alert — it needs no hardcoded threshold and stays correct when `cardinality.metric_limit` is
    changed. `tailscale2otel_scrape_budget_ratio` (last scrape duration ÷ interval; nearing `1` =
    risk of interval overrun) is another `tailscale2otel-health` signal worth enabling.

!!! tip "Default-disabled by design"
    Only a high-signal starter set ships with `spec.paused: false`. The rest are
    `spec.paused: true` — enable them in the Grafana UI once your tailnet has the relevant data.
    Pausing is orthogonal to the evaluation policy below: a paused rule still carries its declared
    no-data and error semantics, it just is not evaluating yet.

!!! tip "Evaluation policy — absence and error are per-rule, not global"
    Every rule declares one of four policies, which fix its `noDataState` and `execErrState`
    together. Previously all of them were fail-open on error, so a broken datasource or a malformed
    query read as *healthy* across the entire pack.

    | policy | noData | execErr | for |
    | --- | --- | --- | --- |
    | `coverage_critical` | `Alerting` | `Alerting` | the rule exists to notice something stopped — absence IS the alert |
    | `core` | `NoData` | `Error` | always emitted by a running exporter, so absence is abnormal |
    | `optional` | `Ok` | `Error` | absence means "not configured", but an error is still a fault |
    | `advisory` | `Ok` | `OK` | neither absence nor a transient error is actionable here |

    `ExporterDown` is the only `coverage_critical` rule. Per-collector scrape rules are `core`
    rather than `coverage_critical` on purpose: total absence means the exporter is gone, which
    `ExporterDown` already pages on, and promoting them would fan one outage into a page per
    collector.

    **Nothing here can watch Grafana's own ruler.** If the ruler stops evaluating, no rule fires,
    including the `coverage_critical` one. See
    [Runbooks — who watches the watcher](runbooks.md) for the three operator-owned answers.

!!! tip "Every alert links to a runbook and a panel"
    All 91 alert rules carry a `runbook_url` pointing at a section of
    [Runbooks](runbooks.md), and 90 of them carry the paired `__dashboardUid__`/`__panelId__`
    annotations naming a canonical panel. Panel ids are resolved **by title** against the generated
    dashboard at build time, and generation hard-fails on a title that matches zero or more than one
    panel — so a renumbered or renamed panel breaks the build instead of silently producing a dead
    link.

!!! tip "Alert on accepted data, not only a running receiver"
    `AcceptedIngestDataStale` compares `time()` with
    `tailscale2otel_ingest_last_event_timestamp_seconds`. It catches a receiver or poller that
    remains healthy while delivering old events. The rule ships paused because audit and webhook
    streams can be legitimately quiet. Enable it only for `source`/`signal` pairs expected to be
    continuous, add label filters, and tune the one-hour threshold to that workload. The paused
    `tailscale2otel:ingest_event_freshness_seconds` recording rule exposes the same calculation for
    custom alerts.

## Deploying

```bash
# push the whole directory (the folder manifest sorts first, so it lands first)
gcx resources push -p deploy/alerts/grafana-managed
```

Anything that speaks the Grafana app-platform API works too: `kubectl` against the Grafana API
server, or the Terraform `grafana_apps_rules_alertrule_v0alpha1` /
`grafana_apps_rules_recordingrule_v0alpha1` resources, which take the same spec.

`gcx resources validate -p deploy/alerts/grafana-managed` is a **client-side** check only —
`alertrules.rules.alerting.grafana.app` does not support server-side dry-run, so it confirms the
manifests parse and that the kind is served, nothing more. The spec check is
`python3 deploy/alerts/gen/validate_manifests.py`, which runs offline and in CI.

## Metric naming in rule expressions

All expressions query the **normalized Prometheus names** produced by Grafana Cloud's OTLP
pipeline, not the raw OTEL names. The same rules apply as in the dashboards: dots become
underscores, counters gain `_total`, and a gauge with unit `"1"` becomes `*_ratio`. See
[Metrics](metrics.md) for the full translation table.

!!! note "Non-Grafana backends"
    If you send metrics to a non-Grafana OTEL backend with different normalization rules, you will
    need to adjust the metric names in the rule expressions accordingly.

## Wiring notifications

Every rule sets a `severity` label (`critical` / `warning` / `info`), plus `service` and `domain`.
Wire those into your Grafana notification policy to fan alerts to the right contact points.

## Executing the rules locally

`promtool test rules` is the only thing that **runs** a rule rather than reading it, and it
understands no format but Prometheus's. The generator can therefore re-render the same catalogue as
a throwaway Prometheus rule file:

```bash
python3 deploy/alerts/gen/build_rules.py --prom-out deploy/alerts/.generated-prom-rules.yaml
promtool check rules deploy/alerts/.generated-prom-rules.yaml
promtool test rules deploy/alerts/tests/*.yaml
```

**That file is a test fixture, not a deliverable** — it is gitignored, never committed and never
shipped. It omits the Loki-backed rules (promtool cannot parse LogQL) and renders
`coverage_critical`'s "absence is the alert" semantics as an explicit `or absent(...)` arm, which
Grafana expresses as `noDataState: Alerting`.

The offline contract tests cover the rest:

```bash
python3 -m unittest discover -s deploy/alerts/gen -t deploy/alerts/gen
```
