---
id: TSO-0049
title: ACL risk findings as structured log records
status: Done
assignee:
  - '@codex'
created_date: '2026-08-30 09:27'
updated_date: '2026-08-30 16:32'
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
- [x] #1 Each risk finding is available in Loki as a structured record with stable attributes
- [x] #2 Emission is change-driven or otherwise bounded, not per-poll repetition
- [x] #3 A dashboard panel lists current findings
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Latitude deviation: the run contract called for one commit per feature, but root retained the already-integrated shared-tree feature commit fa6a465 plus review-fix commit a18a5dd rather than performing prohibited destructive history surgery after integration and push. All task evidence is tied to the verified implementation head a18a5dd06f9ac9c8b84fda73bba653ded2398d5a.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Extended the existing risky-rule event to SSH and auto-approver findings, made emission change-driven, and added a current-findings panel. Verified by change-transition telemetry tests, generated coverage guards, final just check, and exact-head CI run 33322449434 at a18a5dd06f9ac9c8b84fda73bba653ded2398d5a (success).
<!-- SECTION:FINAL_SUMMARY:END -->
