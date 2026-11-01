---
id: TSO-0206
title: Export the Go memory limit and alert when heap nears it
status: To Do
assignee: []
created_date: '2026-11-01T20:55:00Z'
labels:
  - alerts
  - dashboards
dependencies: []
references:
  - deploy/helm/tailscale2otel/values.yaml
priority: medium
type: feature
ordinal: 208000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
OOM restarts happened (TSO-0192, TSO-0197). The chart sets GOMEMLIMIT but no metric reports the effective limit and no runtime.memory family has an alert. Read the effective limit with debug.SetMemoryLimit(-1).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A self-obs gauge reports the effective Go memory limit (absent or a stated sentinel when unlimited), in the catalog with a disposition
- [ ] #2 A dashboard panel on Exporter Health shows heap against the limit, and a shipped alert fires when heap stays above a stated fraction of a set limit, both generated via just gen
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
