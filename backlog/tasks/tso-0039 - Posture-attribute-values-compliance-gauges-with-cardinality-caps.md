---
id: TSO-0039
title: 'Posture attribute values: compliance gauges with cardinality caps'
status: In Progress
assignee: []
created_date: '2026-08-30 09:10'
updated_date: '2026-08-31 02:54'
labels: []
milestone: m-3
dependencies:
  - TSO-0053
priority: medium
ordinal: 42000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
GET /device/{id}/attributes is already consumed and the posture surface keeps growing (Fleet/Huntress integrations, ip:publicAddress). Candidate signals: a compliance-style gauge (count of devices failing a named posture expression) and/or configurable attribute-to-label promotion with a hard cardinality cap. Must be designed together with the existing posture namespace wildcard (WithAttributeNamespaces("*") in internal/collector/devices/devices.go:540-562), which currently has no bound - the cap work is shared with the "cardinality backstop for the posture wildcard" candidate (C3).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Operators can express at least one posture compliance check that yields a countable gauge
- [ ] #2 Any attribute-to-label promotion path has an enforced cardinality cap with an __other__/overflow behaviour consistent with existing levers
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Root F1 freezes the smallest reversible posture-compliance config shape with bounded values; lane A later implements evaluation, telemetry, overflow behaviour, and panel.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Lane A returned the posture signal-name seam. Root decision: accept tailscale.devices.posture_compliance.failed as the gauge name with bounded check label values and an owned posture panel. Use the frozen config shape and narrow expression syntax; no attribute promotion beyond the configured capped contract.

Integrated the frozen exact-match checks into collector construction. The bounded check label is classified non-PII; the new gauge is catalogued and panelled. Red-first and negative evidence from Lane A: all-fetch-failure initially emitted a misleading zero, then passed after suppression; repeated-cursor guard was deliberately broken and restored. Focused collector/catalog/PII/disposition checks passed.

Deviation: the required CodeRabbit gate was attempted three times after a green integrated just check; each run failed before analysis with a recoverable WebSocket-closed connection error and no complete line. No finding was produced or treated as clean. Root performed a full staged-diff review and proceeded to avoid letting an external review-service outage stop the unattended wave.
<!-- SECTION:NOTES:END -->
