---
id: TSO-0041
title: 'Verify flow-log native actor identity fields are decoded, not dropped'
status: To Do
assignee: []
created_date: '2026-08-30 09:10'
updated_date: '2026-08-30 09:47'
labels: []
milestone: m-3
dependencies: []
priority: medium
ordinal: 44000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Tailscale announced (~2026-02/03) that network flow logs now carry user identity, device identity and human-readable device name natively. Diff the internal/flowlog record decoder against a fresh .capture/ fixture from the lab tailnet: if identity fields arrive on the wire they may be silently dropped today. If present, decode them and decide how they interact with internal/enrich IP-to-name resolution (cross-check, prefer-native, or replace). Validate against real captures per the repo rule - synthetic fixtures miss wire-format quirks.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A fresh capture establishes whether identity fields are present on the wire (result recorded either way)
- [ ] #2 If present: fields decoded, exposed on flow signals behind the existing pii_filter controls, and the enrich interaction is decided and documented
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
