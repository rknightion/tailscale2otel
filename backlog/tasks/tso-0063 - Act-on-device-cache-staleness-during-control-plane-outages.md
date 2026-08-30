---
id: TSO-0063
title: Act on device-cache staleness during control-plane outages
status: In Progress
assignee: []
created_date: '2026-08-30 09:31'
updated_date: '2026-08-30 23:22'
labels: []
milestone: m-4
dependencies: []
priority: medium
ordinal: 66000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
enrich.DeviceCache exports Age() for the enrich.cache_age gauge (internal/enrich/devicecache.go:542-547) but nothing degrades or marks hours-stale authoritative names during a control-plane outage - they serve as fresh forever. Decide and implement the staleness action (e.g. staleness attribute on enriched signals past a threshold, status-page warning, or configurable behaviour). Also verify whether Replace() dropping the whole unverified tier each poll (devicecache.go:200-219) visibly flaps labels for devices only ever seen via flow-embedded identity, and fix if so.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Staleness past a threshold has a defined, observable behaviour (not silent fresh-forever)
- [ ] #2 The unverified-tier flap question is answered with evidence and fixed or documented
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Root F1 freezes the device-cache stale threshold with a non-disruptive default; lane D later implements observable stale behaviour and investigates unverified-tier replacement.
<!-- SECTION:PLAN:END -->
