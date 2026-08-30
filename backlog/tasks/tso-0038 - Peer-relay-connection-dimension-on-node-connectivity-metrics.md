---
id: TSO-0038
title: Peer-relay connection dimension on node connectivity metrics
status: To Do
assignee: []
created_date: '2026-08-30 09:09'
updated_date: '2026-08-30 09:47'
labels: []
milestone: m-3
dependencies: []
priority: medium
ordinal: 41000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Peer relays went GA 2026-02-18. Investigate whether the API or device data (DevicesRich fields, node metrics endpoint) exposes relay designation or relay-vs-DERP-vs-direct connection usage - the public API surface is unconfirmed, so first step is a live-capture check against .capture/ style fixtures. If present, add it as a connection-type dimension to the existing node connection metrics and a peer-relay health panel; if absent, record the negative result and park.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Investigation records exactly which relay-related fields exist on the wire (or a proven negative result)
- [ ] #2 If present: connection-type dimension emitted, catalogued, and surfaced on the dashboard
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
