---
id: TSO-0192
title: Diagnose exporter OOM restarts after availability restoration
status: In Progress
assignee:
  - '@loop31'
created_date: '2026-10-12 19:34'
updated_date: '2026-10-13 08:54'
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
- [ ] #1 Identify the memory-pressure cause from bounded process and workload evidence without conflating it with the resolved configuration mismatch.
- [ ] #2 Propose or implement an authorized fix preserving ingress durability, with a workload proof and explicit residual risks.
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
