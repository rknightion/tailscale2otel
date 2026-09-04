---
id: TSO-0128
title: Alert on Kubernetes audit schema drift
status: To Do
assignee: []
created_date: '2026-09-04 06:35'
labels:
  - needs-triage
dependencies: []
priority: medium
type: feature
ordinal: 129000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The health dashboard visualizes tailscale.k8s.schema_drift by field, but no rule watches parser or classifier drift that can silently reduce Kubernetes audit meaning after an upstream schema change. The first shipped rule should remain paused while operators establish upgrade-time behavior.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A generated advisory non-paging rule detects a non-zero schema-drift rate per field for 15 minutes and ships paused by default
- [ ] #2 Executable fixtures cover sustained drift and healthy input, and a Kubernetes-audit runbook section explains parser refresh and expected upgrade review
- [ ] #3 Grafana-managed and Prometheus artifacts regenerate, deploy successfully, and verify in sync
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
