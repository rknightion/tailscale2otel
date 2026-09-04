---
id: TSO-0130
title: Self-fence promptly when a live coordination Lease is deleted or replaced
status: To Do
assignee: []
created_date: '2026-09-04 07:02'
labels:
  - needs-triage
dependencies: []
priority: high
type: bug
ordinal: 131000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
client-go leader election intentionally provides no fencing. If the live Lease is deleted or replaced beneath a leader, that process can continue active collection until renew_deadline while another replica acquires the recreated Lease, creating a duplicate-active window. Current tests cover caller cancellation and draining but not deletion or conflicting replacement.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A deterministic fake-client test reproduces Lease deletion and conflicting replacement while the active callback is running
- [ ] #2 The old leader cancels active collection promptly when it observes deletion, replacement, or another holder rather than waiting the full renew deadline
- [ ] #3 The chosen fencing boundary is documented against client-go no-fencing semantics and does not mutate the tailnet
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
