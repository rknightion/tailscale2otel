---
id: TSO-0042
title: Adjudicate the 14 unhandled API response fields in the contract ledger
status: Done
assignee:
  - '@codex'
created_date: '2026-08-30 09:10'
updated_date: '2026-08-30 16:32'
labels: []
milestone: m-3
dependencies:
  - TSO-0037
priority: low
ordinal: 45000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
internal/tsapi/contract/field_dispositions.json carries 14 decoded-but-dropped fields with disposition unhandled and no note: listUsers.DisplayName/ProfilePicURL/TailnetID, listTailnetKeys.Updated, listWebhooks.LastModified, listOAuthApps.Updated/Description, listConfigurationAuditLogs.Version, listUserInvites.ID/InviterID/TailnetID, listTailnetDevices.TailnetLockKey, listServices.Tags[], listServiceHosts.NodeID (the last two are covered by the Services task TSO-0037). For each remaining field: either emit it (mostly freshness timestamps suited to staleness panels) or formally park it with a note explaining why. Zero rows left noteless.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Every unhandled row either becomes an emitted signal/attribute or carries an explicit parking note
- [x] #2 Newly emitted signals are catalogued and reach a dashboard surface (per the signal-coverage gate)
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
C2: adjudicate every remaining noteless unhandled response field after the Services lane; prefer explicit parking notes unless an emitted signal is justified and panel ownership is available; leave zero noteless rows; return focused-check evidence without tracker writes or commits.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Latitude deviation: the run contract called for one commit per feature, but root retained the already-integrated shared-tree feature commit fa6a465 plus review-fix commit a18a5dd rather than performing prohibited destructive history surgery after integration and push. All task evidence is tied to the verified implementation head a18a5dd06f9ac9c8b84fda73bba653ded2398d5a.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Adjudicated the response-field contract ledger with concrete types, dispositions, and explanatory notes; zero noteless rows remain. Verified by the negative-tested contract guard, final just check, and exact-head CI run 33322449434 at a18a5dd06f9ac9c8b84fda73bba653ded2398d5a (success).
<!-- SECTION:FINAL_SUMMARY:END -->
