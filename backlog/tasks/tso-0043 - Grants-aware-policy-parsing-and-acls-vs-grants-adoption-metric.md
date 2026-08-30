---
id: TSO-0043
title: Grants-aware policy parsing and acls-vs-grants adoption metric
status: Done
assignee:
  - '@codex'
created_date: '2026-08-30 09:10'
updated_date: '2026-08-30 16:32'
labels: []
milestone: m-3
dependencies: []
priority: medium
ordinal: 46000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Grants blocks coexist with acls in the policy file (grants GA; next-gen syntax). If the acl collector rule-count/risk analysis (internal/collector/acl/) only reads the acls key, grants-based tailnets under-report policy size and risk. Add grants parsing to the rule counts and risk heuristics where they translate, plus a grants-vs-acls adoption metric (counts per syntax family). Verify current behaviour against a grants-bearing policy fixture first.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A policy file using only grants reports non-zero rule counts and applicable risk findings
- [x] #2 An adoption metric distinguishes acls vs grants rule counts
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
Made policy parsing grants-aware and exposed the ACL-versus-grants adoption metric with dashboard coverage. Verified through collector telemetry tests, generated-surface guards, final just check, and exact-head CI run 33322449434 at a18a5dd06f9ac9c8b84fda73bba653ded2398d5a (success).
<!-- SECTION:FINAL_SUMMARY:END -->
