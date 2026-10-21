---
id: TSO-0199
title: >-
  Finish dependency sweep and stable release after external review checks
  recover
status: Done
assignee: []
created_date: '2026-10-21T15:44:00Z'
updated_date: '2026-10-21T23:57:00Z'
labels: []
dependencies: []
references:
  - 'https://github.com/rknightion/tailscale2otel/pull/727'
  - 'https://github.com/rknightion/tailscale2otel/pull/723'
priority: high
type: chore
ordinal: 201000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Go 1.27.3 and patched CA security fix is pushed as 862bd1ef6f563e248416d9c8e1d7e9dfaec46e5f, with main CI green and a verified patched main image. The 2026-10-21 sweep stopped at its 30-minute PR wait: Renovate PR 727 has green required aggregates but a stale nonrequired metricscatalog lint check, while release PR 723 has green required aggregates but Cursor Bugbot fails because its daily bot-review quota is exhausted. No red or pending check was bypassed. Re-query every head, base and check before resuming; do not trust this historical status.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Every remaining Renovate PR is updated to current main and merged only after its exact head meets the authorized check policy, or has a recorded decision
- [x] #2 The release PR is refreshed and merged under the authorized check policy after main CI is green
- [x] #3 Published stable release binaries report go1.27.3 and the released image carries ca-certificates 20250419+deb13u1 or later
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner removed Bugbot. Refreshed release head f8bfd66d19ed3bc215f2075747b70580e42b3f9a was current with main and every check completed before merge 4e63b92f1fb9899c9251cc0ebc3b260a1205e72d. No check bypass or policy exception. Stable v5.2.1 publication 39695504658 and exact release commit CI succeeded. Official Linux and Darwin arm64 archive checksums match and both binaries report go1.27.3. Both released container architectures report go1.27.3 and ca-certificates 20250419+deb13u1; extracted-bundle probes reject both incident chains. Runtime curl/wget absent, so no in-image requests claimed. The prior full local remote gate passed, current exact-head CI covers all modules, and just --fmt --check passes. Generated-input DoD is not applicable: no generator inputs changed in this refresh.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Completed the dependency and stable-release follow-up after Bugbot removal. All repository PRs are closed, release 5.2.1 is fully published, official archives and both container architectures have verified Go 1.27.3 and patched trust evidence. Publication includes signing, SBOM and Helm. No outstanding blocker.
<!-- SECTION:FINAL_SUMMARY:END -->
