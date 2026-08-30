---
id: TSO-0046
title: >-
  Config-state snapshot family: DNS, settings, webhooks, posture integrations to
  Loki
status: In Progress
assignee:
  - '@codex'
created_date: '2026-08-30 09:27'
updated_date: '2026-08-30 14:00'
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

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 2 root freeze plan: add behavior-preserving per-source snapshot opt-ins for DNS, settings, webhooks, and posture integrations, all default off, plus the shared snapshot emitter.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Latitude deviation: the goal described six hand-maintained config files, but the live TestDocsConfigurationMentionsEveryKey gate proved docs/configuration.md is a seventh required config surface. Added the affected reference entries rather than weakening or bypassing the guard.
<!-- SECTION:NOTES:END -->
