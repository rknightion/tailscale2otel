---
id: TSO-0119
title: >-
  Standby replicas record coordination telemetry that the Prometheus pull path
  never serves
status: Done
assignee:
  - '@codex'
created_date: '2026-09-03 23:02'
updated_date: '2026-09-04 07:10'
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
- [x] #1 Verify first, before changing anything: confirm by observation whether a standby's OTLP push path carries coordination.leader, and record the result on this task either way
- [x] #2 A standby's recorded coordination state is observable on every delivery path the deployment has enabled, or the asymmetry is documented as deliberate with its reason
- [x] #3 The chosen behaviour is decided explicitly against the duplicate-series risk, and the reason is written on the task
- [x] #4 docs/metrics.md no longer describes a state that a supported delivery path cannot show
- [x] #5 A test pins the standby delivery behaviour so a future change to runActive cannot silently re-gate it
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Establish by observation, before source edits, whether a standby ProviderSet exports coordination.leader=0 through OTLP; record the observed result.
2. Add a failing test that pins the intended standby delivery surfaces.
3. Choose and implement the narrowest standby serving behavior that exposes process coordination state without duplicating active collector series, and document the duplicate-series rationale.
4. Regenerate docs/metrics.md and run focused checks; root owns shared downstream generation, integrated review, full gate, commit, push, CI, and finalization.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Pre-change observation (2026-09-04, before any tracked Lane A source edit): an in-repository Go overlay exercised App.New, the real ProviderSet, the real observeCoordination standby path, and an actual loopback OTLP/HTTP receiver. Command: `GOFLAGS='-overlay=/Users/rob/repos/tailscale2otel/codex/tso0119_overlay.json' go test -race -v -run '^TestTSO0119ObservedStandbyOTLPExport$' ./internal/app`. Decisive output: `OTLP receiver observed tailscale2otel.coordination.leader value=0 coordination.mode=kubernetes coordination.state=standby coordination.identity=standby-pod coordination.lease_name=tailscale2otel coordination.namespace=observation`. This proves the standby OTLP push path carries leader=0 and the expected coordination attributes. It does not prove live Kubernetes election or Prometheus serving.

Chosen behavior and duplicate-series decision: keep the Prometheus listener alive for the whole Kubernetes coordination lifecycle, but select its gatherer at scrape time. Standby and stepped-down replicas expose only the process-level provider, including coordination.leader=0; only the current leader exposes the full process-plus-tailnet gatherer. This makes coordination state observable on every enabled delivery path without allowing collector/per-tailnet series retained by a former leader to remain scrapeable after demotion. A scrape already in progress at transition may finish its prior snapshot; subsequent gathers follow the latest observed state.

Integrated as 15e4777d with formatting follow-up 8c7ae613. The cumulative full gate passed at 0e212ab5; the earlier integrated just gen left no diff, and the final formatting check passed. Exact-head CI run 33844779329 attempt 1 succeeded for 630b1d75 before the later fallback fix. CodeRabbit review completed and its findings were resolved.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Observed a real loopback OTLP export of coordination.leader=0 from standby before source edits, then made Prometheus serve process-only coordination metrics on standby and stepped-down replicas while leaders serve the full gatherer. Tests pin both visibility and duplicate-series exclusion; docs and the full gate pass.
<!-- SECTION:FINAL_SUMMARY:END -->
