---
id: TSO-0122
title: Keep alert policy counts and recommended-profile prose truthful
status: In Progress
assignee:
  - '@codex'
created_date: '2026-09-04 05:59'
updated_date: '2026-09-04 06:25'
labels:
  - needs-triage
dependencies: []
modified_files:
  - deploy/alerts/gen/build_rules.py
  - deploy/alerts/gen/test_rules.py
  - deploy/alerts/README.md
  - docs/alert-profiles.md
priority: low
type: bug
ordinal: 123000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
CodeRabbit review during Wave 10 found two related documentation claims that drift when the generated alert catalogue changes. The policy table still reported 67 optional rules although the generator declared 72, making its rows sum to 100 while 105 alert manifests existed. The recommended-profile prose also said the shipped starter set was unchanged and byte-identical to what had always shipped, which becomes false whenever a new enabled rule is added even though the intended contract is only that the profile preserves each rule's authored paused state.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The documented policy counts are derived or tested against the generator and sum to the shipped alert count
- [ ] #2 Recommended-profile documentation states the actual preservation contract without claiming the catalogue never changes
- [ ] #3 Generator tests and the alert documentation drift checks pass
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Add failing generator tests that compare the documented policy table with POLICY_BY_UID and reject historical-immutability wording for the recommended profile.
2. Update the generator-owned profile rationale and decision text to state that recommended preserves authored paused values and equals the default output for the current catalogue.
3. Correct the optional-policy count in the alert README and regenerate docs/alert-profiles.md.
4. Run focused generator tests and alert generation checks; fold this small covered repair into the Wave 10 alert commit and finalize it separately.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Negative tests first proved both defects: the policy-count test reported optional 72 versus documented 67, and the profile-contract test found no `authored` wording plus the stale `always shipped` claim. Fixed the generator-owned rationale/decision/help text, corrected the optional policy count to 72, regenerated docs/alert-profiles.md, and added durable tests. Targeted red-to-green passed; the full generator suite now reports 132 tests passed, and `just rules-check` validates 127 rules plus all fixtures.

`just rules-check` covers 127 Prometheus-compatible rules. The 128th catalogue resource is the Loki-backed `AuditConfigChangeWARN`, which is intentionally absent because promtool cannot parse LogQL; the real Grafana push and `just verify-deploy m7kni` cover all 128.
<!-- SECTION:NOTES:END -->
