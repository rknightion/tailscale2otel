---
id: TSO-0126
title: Alert on sustained processor queue drops
status: Done
assignee: []
created_date: '2026-09-04 06:35'
updated_date: '2026-09-04 08:41'
labels:
  - needs-triage
dependencies: []
priority: medium
type: feature
ordinal: 127000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The health dashboard visualizes tailscale2otel.processor.dropped by signal and reason, but no rule watches confirmed record or span loss after queue saturation. Operators can respond by correcting capacity or deliberate load-shedding policy.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A generated advisory non-paging rule detects a non-zero processor drop rate per signal and reason for 10 minutes
- [x] #2 Executable fixtures cover sustained drops, recovery, and separation by signal and reason
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
Done in 7841543f. ts2o-processor-queue-drops: sum by (signal, reason) (rate(tailscale2otel_processor_dropped_total[10m])) > 0 for 10m, advisory, non-paging, policy optional. Runbook otlp-export-health, because the loss is upstream of the exporter and no export-failure rule can see it. Panel 'Processor queue drops/s by signal & reason'. The fixture drives two series at once and asserts two separate alerts, pinning the per-signal-and-reason split: deliberate load-shedding and a saturated queue need different answers and collapsing them would hide which is happening. Negative-tested individually. Live-verified in the same push as TSO-0124.
<!-- SECTION:NOTES:END -->
