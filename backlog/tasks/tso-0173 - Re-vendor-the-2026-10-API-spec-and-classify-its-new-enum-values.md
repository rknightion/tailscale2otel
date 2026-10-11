---
id: TSO-0173
title: Re-vendor the 2026-10 API spec and classify its new enum values
status: In Progress
assignee:
  - loop26
created_date: '2026-10-11 10:40'
updated_date: '2026-10-11 12:07'
labels: []
dependencies: []
references:
  - spec/README.md
  - 'https://api.tailscale.com/api/v2?outputOpenapiSchema=true'
priority: high
type: chore
ordinal: 173000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The live OpenAPI spec (fetched 2026-10-11) has no new paths since the 2026-08-30 vendor, but it adds GET fields and enum values. The audit target property ROUTE_SELECTION and event filters TAILNET.UPDATE.ROUTE_SELECTION / GROUP.UPDATE.USER_ROLE are new; the logstream destinationType enum gains crowdstrike, which boundedDestinationType currently folds to unknown. Tailscale also announced Azure Blob streaming (2026-06-17) but azure is not in the spec enum, so its wire value is unconfirmed. Re-vendoring turns the audit taxonomy guard red until ROUTE_SELECTION is classified, as happened in TSO-0087. Read spec/README.md first: the live endpoint emits YAML and must be converted.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 spec/tailscale-api.json matches the live spec and the apidrift tool reports no drift against it
- [ ] #2 The audit taxonomy test is seen failing on ROUTE_SELECTION before the classification and passing after
- [ ] #3 A logstream configuration with destinationType crowdstrike emits that value as the bounded destination label, not unknown; azure is added only if a capture or the docs confirm the wire value
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Fetch and convert live spec; observe taxonomy failure; classify confirmed enum additions and prove emitted logstream label; return staged candidate for root review, serialized full gate and landing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop26: implementation attempt 1 returned seven staged paths on base faa8cd30875301b8ae44901521b2405a668d8272, independently reviewed index tree 9d6ec8c89f7e255dea300917a6e9f565abbff26b. Full candidate gate passed exit 0; live spec byte identity, taxonomy failing-before/passing-after and emitted crowdstrike label verified. CodeRabbit completed all seven files, zero critical/major, one minor retained as TSO-0177 (make the documented API spec refresh failure-atomic). Published feature commit d467fb4466e725d7df377637613b9c128fe23ea5. Fresh composed gate and exact-SHA CI pending; no criteria checked yet.
<!-- SECTION:NOTES:END -->
