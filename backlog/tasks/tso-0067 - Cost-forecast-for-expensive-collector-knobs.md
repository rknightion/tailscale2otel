---
id: TSO-0067
title: Cost forecast for expensive collector knobs
status: To Do
assignee: []
created_date: '2026-08-30 09:34'
updated_date: '2026-08-30 09:48'
labels: []
milestone: m-5
dependencies: []
priority: medium
ordinal: 70000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Options like collect_posture, collect_device_invites, services.collect_hosts, identity_dims and the posture wildcard carry prose warnings only; operators discover the API-call and series cost live. The app already knows fleet size and current series counts - provide a status-page and/or -preflight estimate ("enabling collect_posture adds ~N API calls/tick; identity_dims adds ~N series") before the knob is enabled. Extends the existing acknowledge_cardinality advisory pattern.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 For at least the per-device subrequest knobs and identity_dims, an estimate of added API calls/tick and series is available before enabling
- [ ] #2 Estimates derive from live fleet/series data, not hardcoded guesses
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
