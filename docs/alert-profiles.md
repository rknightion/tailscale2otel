---
title: Alert Profiles
description: Installable alert profiles (baseline/recommended/strict) and how to materialize one
---

# Alert installation profiles

<!-- GENERATED FILE. Do not hand-edit — regenerate with:
       python3 deploy/alerts/gen/build_rules.py --docs-out docs/alert-profiles.md
     (or `scripts/regen-generated.sh dashboards`, which regenerates this alongside
     deploy/alerts/grafana-managed/). Content is derived entirely from the PROFILES
     table and each rule's evaluation policy in deploy/alerts/gen/build_rules.py; a
     unittest in deploy/alerts/gen/test_rules.py fails the build if this file drifts. -->

tailscale2otel's Grafana-managed alert catalogue (see
[deploy/alerts/README.md](../deploy/alerts/README.md)) ships one committed manifest set:
the **recommended** profile below, unchanged from every previous release. `baseline` and
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

- Alert rules: **11 enabled**, 86 paused
- Recording rules: **8 enabled**, 15 paused

## `recommended`

Today's shipped starter set, unchanged. The compatibility profile: its output is byte-identical to what tailscale2otel has always shipped in deploy/alerts/grafana-managed/, and is what `--out` produces with no `--profile` flag.

- Alert rules: **32 enabled**, 65 paused
- Recording rules: **8 enabled**, 15 paused

## `strict`

Enables every alert and every recording rule EXCEPT the explicit exceptions below, which stay paused because enabling them blind is actively misleading rather than merely noisy — a documented placeholder threshold, a per-plan ingest-cost budget, or a signal that is legitimately absent on a healthy, idle deployment.

- Alert rules: **94 enabled**, 3 paused
- Recording rules: **23 enabled**, 0 paused

Explicit exceptions (stay paused, with a reason):

- `ts2o-api-rate-limit-wait-high` (API rate-limiter wait high) — strict exception: the rule's own description says its 5s threshold IS A PLACEHOLDER pending a real per-site baseline; enabling it blind pages on a busy tailnet's normal rate-limiter wait time rather than on an actual problem.
- `ts2o-export-volume-high` (Export volume high) — strict exception: its 5000/s threshold is a Grafana Cloud ingest-cost budget tied to one specific plan, not a correctness signal, so enabling it fleet-wide pages on someone else's billing tier rather than on a real export problem.
- `ts2o-ingest-data-stale` (Accepted ingest data stale) — strict exception: it fires on any legitimately idle sparse ingestion source — a quiet webhook or audit stream with nothing to report is not a fault, and the rule ships paused specifically so it is enabled per source/signal pair with a threshold tuned to that workload.
