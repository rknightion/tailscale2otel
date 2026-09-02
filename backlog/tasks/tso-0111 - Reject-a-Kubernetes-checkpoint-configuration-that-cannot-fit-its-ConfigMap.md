---
id: TSO-0111
title: Reject a Kubernetes checkpoint configuration that cannot fit its ConfigMap
status: Done
assignee:
  - '@codex'
created_date: '2026-09-02 05:17'
updated_date: '2026-09-02 09:21'
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
- [x] #1 Config validation rejects a coordinated Kubernetes-checkpoint configuration whose projected worst-case checkpoint payload exceeds the ConfigMap data limit, before any collector starts
- [x] #2 The rejection message states the projected size, the limit, and which configuration key to lower
- [x] #3 The projection accounts for enabled object-store destinations, their configured max_seen_keys, and the per-tailnet key namespacing that multi-tailnet runtimes add
- [x] #4 The projection is derived from the same key-construction code paths the store actually writes, so it cannot drift from them silently
- [x] #5 Tests pin the three measured configurations above and a passing single-feed default, driving the real key construction rather than a hand-copied byte estimate
- [x] #6 The guard applies only to the Kubernetes checkpoint backend; file and memory backends are unaffected
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 6 execution plan (32e0fa2 preflight):
1. Lane A inventories every checkpoint writer, exact key shape, growth bound, ConfigMap name and RBAC reference.
2. Lane B adds a test-first startup projection guard against the frozen ShardKey/per-shard-limit seam, negative-tests every guard, reproduces the three measured configurations from real key construction, and leaves storage unchanged.
3. Root integrates owned schema/Helm/generated surfaces, runs CodeRabbit and the full gate, build-checks the feature commit, then commits and pushes TSO-0111 before TSO-0110 starts.
4. Lane C implements test-first per-namespace gzip binaryData shards, migration, conflict/oversize semantics, observability and narrow RBAC; lane D then performs the adversarial durability review.
5. Root fixes any findings with a fresh review, runs the final gate/generation checks, commits and pushes, verifies exact-head CI, rolls the lab through the published rc and back, then finalizes both tasks in one edit each.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Frozen implementation seam declared before Lane B: KubernetesCheckpointDataLimit remains 1<<20 and is interpreted per shard; ShardKey(key string) is shared by projection and storage; Lane B ships a single constant shard and a projection parameterized by shard function plus limit; Lane C replaces ShardKey with collector-namespace derivation and uses one shared Lease-name/shard-to-ConfigMap-name function.

Lane A completed the checkpoint consumer/key inventory with no unclassified production writer. Bounded keys: scheduler window cursors, flow replay seen-set, object-store cursor/scan/seen state, ACL revision/current audit timestamp, and migration rows. Traffic/time-bounded without a hard cardinality cap: unresolved object-store gap rows and annotation dedupe. The configuration-only startup guard therefore sizes enabled object-store seen rows as specified; unprojectable runtime growth remains protected by the visible no-truncation oversize path and is an explicit Lane C review input. ConfigMap/RBAC source currently names one `<lease>-checkpoints` object. Starting-head CI run 33594447451 failed only in the unrelated timing-sensitive services cancellation test; build, vet, all other packages/modules and heavy lanes passed.

Lane B implementation evidence: the real-key projection measured 885,001 bytes for 1 feed / 1 tailnet / 5,000 keys, 2,685,001 bytes for 3 feeds / 1 tailnet / 15,000 keys, and 8,400,001 bytes for 3 feeds / 3 tailnets / 45,000 keys. The commissioned table claimed 785,001 / 2,355,001 / 7,065,001 bytes. The reproduction wins per the run contract: the real CheckpointScope namespace includes the base64url tailnet, provider, signal and 24-hex feed digest, so row widths vary and are larger than the table assumed. The single-feed default remains below 1,048,576 bytes; the two larger cases exceed it.

Negative-test evidence: all new guard seams were broken one at a time and restored. Tests went red when ShardKey stopped returning one shard; Namespaced was omitted; the supplied shard function was ignored; projected feeds were suppressed; the Kubernetes-only backend condition was removed; disabled feeds were counted; and the passing single-feed limit was temporarily lowered to 800,000 bytes. The restored focused selector just test Test(ShardKeyInitiallyUsesOneShard|ProjectCheckpointSize|Validate.*CheckpointProjection|ValidateCheckpointProjection) passed across the root module.

CodeRabbit major finding fixed test-first: the projection timestamp now has nine non-zero fractional digits so time.Time.MarshalJSON uses maximum-width RFC3339Nano. The final real-path worst-case measurements are 935,001 bytes for 5,000 rows, 2,835,001 bytes for 15,000 rows, and 8,850,001 bytes for 45,000 rows; these supersede the earlier zero-fraction reproduction while the commissioned 785,001 / 2,355,001 / 7,065,001 claims remain recorded above for comparison. The updated measured test failed at the old 885,001 / 2,685,001 / 8,400,001 output before the production fixture changed, then the full focused selector passed.

Integration evidence before commit: CodeRabbit first pass reported one valid major timestamp-width finding and one intentionally left minor tracker-table suggestion; the valid finding was fixed and the second CodeRabbit pass returned zero findings. just check passed after tools/configcheck/go.mod was tidied for the config package new production dependency. just gen regenerated all eleven artifact families and left no unstaged diff; just build passed against the exact staged tree.

Final integrated verification: just check passed at 976cc00c1f004922d1cec5936987abfa01f6f67b, including root and all tool modules, vulnerability scans, generated-artifact checks and 466 passing Helm assertions. just gen left no diff, just --fmt --check passed through the gate, and exact-head CI run 33611867414 completed success with all 26 jobs successful. Auto-RC run 33612619093 cut v4.1.0-rc.57 at the same SHA and published immutable image digest sha256:700279e397ba94edeb3eec1bb16242522c15fd554030e98cd2a01e3ddcd1e3b4. The image-only lab cycle reached 1/1 ready with health and readiness HTTP 200, then restored the prior immutable digest. Live Kubernetes-checkpoint behavior was not exercised: preflight found the deployment configured for checkpoint.store=file with zero legacy or shard ConfigMaps, and the authorised boundary permitted only an image rollout and rollback.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Implemented the pre-start Kubernetes checkpoint capacity guard in b0eb87ab89d8d97fb2c3f2ff84736b9ca3990227 and carried its shared ShardKey seam through final integrated SHA 976cc00c1f004922d1cec5936987abfa01f6f67b. Real production-key measurements and deliberate red/restore checks prove the three configurations, backend scoping, shared key construction and actionable arithmetic; just check, generation/fmt checks and exact-head CI 33611867414 passed. The exact RC image was healthy in the bounded lab image cycle, while Kubernetes-checkpoint live proof remains explicitly unproven because the lab is configured for the file store.
<!-- SECTION:FINAL_SUMMARY:END -->
