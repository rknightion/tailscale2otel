---
id: TSO-0036
title: PAM telemetry collector (defer until API surface publishes)
status: Parked
assignee: []
created_date: '2026-08-30 09:09'
updated_date: '2026-09-03 19:55'
labels: []
milestone: m-8
dependencies: []
priority: low
ordinal: 39000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Tailscale PAM went beta 2026-08-26 (Border0 acquisition); PAM service accounts call a PAM API but no endpoints are in the published OpenAPI spec yet. We have no PAM setup, so this is spec-driven only: placeholder tracking task - when PAM endpoints appear in the vendored spec (the daily api-drift lane will surface them), design a collector for session counts/durations by service type, JIT access-request rates and recording-storage settings. Do not build ahead of the spec.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Revisited when PAM operations appear in spec/tailscale-api.json; scope defined then
- [ ] #2 Until then the operations (when they appear) get explicit dispositions rather than sitting unadjudicated
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Upstream check 2026-09-01 against the vendored spec/tailscale-api.json (60 paths): zero paths matching pam/border0/session/recording. PAM operations still absent, so this stays correctly blocked on upstream and is excluded from Wave 5. Re-check is free: the daily api-drift lane surfaces new paths, or rerun the same path scan after any spec re-vendor.

Parked 2026-09-03 by owner decision. Blocked on Tailscale publishing a PAM telemetry API surface; no wave can drain it. Resume boundary: when the Tailscale API spec re-vendor (spec/tailscale-api.json) first carries PAM endpoints, move back to To Do and scope a collector against them. Parked rather than left in To Do so the board reads as genuinely drained, which is what the v5 trigger keys on.
<!-- SECTION:NOTES:END -->
