---
id: TSO-0071
title: Per-tailnet Tailscale API rate-limit utilization gauge
status: In Progress
assignee: []
created_date: '2026-08-30 09:34'
updated_date: '2026-08-31 00:29'
labels: []
milestone: m-5
dependencies: []
priority: medium
ordinal: 74000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
WaitDuration exists per request in internal/tsapi/transport.go but no utilization/queue-depth gauge tells an MSP which tailnet is closest to saturating the shared Tailscale API quota before requests visibly queue. Export a per-tailnet utilization or wait-time signal, catalogue it, and surface it on the health dashboard API panel.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A per-tailnet gauge/histogram exposes rate-limit pressure before saturation
- [ ] #2 Catalogued and visualized on the health dashboard
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Lane D implements per-tailnet API rate-limit utilization from transport response state, with cardinality-bounded telemetry and an assigned-tab panel.
<!-- SECTION:PLAN:END -->
