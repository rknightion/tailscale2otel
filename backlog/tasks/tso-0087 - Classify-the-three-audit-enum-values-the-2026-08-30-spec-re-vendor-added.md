---
id: TSO-0087
title: Classify the three audit enum values the 2026-08-30 spec re-vendor added
status: To Do
assignee: []
created_date: '2026-08-30 10:03'
labels:
  - needs-triage
milestone: m-1
dependencies: []
priority: high
type: bug
ordinal: 27000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
main is RED at 1dd76a9 with a clean worktree: `just check` fails its `test` leg on `internal/audit`.

```
--- FAIL: TestAuditVocabularyCoversVendoredSchema (0.02s)
    taxonomy_test.go:53: vendored ConfigurationAuditLog.origin enum "BORDER0_API" lacks a classification
    taxonomy_test.go:53: vendored ConfigurationAuditLog.actor_type enum "PAM_CONNECTOR" lacks a classification
    taxonomy_test.go:53: vendored ConfigurationAuditLog.actor_type enum "PAM_SERVICE_ACCOUNT" lacks a classification
FAIL	github.com/rknightion/tailscale2otel/v4/internal/audit	0.628s
```

Root cause: commit 2b47172 `chore(spec): re-vendor Tailscale OpenAPI spec (58->60 paths)` (2026-08-30 09:44) landed three new `ConfigurationAuditLog` enum values in `spec/tailscale-api.json` without updating the hand-maintained vocabulary maps in `internal/audit/classify.go` — `knownOrigins` (classify.go:92) and `knownActorTypes` (classify.go:139). `TestAuditVocabularyCoversVendoredSchema` (internal/audit/taxonomy_test.go:35) exists precisely to catch that and did.

Effect until fixed: `normalizeOrigin`/`normalizeActorType` fold all three to `other`, so a real Border0 or PAM audit event is invisible on the `tailscale.config.audit.events` origin label and the `tailscale.config.audit.changes` actor-type label. The bounded-cardinality guarantee still holds; the classification is just wrong.

This BLOCKS every m-1 lane: `just check` is the definition-of-done gate for all of them, so it must land before any other Wave 1 work.

Verified 2026-08-30 at 1dd76a9, clean worktree: `test` is the ONLY failing leg. `docs-check`, `gen-check`, `helm-gen-check`, `helm-lint`, `promql`, `rules-check`, `hygiene`, `config-check`, `tidy-check`, `test-python`, `test-modules`, `vuln` and `compose-check` were each run individually and all PASS.

Fix shape (small, mechanical): add `"BORDER0_API": true` to `knownOrigins` and `"PAM_CONNECTOR": true, "PAM_SERVICE_ACCOUNT": true` to `knownActorTypes`, keeping the source comments accurate. Then check whether any curated set in classify.go (deviceChurnActions, apiKeyActions, the actor/origin curated groupings) or a docs table should also name them, and whether docs/metrics.md label documentation enumerates the vocabularies.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 TestAuditVocabularyCoversVendoredSchema passes against the vendored spec at spec/tailscale-api.json
- [ ] #2 just check passes end to end on a clean tree
- [ ] #3 Any doc or catalog table that enumerates the origin/actor_type vocabularies lists the three new values
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
