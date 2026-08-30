---
id: TSO-0049
title: ACL risk findings as structured log records
status: To Do
assignee: []
created_date: '2026-08-30 09:27'
updated_date: '2026-08-30 09:47'
labels: []
milestone: m-2
dependencies: []
priority: low
ordinal: 52000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
internal/collector/acl/risk.go already computes policy risk findings but only counts reach telemetry. Emit each finding as a structured log record (rule ref, risk class, detail) so the dashboard can list current policy risks with history rather than just a count. Align attribute naming with the audit/log conventions; consider emission on change only (findings are stable between revisions) to avoid per-poll log spam.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Each risk finding is available in Loki as a structured record with stable attributes
- [ ] #2 Emission is change-driven or otherwise bounded, not per-poll repetition
- [ ] #3 A dashboard panel lists current findings
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
