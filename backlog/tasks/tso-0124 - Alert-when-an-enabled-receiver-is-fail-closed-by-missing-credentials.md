---
id: TSO-0124
title: Alert when an enabled receiver is fail-closed by missing credentials
status: To Do
assignee: []
created_date: '2026-09-04 06:35'
labels:
  - needs-triage
dependencies: []
priority: medium
type: feature
ordinal: 125000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The health dashboard visualizes tailscale2otel.receiver.misconfigured, but no rule watches it. A non-zero value means an enabled network receiver is rejecting every input with HTTP 403 until the operator supplies its credential or binds it safely; this is low-noise and directly actionable.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A generated advisory non-paging rule detects receiver.misconfigured above zero per receiver for a sustained 10-minute window
- [ ] #2 The rule remains quiet when the receiver is disabled and has executable Prometheus fixtures covering fire and healthy cases
- [ ] #3 Grafana-managed and Prometheus artifacts plus runbook documentation regenerate, deploy successfully, and verify in sync
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
