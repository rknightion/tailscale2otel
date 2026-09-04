---
id: TSO-0126
title: Alert on sustained processor queue drops
status: To Do
assignee: []
created_date: '2026-09-04 06:35'
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
- [ ] #1 A generated advisory non-paging rule detects a non-zero processor drop rate per signal and reason for 10 minutes
- [ ] #2 Executable fixtures cover sustained drops, recovery, and separation by signal and reason
- [ ] #3 Grafana-managed and Prometheus artifacts plus runbook documentation regenerate, deploy successfully, and verify in sync
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
