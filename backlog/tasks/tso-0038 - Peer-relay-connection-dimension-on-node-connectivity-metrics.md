---
id: TSO-0038
title: Peer-relay connection dimension on node connectivity metrics
status: Done
assignee: []
created_date: '2026-08-30 09:09'
updated_date: '2026-08-30 18:31'
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
- [x] #1 Investigation records exactly which relay-related fields exist on the wire (or a proven negative result)
- [x] #2 If present: connection-type dimension emitted, catalogued, and surfaced on the dashboard
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
PRE-WAVE-3 RESEARCH, 2026-08-30 — ALREADY DELIVERED, and the negative half of AC#1 is proven offline.

AC#1, the "which relay fields exist on the wire" question, has three parts and all three are now answered:
- Tailscale REST API: NO relay surface. The vendored spec/tailscale-api.json contains zero occurrences of "relay" in any casing. The LIVE spec fetched 2026-08-30 from the documented ?outputOpenapiSchema=true endpoint (HTTP 200, 245422 bytes, now emitted as YAML not JSON) also contains none. Proven negative.
- Rich device data: the 2026-07-13 live capture of GET /devices?fields=all contains no relay-related field either. Proven negative.
- tailscaled node metrics endpoint: POSITIVE, and already consumed. internal/collector/nodemetrics/curated.go:59-60 and :67 curate tailscaled_peer_relay_forwarded_bytes_total, tailscaled_peer_relay_forwarded_packets_total and tailscaled_peer_relay_endpoints into tailscale.node.peer_relay.{io,packets,endpoints}, described at catalog.go:57-59.

AC#2 is satisfied: all three signals are catalogued and carry the visualized disposition in internal/catalog/signal_dispositions.json, and tailscale.node.peer_relay.endpoints is alertable as well. The node-metrics endpoint is the only surface that carries peer-relay data and it is the one already wired.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Refuted-and-delivered: the peer-relay designation is absent from the REST API (vendored AND live spec, both greppped for "relay" with zero hits) and from rich device data, but present on the tailscaled node-metrics endpoint, where all three peer-relay series are already curated (internal/collector/nodemetrics/curated.go:59-67) and all three carry the visualized disposition.
<!-- SECTION:FINAL_SUMMARY:END -->
