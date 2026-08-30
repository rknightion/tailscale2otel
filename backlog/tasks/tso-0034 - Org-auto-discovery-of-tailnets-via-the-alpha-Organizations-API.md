---
id: TSO-0034
title: Org auto-discovery of tailnets via the alpha Organizations API
status: To Do
assignee: []
created_date: '2026-08-30 09:09'
updated_date: '2026-08-30 09:47'
labels: []
milestone: m-3
dependencies: []
priority: medium
ordinal: 37000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Use the alpha Organizations API (listOrganizationTailnets, tailnets:read scope, paginated limit/cursor max 100/page) to auto-discover an org tailnet roster for multi-tailnet mode instead of hand-maintaining the tailnets: list, plus an org tailnet-count inventory gauge. The operation is currently dispositioned parked in internal/tsapi/contract/operation_dispositions.json - flip to consumed when implemented. Alpha API: churn risk accepted by the owner (2026-08-30). Auth/credential fan-out per discovered tailnet needs design (the create API returns a per-tailnet OAuth client; discovery alone does not solve per-tailnet creds).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Multi-tailnet mode can populate its tailnet set from the org roster, with pagination handled
- [ ] #2 An org tailnet-count gauge is emitted and catalogued
- [ ] #3 Disposition ledger updated to consumed and contract manifest covers the operation
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
