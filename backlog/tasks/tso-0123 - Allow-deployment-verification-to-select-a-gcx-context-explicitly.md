---
id: TSO-0123
title: Allow deployment verification to select a gcx context explicitly
status: Done
assignee: []
created_date: '2026-09-04 06:12'
updated_date: '2026-09-04 07:10'
labels:
  - needs-triage
dependencies: []
modified_files:
  - scripts/verify_deployment.py
  - scripts/test_verify_deployment.py
type: bug
ordinal: 124000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Wave 10 pushed alert resources successfully with an explicit m7kni gcx context, but the required repository verifier selected the unrelated default context and failed authentication. The verifier has no repository-scoped way to honor the explicit-context safety contract without mutating the user-level gcx configuration.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Deployment verification can select a named gcx context without changing the user-level current context
- [x] #2 Every gcx configuration check and resource pull uses the selected context, while omission retains the existing current-context behavior
- [x] #3 Automated tests cover both explicit-context and default-context command construction
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Add failing unit coverage for explicit and default gcx context selection. 2. Thread a repository-scoped context input through the verifier and every gcx subprocess. 3. Run the focused verifier suite, then use the corrected just recipe path for live read-back.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Red proof: `just test-python` failed only the two new verifier tests. `check_gcx("m7kni")` and `pull_deployed(workdir, "m7kni")` both raised TypeError because the production functions accepted no explicit context.

Green proof: `just test-python` passed 133 generator tests and 48 script tests; `just --fmt --check` passed. Live proof: `just verify-deploy m7kni` used the explicit context and found 128 shipped and 128 deployed rules, 81 paused on each side, with zero missing, orphaned, or drifted.

Integrated as 630b1d75. The cumulative full gate passed at 0e212ab5; no generated input changed and formatting passed. Exact-head CI run 33844779329 attempt 1 succeeded for 630b1d75. The corrected live command just verify-deploy m7kni read 128 shipped and 128 deployed rules, 81 paused on both sides, and zero missing, orphaned, or drifted.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added explicit gcx context selection to deployment verification without mutating the user-level current context. Every configuration check and resource pull honors the selected context, default behavior remains intact, automated command-construction tests pass, and the m7kni live read-back was fully in sync.
<!-- SECTION:FINAL_SUMMARY:END -->
