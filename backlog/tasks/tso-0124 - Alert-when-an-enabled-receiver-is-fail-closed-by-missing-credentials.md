---
id: TSO-0124
title: Alert when an enabled receiver is fail-closed by missing credentials
status: Done
assignee: []
created_date: '2026-09-04 06:35'
updated_date: '2026-09-04 08:41'
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
- [x] #1 A generated advisory non-paging rule detects receiver.misconfigured above zero per receiver for a sustained 10-minute window
- [x] #2 The rule remains quiet when the receiver is disabled and has executable Prometheus fixtures covering fire and healthy cases
- [x] #3 Grafana-managed and Prometheus artifacts plus runbook documentation regenerate, deploy successfully, and verify in sync
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Done in 7841543f. ts2o-receiver-fail-closed: max by (receiver) (tailscale2otel_receiver_misconfigured_ratio) > 0 for 10m, severity advisory, no page label, policy optional so a disabled receiver's absence stays Ok while a datasource error surfaces as Error. Runbook ingest-receivers, panel 'Fail-closed receiver misconfiguration'. The 10m window keeps a rolling restart quiet. Fixtures cover a receiver reporting 0 (healthy, not absent, since the point of the signal is that the exporter is healthy while one route accepts nothing) and a sustained 1. Negative-tested on its own: threshold moved to 999, the fixture failed, reverted. Pushed live with gcx --context m7kni; verify-deploy m7kni reports 133 shipped, 133 deployed, 0 missing, 0 orphaned, 0 drifted.
<!-- SECTION:NOTES:END -->
