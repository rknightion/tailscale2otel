---
id: TSO-0039
title: 'Posture attribute values: compliance gauges with cardinality caps'
status: To Do
assignee: []
created_date: '2026-08-30 09:10'
labels: []
dependencies: []
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
