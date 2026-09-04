---
title: Alert Profiles
description: Installable alert profiles (baseline/recommended/strict) and how to materialize one
---

# Alert installation profiles

<!-- GENERATED FILE. Do not hand-edit — regenerate with:
       python3 deploy/alerts/gen/build_rules.py --docs-out docs/alert-profiles.md
     (or `just gen dashboards`, which regenerates this alongside
     deploy/alerts/grafana-managed/). Content is derived entirely from the PROFILES
     table and each rule's evaluation policy in deploy/alerts/gen/build_rules.py; a
     unittest in deploy/alerts/gen/test_rules.py fails the build if this file drifts.

     Recording-rule descriptions are surfaced in the generated catalogue below. The
     Grafana RecordingRule schema deliberately has no description/annotations field,
     so the manifests retain their exact schema while this page remains operator-visible.

     The deploy/alerts/README.md link below is ABSOLUTE on purpose: this page is published
     to the docs hub, which builds Zensical with strict = true, and deploy/ is outside docs/
     so a relative ../deploy/... target is a broken link there and fails that build. It has
     been hand-fixed in the generated file and silently regenerated away twice (18f38cb,
     45e489c, 3843477) — fix it HERE, never in docs/alert-profiles.md. -->

tailscale2otel's Grafana-managed alert catalogue (see
[deploy/alerts/README.md](https://github.com/rknightion/tailscale2otel/blob/main/deploy/alerts/README.md)) ships one committed manifest set:
the **recommended** profile below, which is also the default render of the current catalogue. `baseline` and
`strict` are alternative *installable* profiles — materialize either on demand with:

```sh
python3 deploy/alerts/gen/build_rules.py --profile <name> --out <dir>
gcx resources push -p <dir>
```

Neither `baseline` nor `strict` is committed to this repository: three near-duplicate
copies of ~120 manifests would bury every real diff behind profile-only churn, so
materializing another profile is a command, not a checked-in directory.

## `baseline`

The smallest set worth waking someone up to. Enables only coverage_critical (the exporter itself is down) and core-policy rules (a signal every running exporter always emits, so its absence is always abnormal) — nothing that needs an optional collector or feature turned on, and nothing that needs a site-specific threshold tuned first. Recording rules keep their recommended paused state; they never page on their own.

- Alert rules: **11 enabled**, 99 paused
- Recording rules: **8 enabled**, 15 paused

## `recommended`

Preserves every rule's authored paused state from the current catalogue. An explicit recommended render is byte-identical to what `--out` produces with no `--profile` flag.

- Alert rules: **43 enabled**, 67 paused
- Recording rules: **8 enabled**, 15 paused

## `strict`

Enables every alert and every recording rule EXCEPT the explicit exceptions below, which stay paused because enabling them blind is actively misleading rather than merely noisy — a documented placeholder threshold, a per-plan ingest-cost budget, or a signal that is legitimately absent on a healthy, idle deployment.

- Alert rules: **107 enabled**, 3 paused
- Recording rules: **23 enabled**, 0 paused

Explicit exceptions (stay paused, with a reason):

- `ts2o-api-rate-limit-wait-high` (API rate-limiter wait high) — strict exception: the rule's own description says its 5s threshold IS A PLACEHOLDER pending a real per-site baseline; enabling it blind pages on a busy tailnet's normal rate-limiter wait time rather than on an actual problem.
- `ts2o-export-volume-high` (Export volume high) — strict exception: its 5000/s threshold is a Grafana Cloud ingest-cost budget tied to one specific plan, not a correctness signal, so enabling it fleet-wide pages on someone else's billing tier rather than on a real export problem.
- `ts2o-ingest-data-stale` (Accepted ingest data stale) — strict exception: it fires on any legitimately idle sparse ingestion source — a quiet webhook or audit stream with nothing to report is not a fault, and the rule ships paused specifically so it is enabled per source/signal pair with a threshold tuned to that workload.

## Lease coordination coverage

`CoordinationNoLeader`, `CoordinationSplitBrain`, and `CoordinationNoStandby`
aggregate the current leadership gauge by Lease and namespace. They are enabled
advisory rules with no `page` label: observe their behaviour before raising their
notification tier. The Lease leadership panel retains the identity and state labels
needed to identify the contenders.

Leadership flapping is intentionally not a shipped rule. The available
`tailscale2otel_coordination_leader_ratio` signal is a synchronous last-value gauge,
not a handover counter or an event stream; its state-labelled series can linger at
their last value. A `changes()` expression would count individual series changes or
their retention behaviour, not completed Lease handovers, and would therefore turn
normal process churn into a misleading alert. Add an explicit, monotonic handover
counter or timestamped transition signal before alerting on flapping.

TSO-0119 now exposes process-level coordination telemetry on standby and stepped-down
Prometheus pull endpoints while keeping collector telemetry leader-only. `CoordinationNoStandby`
therefore counts identities whose all-state leader-gauge sum remains zero; it does not
select a raw `coordination_state="standby"` series, because a promoted leader retains
that old zero-valued state under synchronous last-value aggregation. Its 10m `for`
window deliberately waits through the backend's stale samples after a replica loss.

## Recording rules

These derived series are written by the recording-rule catalogue. The descriptions below explain what each series means; profile state is shown so an operator can tell whether it is active after materializing a profile.

| UID | Recorded metric | baseline | recommended | strict | Description |
|---|---|---|---|---|---|
| `ts2o-rec-api-error-ratio` | `tailscale2otel:api_requests:error_ratio` | paused | paused | enabled | Tailscale API 5xx error ratio (5m). |
| `ts2o-rec-derp-byte-fraction` | `tailscale:derp_relay:byte_fraction` | enabled | enabled | enabled | Fleet fraction of bytes relayed via DERP (precomputes the heavy 4-rate dashboard/alert query). |
| `ts2o-rec-devices-online` | `tailscale:devices_online:count` | paused | paused | enabled | Fleet devices currently online (deploy-stable count). |
| `ts2o-rec-devices-unauthorized` | `tailscale:devices_unauthorized:count` | enabled | enabled | enabled | Internal devices awaiting admin approval, per tailnet. Keeps tailscale_tailnet because on a multi-tailnet/MSP deployment a summed count hides WHICH tailnet has the unapproved device, which is the only actionable part. Excludes external (shared-in) devices on purpose: a device shared from another tailnet is not yours to approve. Consumed by ts2o-devices-unauthorized (#410), so it ships ENABLED. |
| `ts2o-rec-direct-byte-fraction` | `tailscale:direct_path:byte_fraction` | paused | paused | enabled | Fleet fraction of bytes carried peer-to-peer. The complement view to tailscale:derp_relay:byte_fraction and built by the SAME helper, so the two cannot drift apart. Note the pair does not sum to 1: peer-relay is a third path. |
| `ts2o-rec-export-success-by-signal` | `tailscale2otel:export:success_ratio` | paused | paused | enabled | OTLP export success ratio per signal. Keeps `signal` because a backend can accept metrics while rejecting logs, and one blended number averages that away. This is the per-signal DIAGNOSTIC view; tailscale2otel:sli_delivery:ratio is the deliberately separate aggregate the SLO burns against. |
| `ts2o-rec-flow-throughput` | `tailscale:flow_throughput:bytes:rate5m` | paused | paused | enabled | Total flow throughput (rollup if present, else raw). |
| `ts2o-rec-hard-nat-fraction` | `tailscale:devices_hard_nat:fraction` | paused | paused | enabled | Fraction of fleet devices behind hard NAT. |
| `ts2o-rec-ingest-freshness` | `tailscale2otel:ingest_event_freshness_seconds` | paused | paused | enabled | Seconds since the greatest accepted event timestamp per source/signal. Use only with workload-specific staleness thresholds; sparse sources can be legitimately idle. |
| `ts2o-rec-ingest-freshness-by-tailnet` | `tailscale2otel:ingest_freshness:by_tailnet` | paused | paused | enabled | Seconds since the newest accepted event, per source, signal AND tailnet. The per-tailnet companion to tailscale2otel:ingest_event_freshness_seconds, which stays as the fleet-wide view — both are kept because they answer different questions and the aggregate is the one an SLO should burn against. Same caveat as the aggregate: sparse sources are legitimately idle, so use it only with workload-specific thresholds. |
| `ts2o-rec-ingest-records-by-source` | `tailscale2otel:ingest_records:rate5m` | paused | paused | enabled | Accepted record rate per ingestion path, signal AND tailnet. `source`=poll\|stream\|webhook\|objectstore, `signal`=flow\|audit\|webhook. The canonical cross-source comparison: poll vs HEC vs webhook vs object store on one footing. Keeps tailscale_tailnet so a single silent tailnet cannot hide behind a healthy fleet total. Grouping by an absent label is harmless, so this still works when the operator has disabled the tailnet attribute category. |
| `ts2o-rec-ingest-rejected-by-source` | `tailscale2otel:ingest_rejected:rate5m` | paused | paused | enabled | Rejection rate unified across ingestion paths, which otherwise use three differently-named metrics and cannot be compared on one panel. Uses the label_replace + `or` union rather than addition for the same reason the path-fraction helper does: `+` is a one-to-one join that silently drops any source present in only one of the operands, which is the normal case here since receivers are independently optional. |
| `ts2o-rec-keys-expiring-7d` | `tailscale:device_keys_expiring_7d:count` | paused | paused | enabled | Device node keys expiring within 7 days (and not already expired). |
| `ts2o-rec-node-dropped-packets` | `tailscale:node_dropped_packets:rate5m` | paused | paused | enabled | Per-node outbound dropped-packet rate (5m). |
| `ts2o-rec-objectstore-backlog` | `tailscale2otel:objectstore_backlog:max` | paused | paused | enabled | Objects listed but not yet ingested. Fully aggregated — the object-store path is a single pipeline, so no label is meaningful. A lower bound while objectstore.scan.truncated is 1. |
| `ts2o-rec-posture-autoupdate` | `tailscale:posture_autoupdate:ratio` | enabled | enabled | enabled | Fraction of devices with client auto-update enabled (feeds PostureAutoUpdateLow + the Security tab). |
| `ts2o-rec-posture-encrypted` | `tailscale:posture_encrypted:ratio` | enabled | enabled | enabled | Fraction of devices reporting an encrypted local state store. |
| `ts2o-rec-scrape-freshness` | `tailscale2otel:scrape_freshness:seconds` | paused | paused | enabled | Seconds since each collector's last SUCCESSFUL scrape. Keeps tailscale_collector: bounded (~15 collectors) and useless aggregated, since "something is stale" is only actionable once it names the collector. |
| `ts2o-rec-series-active-sum` | `tailscale2otel:series_active:sum` | enabled | enabled | enabled | Total active series across all tailscale2otel metrics — an ingest-cost proxy. |
| `ts2o-rec-series-by-group` | `tailscale2otel:series_active:by_group` | paused | paused | enabled | Active series per metric group — the cardinality/cost driver view. |
| `ts2o-rec-sli-availability` | `tailscale2otel:sli_availability:ratio` | enabled | enabled | enabled | SLI: the exporter is running and emitting telemetry. Target 99.9%. This is NOT a statement about the tailnet or the backend. |
| `ts2o-rec-sli-delivery` | `tailscale2otel:sli_delivery:ratio` | enabled | enabled | enabled | SLI: the OTLP backend is accepting exports. Target 99%. A drop here is a BACKEND fault — the exporter and the tailnet are healthy — which is exactly why it is a separate SLI rather than folded into availability. |
| `ts2o-rec-sli-freshness` | `tailscale2otel:sli_freshness:ratio` | enabled | enabled | enabled | SLI: fraction of collectors whose last scrape succeeded — collection is current. Target 99%. Degrades when the Tailscale API or a single collector is failing, independently of whether the exporter is up or the backend is reachable. |
