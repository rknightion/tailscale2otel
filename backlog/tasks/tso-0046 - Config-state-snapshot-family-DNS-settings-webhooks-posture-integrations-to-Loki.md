---
id: TSO-0046
title: >-
  Config-state snapshot family: DNS, settings, webhooks, posture integrations to
  Loki
status: To Do
assignee: []
created_date: '2026-08-30 09:27'
updated_date: '2026-08-30 09:48'
labels: []
milestone: m-2
dependencies:
  - TSO-0044
priority: medium
ordinal: 49000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Generalize the policy-snapshot pattern (TSO-0044): a shared snapshot-emitter helper that any collector can opt into, emitting a JSON snapshot log record on change (plus periodic heartbeat) for DNS configuration, tailnet settings, webhook configs and posture integrations. Each Grafana tab then shows current config + change history alongside its metrics. Per-source opt-in config keys; consistent attribute marking so dashboards query snapshots uniformly.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 One shared emitter with per-collector opt-in produces on-change JSON snapshot records for at least DNS config and tailnet settings
- [ ] #2 Snapshot records share a uniform attribute shape usable by one dashboard query pattern
- [ ] #3 Webhooks and posture integrations covered or explicitly parked with a note
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
