---
id: TSO-0037
title: 'Services collector: displayName, tags rollup and NodeID join'
status: To Do
assignee: []
created_date: '2026-08-30 09:09'
updated_date: '2026-08-30 09:47'
labels: []
milestone: m-3
dependencies: []
priority: medium
ordinal: 40000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The spec added VIPServiceInfo.displayName, and listServices.Tags[] plus listServiceHosts.NodeID are already decoded-but-dropped (unhandled in internal/tsapi/contract/field_dispositions.json). Enrich the existing services collector: displayName as a label/log attribute, a tags rollup consistent with the existing tag_rollup_limit pattern, NodeID to join service hosts to devices. Update field dispositions to consumed.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 displayName, Tags and NodeID are emitted on the relevant service signals with cardinality controls consistent with existing tag handling
- [ ] #2 field_dispositions.json rows flip from unhandled to consumed
- [ ] #3 Dashboard/docs artifacts regenerated where the new attributes surface
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
