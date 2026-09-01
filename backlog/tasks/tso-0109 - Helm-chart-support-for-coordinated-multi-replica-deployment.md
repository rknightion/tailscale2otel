---
id: TSO-0109
title: Helm chart support for coordinated multi-replica deployment
status: Done
assignee: []
created_date: '2026-09-01 20:02'
updated_date: '2026-09-01 23:37'
labels: []
milestone: m-10
dependencies:
  - TSO-0107
priority: medium
type: feature
ordinal: 110000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Chart-side half of TSO-0033 phase A1. The chart currently hard-refuses replicaCount other than 1 at deploy/helm/tailscale2otel/templates/deployment.yaml:13-15, with a matching schema maximum at values.yaml:87. That guard becomes conditional: replicaCount must be 1 unless coordination.mode is kubernetes, in which case up to 3 is allowed.

The values.yaml schema maximum must move in the SAME commit as the template guard. They are two expressions of one rule and a split lands a chart that validates one way and behaves another.

Also needed: a Role and RoleBinding granting get, create and update on coordination.k8s.io leases, scoped to the lease name where the API allows it; the coordination values block plumbed through to the container's config or TS2OTEL_ env; and the generated chart README and values.schema.json regenerated with the pinned tools.

Regenerate with just gen-helm, which uses helm-docs v1.14.2 and helm-values-schema-json v2.5.0. A locally installed tool of any other version produces different output and lands as a red fail-on-diff. just gen-tools installs the pins.

Do not push anything to a live cluster from this task. Validation here is helm lint, helm template and configcheck against the rendered config, plus kubeconform with real schemas rather than -ignore-missing-schemas. Live rollout is the wave's validation lane, not this one.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 replicaCount above 1 is rejected unless coordination.mode is kubernetes, and permitted up to 3 when it is
- [x] #2 the values.schema.json maximum and the template guard change in one commit and agree
- [x] #3 a Role and RoleBinding grant only the lease permissions the coordination client actually needs
- [x] #4 the coordination values block reaches the container and a rendered multi-replica release passes configcheck
- [x] #5 helm lint, helm template and kubeconform with real schemas all pass on both a single-replica and a coordinated multi-replica values set
- [x] #6 the chart README and values.schema.json are regenerated with the pinned tool versions and leave no diff
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
After C freezes config keys, conditionally permit coordinated replicas, add least-privilege Lease RBAC and config plumbing, own the RollingUpdate switch, regenerate Helm artifacts, and validate single and multi-replica renders.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
The chart keeps singleton/Recreate defaults unchanged, permits two or three replicas only with coordination.mode=kubernetes, switches that mode to RollingUpdate, derives the dedicated checkpoint ConfigMap name, and grants only Lease plus checkpoint ConfigMap get/create/update permissions. Declarative chart work was validated rather than given application TDD.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Implemented coordinated multi-replica chart support in aea526d, integrated at 48bf65c8bf30c0f77f679728b4b56947bd5df944. Generated README/schema artifacts are in sync; single-replica and coordinated renders pass configcheck, Helm lint/template, kubeconform with real schemas, 464 Helm checks, just fmt-check, full just check, and exact-head CI 33569379997.
<!-- SECTION:FINAL_SUMMARY:END -->
