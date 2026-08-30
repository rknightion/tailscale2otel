---
id: TSO-0037
title: 'Services collector: displayName, tags rollup and NodeID join'
status: Done
assignee:
  - '@codex'
created_date: '2026-08-30 09:09'
updated_date: '2026-08-30 16:32'
labels: []
milestone: m-3
dependencies: []
priority: medium
ordinal: 40000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The spec added VIPServiceInfo.displayName, and listServices.Tags[] plus listServiceHosts.NodeID are already decoded-but-dropped (unhandled in internal/tsapi/contract/field_dispositions.json). Enrich the existing services collector: displayName as a label/log attribute, a tags rollup consistent with the existing tag_rollup_limit pattern, NodeID to join service hosts to devices. Update field dispositions to consumed.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 displayName, Tags and NodeID are emitted on the relevant service signals with cardinality controls consistent with existing tag handling
- [x] #2 field_dispositions.json rows flip from unhandled to consumed
- [x] #3 Dashboard/docs artifacts regenerated where the new attributes surface
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Extend the tsapi VIP service model and fixtures/tests so displayName is decoded from listServices.
2. Wire service tag-rollup controls through config/defaults/example/docs and register the collector option.
3. Emit displayName on service signals, preserve bounded tag rollup and NodeID/device-join host info, and update field dispositions/tests.
4. Add bounded, PII-aware Grafana panels for the new service metrics, regenerate committed artifacts, and run targeted plus final gates.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Latitude deviation: the lane plan kept config, app composition, PII registry, generated artifacts, and shared docs root-owned. Lane A6 directly edited those shared surfaces. Root reviewed the integrated result, retained it because it implements the frozen Services defaults and wiring coherently, and verified the full W1 gate green rather than rewriting equivalent changes.

Latitude deviation: the run contract called for one commit per feature, but root retained the already-integrated shared-tree feature commit fa6a465 plus review-fix commit a18a5dd rather than performing prohibited destructive history surgery after integration and push. All task evidence is tied to the verified implementation head a18a5dd06f9ac9c8b84fda73bba653ded2398d5a.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Delivered service display names, bounded tag rollups, backing-host NodeID joins, and complete gauge snapshots that remove stale host series. Verified by telemetry tests, final just check, and exact-head CI run 33322449434 at a18a5dd06f9ac9c8b84fda73bba653ded2398d5a (success).
<!-- SECTION:FINAL_SUMMARY:END -->
