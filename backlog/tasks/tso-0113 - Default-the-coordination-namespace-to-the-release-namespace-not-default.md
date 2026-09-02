---
id: TSO-0113
title: 'Default the coordination namespace to the release namespace, not default'
status: To Do
assignee: []
created_date: '2026-09-02 15:48'
labels: []
dependencies:
  - TSO-0107
priority: medium
type: bug
ordinal: 114000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The chart sets config.coordination.namespace to 'default'. When coordination.mode is kubernetes the rendered Role grants get, list, update and create on every ConfigMap in that namespace - RBAC has no prefix matching for resourceNames and list is never name-filtered, so the shard store cannot be name-scoped as it currently discovers its shards. Those two defaults compose badly: an operator who enables coordination and changes nothing else gives the exporter read and write access to every ConfigMap in the cluster's default namespace, which holds unrelated objects.

Owner decision 2026-09-02: the verbs stand as the narrowest portable grant; the namespace default is what changes. Default it to the release namespace so the blast radius is the chart's own, keeping the value overridable for a deployment whose Lease genuinely lives elsewhere.

The Lease namespace and the checkpoint ConfigMap namespace are the same value today. Confirm that a single key is still the right shape once the default stops being a fixed string, rather than assuming it.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A chart installed with coordination.mode=kubernetes and no namespace override places its Lease and checkpoint ConfigMaps in the release namespace
- [ ] #2 The rendered Role and RoleBinding target that same namespace, and a rendered-manifest assertion pins it
- [ ] #3 An explicit coordination.namespace override still works and is still validated as a DNS-1123 label
- [ ] #4 The values documentation and generated chart README state that the grant is namespace-wide for ConfigMaps and why, so the choice is visible to an operator overriding it
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
