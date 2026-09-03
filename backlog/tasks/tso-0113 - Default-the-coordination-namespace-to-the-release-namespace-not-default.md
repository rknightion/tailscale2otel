---
id: TSO-0113
title: 'Default the coordination namespace to the release namespace, not default'
status: Done
assignee:
  - '@codex'
created_date: '2026-09-02 15:48'
updated_date: '2026-09-03 13:57'
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
- [x] #1 A chart installed with coordination.mode=kubernetes and no namespace override places its Lease and checkpoint ConfigMaps in the release namespace
- [x] #2 The rendered Role and RoleBinding target that same namespace, and a rendered-manifest assertion pins it
- [x] #3 An explicit coordination.namespace override still works and is still validated as a DNS-1123 label
- [x] #4 The values documentation and generated chart README state that the grant is namespace-wide for ConfigMaps and why, so the choice is visible to an operator overriding it
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 8 Lane A: change the Helm coordination namespace default to the release namespace while preserving one key for Lease and checkpoint objects; pin default and override rendering, DNS-1123 validation, namespace-scoped RBAC, and operator-visible grant documentation; regenerate the chart README and values schema with the pinned tools; return focused render/check evidence without committing or pushing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Wave 8 Lane A implemented the single effective namespace seam in local commit ff444ba: an empty chart value renders the release namespace into config.yaml, Role and RoleBinding; explicit DNS-1123 overrides remain supported; the namespace-wide ConfigMap grant rationale is operator-visible. just helm-lint passed 473/473 render cases, just gen-helm was idempotent, and just --fmt --check passed. Validation replaced a unit test for this declarative template change. CodeRabbit was skipped because this commit is Helm/declarative config plus generated documentation, with no branching application logic.

Final integration at 1c088cea1dbdd9fbcd0d59086953bada2a9ff69f: just check passed; just gen left no diff; just --fmt --check passed; exact-head CI 33762639276 succeeded on attempt 1.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Commit ff444ba defaults the effective coordination namespace to the Helm release namespace across config, Lease/checkpoint access, Role, and RoleBinding while preserving a validated explicit override. The 473-case Helm render gate, idempotent generated artifacts, the integrated local gate, and exact-head CI all passed.
<!-- SECTION:FINAL_SUMMARY:END -->
