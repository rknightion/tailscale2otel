---
id: TSO-0111
title: Reject a Kubernetes checkpoint configuration that cannot fit its ConfigMap
status: To Do
assignee: []
created_date: '2026-09-02 05:17'
updated_date: '2026-09-02 05:17'
labels: []
dependencies:
  - TSO-0108
priority: high
type: bug
ordinal: 112000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Kubernetes checkpoint backend (TSO-0108) serialises the whole shared checkpoint map into one ConfigMap data entry and correctly refuses to truncate when the result exceeds the 1,048,576-byte limit. The failure is visible but it arrives at runtime, after a coordinated deployment is already live and after the cursors it was supposed to protect have stopped persisting.

The overflow is reachable at stock defaults, not only at a configured worst case. Measured 2026-09-02 against the real key shape (scheduler namespace, then the 'seen/' prefix, then base64url of a recorder-layout object key, mapped to an RFC3339 timestamp) with objectstore.max_seen_keys at its default of 5000:

| configuration | keys | serialised bytes | over 1 MiB |
| --- | --- | --- | --- |
| 1 object-store feed, 1 tailnet | 5,000 | 785,001 | no - 75% of the budget |
| 3 feeds (flowlogs, auditlogs, k8s_audit), 1 tailnet | 15,000 | 2,355,001 | yes, 2.2x |
| 3 feeds, 3 tailnets | 45,000 | 7,065,001 | yes, 6.7x |

A single object-store feed at defaults already consumes three quarters of the budget, so a second feed breaks persistence with no configuration change by the operator.

Convert this from a runtime failure into a startup one: project the worst-case serialised size from configuration alone - the enabled object-store destinations, their max_seen_keys, the configured tailnet count and the resulting key namespaces - and reject the configuration in Validate() with the arithmetic in the message, naming the knob to lower. This is the interim guard; TSO-0110 removes the ceiling itself.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Config validation rejects a coordinated Kubernetes-checkpoint configuration whose projected worst-case checkpoint payload exceeds the ConfigMap data limit, before any collector starts
- [ ] #2 The rejection message states the projected size, the limit, and which configuration key to lower
- [ ] #3 The projection accounts for enabled object-store destinations, their configured max_seen_keys, and the per-tailnet key namespacing that multi-tailnet runtimes add
- [ ] #4 The projection is derived from the same key-construction code paths the store actually writes, so it cannot drift from them silently
- [ ] #5 Tests pin the three measured configurations above and a passing single-feed default, driving the real key construction rather than a hand-copied byte estimate
- [ ] #6 The guard applies only to the Kubernetes checkpoint backend; file and memory backends are unaffected
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
