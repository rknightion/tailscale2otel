---
id: TSO-0109
title: Helm chart support for coordinated multi-replica deployment
status: To Do
assignee: []
created_date: '2026-09-01 20:02'
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
- [ ] #1 replicaCount above 1 is rejected unless coordination.mode is kubernetes, and permitted up to 3 when it is
- [ ] #2 the values.schema.json maximum and the template guard change in one commit and agree
- [ ] #3 a Role and RoleBinding grant only the lease permissions the coordination client actually needs
- [ ] #4 the coordination values block reaches the container and a rendered multi-replica release passes configcheck
- [ ] #5 helm lint, helm template and kubeconform with real schemas all pass on both a single-replica and a coordinated multi-replica values set
- [ ] #6 the chart README and values.schema.json are regenerated with the pinned tool versions and leave no diff
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
