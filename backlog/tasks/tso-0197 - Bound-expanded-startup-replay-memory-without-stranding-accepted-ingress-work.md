---
id: TSO-0197
title: Bound expanded startup replay memory without stranding accepted ingress work
status: To Do
assignee: []
created_date: 2026-10-13 09:57
updated_date: '2026-11-01T20:55:00Z'
labels:
  - needs-triage
dependencies: []
references:
  - internal/app/ingresswal.go
  - internal/telemetry/ingress_delivery.go
  - internal/telemetry/metric_program.go
  - internal/stream/stream.go
priority: high
type: bug
ordinal: 197000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Follow-up to TSO-0192 (diagnose OOM restarts and propose a fix). Independent read-only process/workload diagnosis identifies retained startup-replay metric-operation tapes and cloned logs as the dominant observed heap-pressure mechanism. The existing disk-encoded WAL byte window and receipt-count ceiling do not reserve aggregate expanded process memory. Restarting or reducing disk capacity alone can leave already-accepted work unable to drain. Diagnosis is complete, but no remediation or numerical safe budget was implemented or proven. This source fix needs separately commissioned guarded work and protected offline workload/cgroup evidence; no induced live traffic, forced promotion, WAL deletion or undocumented telemetry suppression is authorized by the diagnosis.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Expanded replay and preparation memory remain bounded under a declared measured process budget across multiple accepted originals, including decoding and first-cover materialization; budget is distinct from durable WAL disk capacity.
- [ ] #2 An individually oversized already-accepted original has a bounded progress path rather than permanent starvation, post-effect rejection, implicit data loss or whole-original allocation before staging.
- [ ] #3 Preserve durable ACK, original route/timestamp/generation, one process-local effect entry, frozen metric/log retry payloads and both signal outcomes before generation-checked retirement; demonstrate crash/retry/cancellation/permanent-rejection behavior.
- [ ] #4 Protected offline workload and cgroup proof covers observed backlog/original-shape distribution and representative ordinary process overhead, including negative pre-fix reproduction; report peak headroom, residual uncertainty and any migration/security requirements without making an unmeasured live sizing claim.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner approved design first on 2026-10-13. TSO-0197.01 (freeze the bounded replay-memory design and implementation proof packet) is the separately commissioned prerequisite. Implementation and all four parent acceptance criteria remain open; no new production capture or cleanup is commissioned. Retained evidence cannot establish a safe measured process budget. The design must identify missing workload inputs and preserve future implementation attempt lineage.

loop32 completed TSO-0197.01 (freeze bounded replay-memory design and proof packets) under the approved design-only exception. Canonical independent PASS: /Users/rob/repos/tailscale2otel/codex/loop32/design-review.md. Two design corrections, zero implementation attempts. All four parent criteria remain unchecked, no remediation or numerical safe cap proven. Resume only after protected original-shape/representative ordinary-envelope prerequisites and separately commissioned implementation; retain one serial owner across frozen storage/preparation/delivery seams. TSO-0197.02 (preserve required delta first-cover effects across failed scheduled collection) records a source-grounded inferred failure-path finding; negative reproduction and repair remain uncommissioned.

2026-11-01 owner commissioned implementation for loop33 (both TSO-0197 and TSO-0197.02), plus a read-only read of ingress WAL entry-size and shape statistics from a lab pod through the tailnet-proxied context, no writes, to supply the missing W1 workload input. One serial senior owner across the frozen storage/preparation/delivery seams, per the loop32 design.
<!-- SECTION:NOTES:END -->
