---
id: TSO-0043
title: Grants-aware policy parsing and acls-vs-grants adoption metric
status: To Do
assignee: []
created_date: '2026-08-30 09:10'
updated_date: '2026-08-30 09:47'
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
- [ ] #1 A policy file using only grants reports non-zero rule counts and applicable risk findings
- [ ] #2 An adoption metric distinguishes acls vs grants rule counts
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
