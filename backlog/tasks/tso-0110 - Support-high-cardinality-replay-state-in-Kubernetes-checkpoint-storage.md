---
id: TSO-0110
title: Shard and compress Kubernetes checkpoint storage to remove the 1 MiB ceiling
status: Done
assignee:
  - '@codex'
created_date: '2026-09-01 21:38'
updated_date: '2026-09-02 09:21'
labels: []
dependencies:
  - TSO-0111
priority: high
type: bug
ordinal: 111000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Kubernetes checkpoint backend stores the entire shared checkpoint map as one JSON value in one ConfigMap data entry, deliberately, so that a single resourceVersion-guarded update covers every cursor atomically. That ceiling is 1,048,576 bytes and it is reachable at stock defaults: one object-store feed at the default max_seen_keys of 5000 already serialises to 785,001 bytes, and three feeds reach 2,355,001. TSO-0111 converts the resulting runtime failure into a startup rejection; this task removes the ceiling.

Owner decision 2026-09-02: shard the map into one ConfigMap per collector namespace and gzip each shard into binaryData.

Sharding along the collector boundary is chosen because cross-collector atomicity buys nothing - collectors advance their cursors independently, and no invariant spans two of them - while per-collector atomicity, which is the property that actually matters, is preserved by a per-shard resourceVersion. The boundary already exists in the key namespace the scheduler assigns, so the shard key needs no new concept. Compression is applied because the payload is close to a worst case for gzip: base64 keys over a shared prefix, mapped to timestamps that differ in their last few characters.

Rejected alternatives, with their reasons, so they are not re-proposed: gzip alone keeps global atomicity but only raises the ceiling and returns at higher cardinality; moving the seen-set to SQLite or a PVC makes durable local storage effectively mandatory for object-store ingestion under coordination, which is the deployment shape coordination exists to serve; clamping max_seen_keys silently degrades restart dedup and caps throughput on exactly the deployments that scale out.

Oversize handling must keep the Wave 5 property that a payload which still cannot fit fails visibly and never truncates, and the deposed-leader guarantee that a stale writer cannot overwrite current state must hold per shard.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Checkpoint state is sharded across one ConfigMap per collector key namespace, and each shard's payload is gzip-compressed into binaryData
- [x] #2 Every shard write is guarded by that shard's own resourceVersion, so a deposed leader cannot overwrite a current shard, and a conflict is surfaced rather than retried into an overwrite
- [x] #3 The three measured configurations in TSO-0111 persist successfully, and a shard that still exceeds the limit fails visibly without truncating or dropping keys
- [x] #4 A store opened over checkpoint state written by the single-ConfigMap backend continues to work, and the migration path is exercised by a test rather than assumed
- [x] #5 Shard count, per-shard compressed size and the compression ratio are observable, so an operator can see headroom before it is exhausted
- [x] #6 The Helm chart's checkpoint RBAC covers every ConfigMap the sharded store creates, and no rule is broader than the objects it needs
- [x] #7 Tests cover ordinary poll cursors, the high-cardinality seen-set and gap state, and the deposed-leader conflict path per shard
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 6 Lane C plan after TSO-0111 landed at b0eb87a: implement one gzip binaryData ConfigMap per collector namespace; preserve per-shard resourceVersion conflict safety; migrate the legacy single-ConfigMap JSON state on open; retain visible no-truncation oversize failure; expose shard count, compressed size and compression ratio; narrow Helm RBAC to the derived shard names; update ShardKey and the startup projection to the real per-namespace seam; write failing tests first for ordinary cursors, high-cardinality seen/gap rows, migration, conflict and oversize paths. Root will integrate any app/catalog/generated surfaces, run CodeRabbit and full gates, commit/push, commission the adversarial Lane D review, then verify exact-head CI and the authorised lab rollout/rollback.

Integration correction after current Kubernetes RBAC verification: runtime-derived shard names cannot be prefix-matched by resourceNames; top-level create cannot be name-scoped; and list with resourceNames requires an exact metadata.name field selector already known to the caller. Keep get, list, update and create limited to ConfigMaps in the configured coordination namespace, omit delete, watch and patch, and record owner acceptance of this unavoidable namespace boundary as a mandatory final question. A dedicated namespace or pre-created fixed shard inventory would be a new deployment contract and is not introduced by this run.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Lane C and root integration evidence: one gzip binaryData ConfigMap is written per collector namespace, with object-store cursor, scan, seen and gap rows held in one feed shard and ordinary flow cursor plus replay rows held in flowlogs or tailnet/flowlogs. Post-sharding production-encoder measurements are 15,846 total and max bytes for one feed in one tailnet; 47,381 total and 15,847 max for three feeds in one tailnet; and 143,519 total and 16,302 max for three feeds in three tailnets. Root review corrected replay rows accidentally becoming one shard per key, cross-release shard discovery, Lease-name hash collisions, interrupted migration being mistaken for completion, removal of the TSO-0111 measurements and rejection coverage, duplicate gzip implementations, and a conflict in one shard blocking a synchronous write to an independent shard. The focused race suite and Helm render gate then passed. Five guards were deliberately broken and restored: replay grouping, Lease-owner discovery, Lease-inclusive name hashing, interrupted migration resumption, and compressed startup-limit rejection; each failed independently while broken. The legacy JSON ConfigMap is retained unchanged and marked only after every migration shard is durable, so interrupted migration resumes and the old release can reopen the legacy object. Narrow reversible RBAC choice: grant ConfigMap get, list, update and create in the configured namespace, with no delete, watch or patch. Kubernetes cannot prefix-match resourceNames, cannot name-scope top-level create, and list with resourceNames requires an exact metadata.name selector, so dynamic shard discovery cannot satisfy literal object-name scoping. This exception is carried to the final Questions for Rob section.

CodeRabbit integration follow-up: a valid major finding showed synchronous and debounced multi-shard persistence stopped at the first failing shard. A regression first failed with zero independent shards written, then both paths were changed to attempt every shard, join errors, remove successful shards from the dirty set, and retry only failures; the focused race selector passed. The oversize fixture now uses high-order deterministic PRNG bits and first asserts that the production gzip encoder remains above 1,048,576 bytes. Migration now also has explicit resourceVersion-conflict interruption and successful-resumption coverage, in addition to the create-failure case; the legacy source remains unmarked until the conflict is resolved. ConfigMap creates and updates use a 30-second per-write context deadline while retaining the lock and exact shard resourceVersion, so a timeout cannot become a stale overwrite. Narrow compatibility choice: binaryData requires Kubernetes API server 1.10 or later; the ConfigMap is API-written rather than mounted, so no kubelet minimum is claimed. This modern API floor is recorded here rather than adding an obsolete-version runtime discovery path.

Final CodeRabbit source finding fixed: shard ConfigMaps now carry an explicit shard-key annotation; discovery requires it, derives the canonical Lease-plus-shard name, and rejects a mislabeled or misplaced object before decoding. The new test failed while the annotation contract was absent and passes after create, update and list share it. Two tracker findings were rejected as false positives: every task change in this run was made through backlog task edit, never by hand, and the appended Integration correction explicitly supersedes the earlier derived-name RBAC wording because append-only plan updates are required.

Last staged-review follow-up: the 30-second Kubernetes API deadline is now one shared budget per store open, synchronous batch, debounced persist, or shutdown flush, rather than multiplying once per shard. The regression first observed two distinct sibling deadlines and then passed with one identical deadline. A reviewer suggestion to silently re-home a row whose stored shard disagrees with current ShardKey was rejected: gzip-shards-v1 makes ownership an on-disk invariant, this is the first release of that format, and automatic rewrite would convert corruption or an incompatible future format into writes during open. An explicit test now pins fail-closed rejection; any future ownership change must bump the envelope format and ship a deliberate migration.

Final review size finding rejected against the current Kubernetes API-server implementation: ValidateConfigMap sums len(value) for each decoded BinaryData entry and compares that raw total with MaxSecretSize. JSON base64 wire expansion is not part of the admitted ConfigMap data budget, so the store and startup projection correctly compare raw gzip bytes; using base64.EncodedLen would falsely discard one quarter of usable capacity. The source comment now pins this API fact.

Final coverage review: ShardKey now has explicit production-format cursor, scan, seen and gap row cases, all resolving to the same feed namespace. A request for a new collector-level Collect harness was rejected as duplicate coverage: atomicity_test.go already invokes Collect and asserts durable seen/gap behavior, layout_test.go invokes Collect and asserts scan-row persistence, and objectstore_test.go exercises durable cursor advancement and restart behavior. The new projection test owns only the shared row-to-shard mapping.

Correction to the earlier per-write timeout wording: it is superseded. The implementation has one shared 30-second parent deadline for the complete store open, synchronous multi-shard batch, debounced persist, or shutdown flush; it is not 30 seconds per shard. Decompression hardening choice: gzip-shards-v1 has a symmetric 128 MiB decoded-envelope ceiling enforced by both the shared encoder and limited decoder, preventing an admitted compressed shard from expanding without bound or becoming state the writer cannot reopen. Tests inject a small limit and prove both decode rejection and encode symmetry. The namespace-wide ConfigMap RBAC boundary still requires Robs explicit acceptance in the final mandatory question; this run does not manufacture that approval. The implemented grant remains get, list, update and create only in the configured coordination namespace, excluding delete, watch and patch.

Lane D adversarial review at the integrated head proved deposed-leader conflict rejection, independent sibling-shard persistence, initial and interrupted migration resumption, compressed oversize refusal, symmetric bounded decompression and the shared operation deadline. It found one valid ambiguity: a ConfigMap API commit followed by a lost response could leave persistence uncertain. The fix landed test-first in 976cc00c1f004922d1cec5936987abfa01f6f67b: non-definitive create/update errors now surface ErrKubernetesCheckpointWriteUncertain and end the active app lifecycle so Kubernetes restarts from authoritative API state; Conflict and AlreadyExists stay definitive and are never reloaded into an overwrite. CodeRabbit returned zero findings on the ten-file fix. Final just check passed, just gen left no diff, just --fmt --check passed, and exact-head CI run 33611867414 completed success with all 26 jobs successful. Auto-RC run 33612619093 cut v4.1.0-rc.57 at the same SHA and published immutable image digest sha256:700279e397ba94edeb3eec1bb16242522c15fd554030e98cd2a01e3ddcd1e3b4. The authorised lab image cycle ran that digest 1/1 ready with health and readiness HTTP 200 and no checkpoint/coordination error or fatal, then restored the prior immutable digest with the same checks. Preflight found checkpoint.store=file and zero legacy or shard ConfigMaps, so migration, shard count/size, annotations and legacy reopen are not live-proven. No helper objects were created. The unresolved rollback-era write policy remains: if an old release writes the retained legacy object after migration, a later re-upgrade currently ignores that write; dual-write was not invented because it recreates the ceiling and requires an explicit reconciliation policy.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Sharded and compressed Kubernetes checkpoint persistence landed in 2901c12e4ba66ab386335aa20008ed3d5d92acec and the adversarial uncertain-write fix landed in 976cc00c1f004922d1cec5936987abfa01f6f67b. Tests prove per-shard resourceVersion safety, all measured high-cardinality configurations, visible no-truncation oversize handling, restart-safe legacy migration, observability, minimal dynamic-shard RBAC and ordinary/replay/gap paths; CodeRabbit, just check, generation/fmt checks and exact-head CI 33611867414 passed. RC.57 was healthy during the bounded lab image cycle and the previous digest was restored; live shard/migration proof remains explicitly unproven because the lab uses checkpoint.store=file.
<!-- SECTION:FINAL_SUMMARY:END -->
