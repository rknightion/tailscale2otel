---
id: TSO-0091
title: >-
  Dedup youngest-eviction-age gauge latches for the process lifetime with no
  reset path
status: In Progress
assignee: []
created_date: '2026-08-30 18:32'
updated_date: '2026-08-31 00:37'
labels:
  - needs-triage
milestone: m-4
dependencies: []
priority: medium
ordinal: 92000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
dedup.Set.YoungestEvictionAge (internal/dedup/dedup.go:162-172, set in evictLocked at :183-190) keeps an all-time low-water mark and never decays it — deliberately, so a short burst stays visible after it subsides. e3caac4 fixed the immediate live symptom by scoping the exported overlap horizon to the poll path, so the shipped ts2o-dedup-youngest-eviction rule now has no denominator on stream- and objectstore-fed collectors and cannot fire there. That does NOT fix the latch on a genuinely polling deployment: one max_window catch-up after a restart pins the gauge below the horizon and the alert then fires for the life of the process with no way to resolve. The catalog description at internal/appcatalog/catalog.go:523 already names the right current-pressure signal ("evictions approaching the set capacity WITHIN A SINGLE POLL INTERVAL"), and the sibling rule ts2o-dedup-set-saturated ships PAUSED for the mirror-image reason. Decide between a windowed/decaying variant of the gauge, an alert expression built on per-interval eviction pressure instead, or moving the latched value off alerting onto the status page and dashboard only.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A polling deployment that recovers from a catch-up burst sees the alert resolve without a process restart
- [ ] #2 The chosen mechanism is proven by a test that latches the gauge, clears the pressure, and asserts the alerting condition goes false
- [ ] #3 The dashboard/status-page consumer of the latched value is unchanged or explicitly re-sited
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Lane F chooses the narrowest reset mechanism among the three task options, implements it with TDD, and records the choice; it owns internal/dedup, internal/app/dedupobs.go, and tabs/policy_access.py plus policy_dns.py.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Open question resolved by root: use the narrowest reversible windowed-reset mechanism. Keep the existing metric name, type, labels, panel, and alert; atomically consume the minimum capacity-eviction residency age since the previous self-observability interval, clear the gauge series during intervals with no eviction, and retain the lifetime accessor only as an internal compatibility diagnostic. This lets the existing noDataState Ok rule resolve without restart.
<!-- SECTION:NOTES:END -->
