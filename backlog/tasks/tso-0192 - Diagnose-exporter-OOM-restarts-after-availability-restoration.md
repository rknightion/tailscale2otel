---
id: TSO-0192
title: Diagnose exporter OOM restarts after availability restoration
status: Done
assignee:
  - '@loop31'
created_date: '2026-10-12 19:34'
updated_date: '2026-10-13 09:17'
labels:
  - needs-triage
dependencies: []
priority: medium
type: bug
ordinal: 192000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Read-only post-restoration verification found one currently Ready exporter replica with historical OOMKilled termination and nonzero restart count, while its peer remained restart-free. The original strict-config mismatch is resolved; this is a separate reliability concern. Deployment identities and raw evidence remain local and ignored. No remediation was attempted because the proof run prohibited cluster writes.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Identify the memory-pressure cause from bounded process and workload evidence without conflating it with the resolved configuration mismatch.
- [x] #2 Propose or implement an authorized fix preserving ingress durability, with a workload proof and explicit residual risks.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
loop31: implement only the frozen owner-approved seam in isolated ownership; retain exact candidate and behavior evidence, named gate where applicable; independent guarded review then root integration/CI/delivery. Read-only diagnosis has no remediation or code gate.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop31: 0 implementation attempts, read-only diagnosis/proposal accepted after independent causal/security review. Existing individual pre-termination and crash-cohort profiles corroborate startup replay amplification into retained metric tapes and cloned logs; real observed backlog/workload supports separate replay-memory credits, bounded groups and compact tapes. Preserve accepted durability and progress for oversized originals; no remediation implemented. Scheduled collection can seal groups during an application pass and delivered log chunks may release independently. Final cgroup accounting, envelope shape distribution and candidate sizing/effectiveness remain separately scoped follow-up. Code/composed/CI gates not applicable under read-only goal exception; no implementation land.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Diagnosed dominant replay-memory pressure using exact running identity, bounded workload evidence and individual heap-stack attribution; independently reviewed proposal only. No cluster/configuration/source change and no fixed-OOM claim. Residual sizing and oversized-workload proof needs recorded.
<!-- SECTION:FINAL_SUMMARY:END -->
