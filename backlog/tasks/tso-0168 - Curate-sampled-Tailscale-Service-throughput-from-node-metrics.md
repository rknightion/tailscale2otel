---
id: TSO-0168
title: Curate sampled Tailscale Service throughput from node metrics
status: Done
assignee:
  - '@loop23'
created_date: '2026-10-09 22:59'
updated_date: '2026-10-10 17:42'
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
- [x] #1 Both Serve byte counter families feed a first-class OTEL byte counter with bounded receive/transmit direction, existing node identity and Service identity derived from the observed service key; a redacted captured-shape fixture proves mapping and safe handling of absent or unsupported Service shapes
- [x] #2 Real node-metrics collector plus telemetry recorder tests cover successive scrapes and counter reset, share raw per-series delta accounting without double-counting or an independent baseline, and preserve raw forwarding and current passthrough filter behavior
- [x] #3 Service and node identity obey existing cardinality and PII contracts; hostile extra labels and folded or filtered identities cannot inject IP, URL or free-text dimensions or create duplicate curated series; fixtures contain no real identifiers
- [x] #4 Catalog, generated metric documentation, disposition coverage and generated dashboard include the Service-throughput signal and normalized Prometheus panel; tests connect panel to emitted metric, existing raw families remain available, just check and generator stability pass without a required live write
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Implement additive Service byte curation using existing raw delta accounting; inspect captured label shape locally, redact fixtures, prove boundary mapping/reset/filter/cardinality; regenerate catalog/dashboard, run isolated gate, security review and sharded CodeRabbit; root lands only after TSO-0163 composed gate.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop22: two worker cycles partial, no final gate yet. Root narrow ownership-gap amendments: sentinel registry has_node_service_io entry in builder.py; public metric-count summaries in README, docs/index, docs/comparison (333 to catalog-derived 334). Authentic retained local series-response evidence located after empty supplied capture directory; fixtures synthetic. Dispatch final authorized lane-worker-retry cycle for summaries, dispositions, exact-tree gate and generation stability; third failure parks under D26.

loop22: 3 implementation/verification cycles consumed, third/final retry stopped on scratch capture verification KeyError data after successful regeneration. This is verification-script failure, not proof of a collector bug. Current-tree regression tests, full just check, generator stability and just format verification remain unrun; guarded security review and CodeRabbit not started. Candidate retained uncommitted in isolated feature worktree; no source landed or dashboard delivery. D26 mandates park on third failure. Resume: owner regrade ceiling, correct scratch JSON envelope inspection, then exact retained-tree validation and required reviews before any land.

loop23 recommission (Rob, 2026-10-10): previous failure -> loop22 third cycle stopped on a scratch verification script KeyError 'data' after successful regeneration; no candidate defect was shown. Changed premise -> the capture envelope is now known: the retained series evidence is {response: {status, data: [label sets], warnings}}, so labels are read from response.data, and the owner raised the ceiling for a bounded continuation. Discriminating check -> the retained patch (sha256 799f51ad6179c7959d886c5964893f54de00509d64abde8772ab30ac05eeafa9) applied unchanged to a fresh worktree of current main passes the nodemetrics and generator tests, the composed gate and gen stability. Remaining allowance -> one verification cycle plus at most one repair cycle if a real defect is found; park on any further failure.

loop23: verification cycle 1 applied unchanged retained patch; collector race and Python tests passed. Initial gate false red was unstaged index precondition (not source defect); exact-tree staging retry passed full setup/check, gen twice and fmt. Cycle 2 (sole authorized source repair) corrected independently proven major: Service panel now scopes tailnet/provider before aggregation, with real generated-query proof failing before at 15 B/s and passing after at selected 1 B/s. Repaired 40-file tree c2736c4c284328e376497558b17b0c72ac3b1861, complete patch SHA256 68ed6d6b723633d9d78b0c22ad15248d01cc54c3f4135baad830b981a02643e5; only 4 files changed, other 36 frozen. Full gate/gen stability/fmt green, security full plus delta PASS. CodeRabbit initial all40 complete, zero major, one pre-existing minor log-count inconsistency outside metric-count-only ownership left unchanged; delta pending. No live probes/writes or publication yet; source repair allowance exhausted.

loop23 done: 3 historical cycles plus 1 owner-authorized verification cycle and 1 owner-authorized source-repair cycle; staged-index precondition retry was environment verification, not extra source repair. All acceptance proof green on exact retained and landed identities; no SIGTERM. Source repair allowance exhausted, no further work admitted.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
loop23: landed sampled Service throughput at 3393e3c2d4feab34418cf8ee434f98b59d5f3679 after verifying unchanged retained collector/catalog/dashboard candidate, then using sole allowed repair cycle to scope the new panel to selected tailnet/provider. Real generated-query proof failed before at 15 B/s and passed after at selected 1 B/s. Collector race/Python tests, full setup/check, two stable generations and format validation passed on exact repaired tree c2736c4c284328e376497558b17b0c72ac3b1861. Full security review plus four-file delta PASS; CodeRabbit initial all40 and delta all4 complete, zero critical/major or unreviewed files. Pre-existing README log-count minor left outside ownership; suggested test decoupling rejected because acceptance requires actual catalog-to-panel linkage. Post-land clean detached full setup/check exit0 (362.263s) on exact landed SHA; CI38072151170 ci-success success. Helm check absent per unchanged push paths, not a pass. grafana-sync38072151079 successfully published alert links and delivered dashboard via GitSync; no direct Grafana reads/writes or live probes.
<!-- SECTION:FINAL_SUMMARY:END -->
