---
id: TSO-0042
title: Adjudicate the 14 unhandled API response fields in the contract ledger
status: To Do
assignee: []
created_date: '2026-08-30 09:10'
updated_date: '2026-08-30 09:48'
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
- [ ] #1 Every unhandled row either becomes an emitted signal/attribute or carries an explicit parking note
- [ ] #2 Newly emitted signals are catalogued and reach a dashboard surface (per the signal-coverage gate)
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
