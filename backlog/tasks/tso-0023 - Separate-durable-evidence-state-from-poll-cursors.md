---
id: TSO-0023
title: Separate durable evidence state from poll cursors
status: Done
assignee:
  - '@codex'
created_date: '2026-08-27 18:27'
updated_date: '2026-08-27 19:21'
labels:
  - configuration
  - observability
dependencies: []
priority: high
type: bug
ordinal: 26000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The state-store selector currently treats poll high-water marks and semantic evidence as one durability class. A streamed deployment can reasonably choose an in-memory cursor store because it has no poll cursors, but that same choice resets ACL ETag first-observed provenance on every restart and makes the dashboard age track process lifetime. Give restart-stable evidence an explicit durability contract that does not depend on whether flow or audit ingestion is polled. Preserve existing deployments and checkpoint data while making degraded durability visible.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A deployment can keep unused poll cursors in memory while ACL revision provenance remains durable across exporter restarts.
- [x] #2 Existing file-backed checkpoint keys migrate or remain readable without resetting known ACL revision or audit timestamps.
- [x] #3 Configuration and status output distinguish poll-cursor durability from semantic-evidence durability; an unsafe combination is rejected or produces a specific actionable warning.
- [x] #4 Tests cover streamed flow and audit ingestion, an in-memory cursor choice, stable and changed ACL revisions, repeated restarts, and absent authoritative audit history.
- [x] #5 Example configuration, environment reference, installation and checkpoint documentation, and deployment templates describe the two durability classes consistently; generated artifacts are regenerated.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 go build ./... && go vet ./... && go test -race ./...
- [x] #2 golangci-lint run
- [x] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Map checkpoint/provenance/config/status consumers and compatibility keys; freeze separate poll-cursor and semantic-evidence durability seams; add failing streamed-deployment and migration tests; implement the minimum backward-compatible split; update generated docs/templates/status; run full gates and review before finalization.

Frozen seam: retain checkpoint.store as the poll-cursor selector; add checkpoint.evidence_store (file|memory, default file) sharing checkpoint.file_path. Build cursor/evidence stores together so both file-backed classes share one FileStore instance, memory cursors plus file evidence reopen existing ACL keys, and no concurrent file-store snapshots can clobber each other. Wire only ACL/audit provenance to evidence; keep schedulers/object-store/replay on cursors. Surface both effective outcomes and warn specifically when evidence is memory/degraded.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Fan-out infrastructure note: five correctly routed Codex MAPPING lanes (Luna/medium) all failed before repository access with the same configured Responses-endpoint HTTP 404. No child edits, tracker writes, commits, deployments, or live-system actions occurred. Per the canonical route contract, no substitute route was reported as equivalent; root continued locally.

Implementation landed as 66fcb05. Verification observed: streamed flow/audit + memory-cursor/file-evidence restart tests; existing combined-file key compatibility; distinct status API/HTML fields and memory warning; config/API/Helm/env regeneration; Compose 47/47 and Helm render suite green; full go build/vet/race gate green; golangci-lint 0 issues. CodeRabbit organization-plan review found one major and one minor, both fixed and revalidated; fresh review had one invalid minor dismissed because the generated env field is implemented and tested.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Separated poll-cursor durability from semantic ACL evidence in 66fcb05. checkpoint.store now controls cursors while checkpoint.evidence_store independently defaults to file and shares the existing atomic checkpoint path, so streamed deployments can use memory cursors without resetting ACL provenance. Existing keys reopen unchanged; status and warnings expose degraded evidence durability. Verified by restart/compatibility/preflight tests, generated schema/docs, Compose 47/47, the full Helm render suite, full build/vet/race tests, lint with 0 issues, and two organization-plan CodeRabbit passes.
<!-- SECTION:FINAL_SUMMARY:END -->
