---
id: TSO-0115
title: 'Prove coordinated Kubernetes mode on the lab, then revert'
status: To Do
assignee: []
created_date: '2026-09-02 15:48'
labels: []
dependencies:
  - TSO-0110
priority: medium
type: chore
ordinal: 116000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Waves 5 and 6 built Lease-based coordination, a Kubernetes checkpoint backend, sharding, compression and legacy migration, and none of it has ever run outside tests. The lab is configured for checkpoint.store=file, so Wave 6's rollout proved only that the new image starts and stays healthy with the Kubernetes path inactive. Its own report lists legacy-to-shard migration, the migration marker, shard annotations, compressed sizes, legacy reopen and Kubernetes leader state as unproven live.

Owner authorisation 2026-09-02: run a scoped temporary cycle on the lab that enables coordinated mode with its RBAC, gathers the evidence, and reverts to the current configuration. Wave 6 already demonstrated that rollout and rollback of the image are clean.

Live verification is the root agent's work and is never dispatched to a lane. Keep lab names, addresses, identifiers and credentials out of tracked files - record shapes, counts and sizes, not instances.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A coordinated deployment elects a leader through the Lease, and leadership is observed rather than inferred from the absence of errors
- [ ] #2 A pre-existing single-ConfigMap checkpoint is migrated to shards live, and the shard count, compressed sizes and compression ratios are recorded
- [ ] #3 The retained legacy object is confirmed present and reopenable by the previous release, proving the rollback path rather than assuming it
- [ ] #4 The namespace-wide ConfigMap grant is confirmed sufficient and no broader permission was needed at runtime
- [ ] #5 The lab is returned to its prior configuration and image, verified ready, with every temporary object removed and named in the record
- [ ] #6 Anything that could not be proven is recorded as unproven with its reason, never reported as passed
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
