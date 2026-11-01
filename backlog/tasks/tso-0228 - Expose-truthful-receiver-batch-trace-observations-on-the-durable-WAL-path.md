---
id: TSO-0228
title: Expose truthful receiver batch trace observations on the durable WAL path
status: To Do
assignee: []
created_date: '2026-11-01T22:55:00Z'
labels:
  - follow-up
  - dashboards
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/internal/stream/stream.go
priority: medium
type: bug
ordinal: 230000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The deployed successful durable-append branch returns before the stream receive span adds flow-count and decompressed-body-size attributes. With ingress WAL enabled, receive spans exist but both queried batch-size attributes are absent, so the shipped trace panel has no observations. Filed by TSO-0172 (comprehensive live dashboard audit); outside the approved loop33 implementation set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Successful WAL receive spans expose bounded, semantically correct admission counts and body-size observations used by the panel.
- [ ] #2 Instrumentation does not imply processing occurred before durable ACK and does not reapply effects or allocate another whole expanded body to obtain counts.
- [ ] #3 Tests drive the actual durable receive branch and verify emitted attributes; a source or rendered panel fixture proves the observations are queryable.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
