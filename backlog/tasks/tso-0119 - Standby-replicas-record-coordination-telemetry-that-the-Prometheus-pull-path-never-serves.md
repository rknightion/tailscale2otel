---
id: TSO-0119
title: >-
  Standby replicas record coordination telemetry that the Prometheus pull path
  never serves
status: To Do
assignee: []
created_date: '2026-09-03 23:02'
labels:
  - needs-triage
dependencies: []
priority: high
type: bug
ordinal: 120000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A standby replica in coordination.mode=kubernetes records its own coordination state but only one of the two delivery paths carries it, so the same metric is visible to OTLP users and invisible to Prometheus users.

What the code does at a1d49b06. internal/app/coordination.go:49 observeCoordination records tailscale2otel.coordination.leader with value 0 and coordination.state on the standby, through a.procEmitter, from the moment leadership is contested. internal/app/app.go builds the telemetry ProviderSet at line 272 and the Prometheus listener at line 489, both during App.New. But App.Run routes kubernetes mode to runCoordinated, which starts ONLY the admin server, and internal/app/app.go:788 runActive is what starts the Prometheus listener. runActive does not run on a standby.

The consequence, believed and to be verified before any change: the OTLP periodic reader is running from construction, so a standby DOES push coordination.leader=0, while its /metrics endpoint is never served. An operator on the Prometheus pull path therefore cannot distinguish a standby from a dead pod, cannot count replicas, and cannot see that a standby is ready to take over. Wave 9's live cycle hit exactly this: the standby status endpoint proved standby, but no standby leader metric could be observed.

docs/metrics.md documents the metric as '1 while this pod holds the Lease, otherwise 0'. On the pull path the 0 case is unobservable, so the documentation describes a state the delivery path cannot show.

The judgment this needs. Leader-only may have been deliberate, to stop two pods serving overlapping series. Weigh that against coordination.identity and coordination.state already distinguishing them, and against a standby having no collector metrics to duplicate in the first place. Decide which process-level surfaces a standby should serve, and record the reason.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Verify first, before changing anything: confirm by observation whether a standby's OTLP push path carries coordination.leader, and record the result on this task either way
- [ ] #2 A standby's recorded coordination state is observable on every delivery path the deployment has enabled, or the asymmetry is documented as deliberate with its reason
- [ ] #3 The chosen behaviour is decided explicitly against the duplicate-series risk, and the reason is written on the task
- [ ] #4 docs/metrics.md no longer describes a state that a supported delivery path cannot show
- [ ] #5 A test pins the standby delivery behaviour so a future change to runActive cannot silently re-gate it
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
