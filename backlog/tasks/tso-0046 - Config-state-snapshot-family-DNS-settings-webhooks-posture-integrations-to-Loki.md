---
id: TSO-0046
title: >-
  Config-state snapshot family: DNS, settings, webhooks, posture integrations to
  Loki
status: Done
assignee:
  - '@codex'
created_date: '2026-08-30 09:27'
updated_date: '2026-08-30 16:32'
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
- [x] #1 One shared emitter with per-collector opt-in produces on-change JSON snapshot records for at least DNS config and tailnet settings
- [x] #2 Snapshot records share a uniform attribute shape usable by one dashboard query pattern
- [x] #3 Webhooks and posture integrations covered or explicitly parked with a note
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 2 root freeze plan: add behavior-preserving per-source snapshot opt-ins for DNS, settings, webhooks, and posture integrations, all default off, plus the shared snapshot emitter.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Latitude deviation: the goal described six hand-maintained config files, but the live TestDocsConfigurationMentionsEveryKey gate proved docs/configuration.md is a seventh required config surface. Added the affected reference entries rather than weakening or bypassing the guard.

Latitude deviation: the run contract called for one commit per feature, but root retained the already-integrated shared-tree feature commit fa6a465 plus review-fix commit a18a5dd rather than performing prohibited destructive history surgery after integration and push. All task evidence is tied to the verified implementation head a18a5dd06f9ac9c8b84fda73bba653ded2398d5a.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Implemented uniform opt-in DNS, settings, webhook, and posture-integration snapshots through the shared emitter, with secrets excluded and common query attributes. Verified by per-family telemetry tests, generated catalog guards, final just check, and exact-head CI run 33322449434 at a18a5dd06f9ac9c8b84fda73bba653ded2398d5a (success).
<!-- SECTION:FINAL_SUMMARY:END -->
