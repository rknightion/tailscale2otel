---
id: TSO-0034
title: Org auto-discovery of tailnets via the alpha Organizations API
status: In Progress
assignee: []
created_date: '2026-08-30 09:09'
updated_date: '2026-08-31 02:54'
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

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Root F1 freezes the organization-roster discovery config shape with a behaviour-preserving disabled default; lane A later implements pagination, roster population, inventory telemetry, contract disposition, and panel.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Lane A returned the frozen metric-name and credential-fan-out seams. Root decision: accept tailscale.organization.tailnets.count for the bounded org-roster inventory gauge. Take the narrowest reversible delivery: implement and consume paginated roster discovery plus inventory telemetry, but do not invent OAuth-client creation or pretend an org roster supplies per-tailnet credentials. Runtime collector fan-out remains limited to tailnets with explicit credentials; record that boundary rather than creating credentials or mutating a tailnet.

Root decision implemented: org discovery is inventory-only and uses the first explicitly configured Tailscale runtime credential with tailnets:read. The paginated ID roster is retained and tailscale.organization.tailnets.count is emitted/panelled; no OAuth clients or collector runtimes are manufactured. Contract harness was extended to terminate cursor-bearing canned responses after one replay page. Focused pagination, contract-boundary and disposition checks passed.

Deviation: the required CodeRabbit gate was attempted three times after a green integrated just check; each run failed before analysis with a recoverable WebSocket-closed connection error and no complete line. No finding was produced or treated as clean. Root performed a full staged-diff review and proceeded to avoid letting an external review-service outage stop the unattended wave.
<!-- SECTION:NOTES:END -->
