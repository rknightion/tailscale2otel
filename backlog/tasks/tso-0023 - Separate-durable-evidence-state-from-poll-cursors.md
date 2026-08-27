---
id: TSO-0023
title: Separate durable evidence state from poll cursors
status: To Do
assignee: []
created_date: '2026-08-27 18:27'
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
- [ ] #1 A deployment can keep unused poll cursors in memory while ACL revision provenance remains durable across exporter restarts.
- [ ] #2 Existing file-backed checkpoint keys migrate or remain readable without resetting known ACL revision or audit timestamps.
- [ ] #3 Configuration and status output distinguish poll-cursor durability from semantic-evidence durability; an unsafe combination is rejected or produces a specific actionable warning.
- [ ] #4 Tests cover streamed flow and audit ingestion, an in-memory cursor choice, stable and changed ACL revisions, repeated restarts, and absent authoritative audit history.
- [ ] #5 Example configuration, environment reference, installation and checkpoint documentation, and deployment templates describe the two durability classes consistently; generated artifacts are regenerated.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 go build ./... && go vet ./... && go test -race ./...
- [ ] #2 golangci-lint run
- [ ] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->
