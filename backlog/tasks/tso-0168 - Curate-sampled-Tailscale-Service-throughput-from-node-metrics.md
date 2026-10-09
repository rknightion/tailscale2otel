---
id: TSO-0168
title: Curate sampled Tailscale Service throughput from node metrics
status: To Do
assignee: []
created_date: '2026-10-09 22:59'
labels:
  - needs-triage
dependencies: []
references:
  - internal/collector/nodemetrics/curated.go
  - spec/changelog-reviewed.json
priority: medium
type: feature
ordinal: 168000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Raw node metrics already forward Serve inbound/outbound byte counters, but operators have no first-class catalogued Service-throughput metric or panel. The earlier parked verdict in changelog:2026-08-03-client required real samples to establish the Service label key. Discovery now found 11,683 inbound and 11,941 outbound samples over 30 days, positive aggregate increases, and the service label key. This is new evidence satisfying that resume boundary, not proof of any future implementation. Curation should be additive and share the existing raw delta accounting, not add a scrape or independent baseline.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Both Serve byte counter families feed a first-class OTEL byte counter with bounded receive/transmit direction, existing node identity and Service identity derived from the observed service key; a redacted captured-shape fixture proves mapping and safe handling of absent or unsupported Service shapes
- [ ] #2 Real node-metrics collector plus telemetry recorder tests cover successive scrapes and counter reset, share raw per-series delta accounting without double-counting or an independent baseline, and preserve raw forwarding and current passthrough filter behavior
- [ ] #3 Service and node identity obey existing cardinality and PII contracts; hostile extra labels and folded or filtered identities cannot inject IP, URL or free-text dimensions or create duplicate curated series; fixtures contain no real identifiers
- [ ] #4 Catalog, generated metric documentation, disposition coverage and generated dashboard include the Service-throughput signal and normalized Prometheus panel; tests connect panel to emitted metric, existing raw families remain available, just check and generator stability pass without a required live write
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
