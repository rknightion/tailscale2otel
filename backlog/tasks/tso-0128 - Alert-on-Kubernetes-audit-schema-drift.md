---
id: TSO-0128
title: Alert on Kubernetes audit schema drift
status: Done
assignee: []
created_date: '2026-09-04 06:35'
updated_date: '2026-09-04 08:41'
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
- [x] #1 A generated advisory non-paging rule detects a non-zero schema-drift rate per field for 15 minutes and ships paused by default
- [x] #2 Executable fixtures cover sustained drift and healthy input, and a Kubernetes-audit runbook section explains parser refresh and expected upgrade review
- [x] #3 Grafana-managed and Prometheus artifacts regenerate, deploy successfully, and verify in sync
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Done in 7841543f. ts2o-k8s-audit-schema-drift: sum by (field) (rate(tailscale_k8s_schema_drift_total{status="unknown"}[10m])) > 0 for 15m, advisory, non-paging, policy optional, and paused=True per AC 1. Mirrors the existing ts2o-audit-schema-drift, which watches the configuration audit stream; this one watches the Kubernetes audit stream. Kept as two rules rather than one over both signals because a single rule would lose the field breakdown that says which parser to refresh.

New runbook section kubernetes-audit-schema-drift covers parser refresh through re-vendoring spec/tailscale-api.json, and states why it ships paused: nobody has watched it across a Tailscale upgrade, and a control-plane release that adds one enum value lights it up for every deployment at once, which is how a rule teaches people to ignore it.

The fixture asserts status="known" at a high rate does NOT fire - classified vocabulary arriving fast is a busy cluster, not drift, and a rule without the status filter would read them the same. Negative-tested individually. Live-verified in the same push as TSO-0124; it is the 82nd paused rule of 133.
<!-- SECTION:NOTES:END -->
