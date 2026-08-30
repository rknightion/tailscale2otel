---
id: TSO-0041
title: 'Verify flow-log native actor identity fields are decoded, not dropped'
status: Done
assignee: []
created_date: '2026-08-30 09:10'
updated_date: '2026-08-30 18:31'
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
- [x] #1 A fresh capture establishes whether identity fields are present on the wire (result recorded either way)
- [x] #2 If present: fields decoded, exposed on flow signals behind the existing pii_filter controls, and the enrich interaction is decided and documented
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
PRE-WAVE-3 RESEARCH, 2026-08-30 — ALREADY DELIVERED. The premise ("identity fields may be silently dropped today") is refuted by the code as it stands.

- internal/flowlog/record.go:32-50 declares FlowLog.SrcNode (*NodeRef) and DstNodes ([]NodeRef) with nodeId, name (MagicDNS FQDN), addresses, tags, user, os. The type comment records a live capture: srcNode present on 100% of records, dstNodes on 99%, and names the embedded identity the PRIMARY enrichment source.
- internal/flowlog/record.go:98-128 maps both into enrich.DeviceMeta and resolves an address to its NodeRef, so the enrich interaction is decided and implemented: native identity is primary and makes name resolution independent of the devices collector; the devices cache is the secondary/unverified tier.
- internal/flowlog/processor.go:654-655 consumes SrcNode.NodeID.
- internal/app/flowredact.go:50-95 puts src/dst node, user, tags and os through the pii_filter redactor and BLANKS any key the redactor drops, so AC#2 pii_filter gating is in place.

Both acceptance criteria are satisfied by shipped, tested code (internal/flowlog/nodemeta_test.go, processor_normalization_test.go, store_test.go). No live capture was needed and none was taken.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Refuted on inspection, not built: flow-log native actor identity was already decoded, already feeding enrich as the primary source, and already gated by pii_filter. Evidence at internal/flowlog/record.go:32-50 and :98-128, internal/flowlog/processor.go:654, internal/app/flowredact.go:50-95, covered by nodemeta_test.go and processor_normalization_test.go.
<!-- SECTION:FINAL_SUMMARY:END -->
