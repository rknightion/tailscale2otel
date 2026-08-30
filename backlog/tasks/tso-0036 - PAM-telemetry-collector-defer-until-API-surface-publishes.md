---
id: TSO-0036
title: PAM telemetry collector (defer until API surface publishes)
status: To Do
assignee: []
created_date: '2026-08-30 09:09'
labels: []
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
