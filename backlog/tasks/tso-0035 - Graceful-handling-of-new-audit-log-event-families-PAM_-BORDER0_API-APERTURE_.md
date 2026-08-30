---
id: TSO-0035
title: >-
  Graceful handling of new audit-log event families (PAM_*, BORDER0_API,
  APERTURE_*)
status: To Do
assignee: []
created_date: '2026-08-30 09:09'
updated_date: '2026-08-30 09:47'
labels: []
milestone: m-3
dependencies: []
priority: medium
ordinal: 38000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The re-vendored spec added ~23 PAM_* configuration-audit event types, PAM_CONNECTOR/PAM_SERVICE_ACCOUNT actor types and BORDER0_API as an audit origin; Aperture likewise writes APERTURE-related config-change events into the same stream. We have no PAM/Border0/Aperture setup, so work from the OpenAPI spec only: build fixtures with the new enum values, verify the auditlogs collector and audit processor pass unknown event types/actor types/origins through gracefully (no drops, no crashes, sane normalized labels), extend any normalization tables, and consider a PAM slice on the audit dashboard tab once shapes are pinned. Covers candidates A2+A9.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Fixtures exercise PAM_*, BORDER0_API and unknown/future event types through the audit path with correct passthrough behaviour
- [ ] #2 Audit label normalization covers the new actor types and origins
- [ ] #3 Behaviour for entirely unknown future event families is asserted (forward-compatibility test)
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
