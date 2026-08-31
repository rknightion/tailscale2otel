---
id: TSO-0073
title: Mutual TLS on the admin listener
status: Done
assignee: []
created_date: '2026-08-30 09:34'
updated_date: '2026-08-31 03:39'
labels: []
milestone: m-5
dependencies: []
priority: medium
ordinal: 76000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Prometheus pull listener supports client_ca_file mutual TLS (internal/app/metrics.go:102-148); the admin listener - which exposes strictly more (support bundle, config, pprof) - does not. Add the same client-CA option to the admin server config. Security-surface change: adversarial review tier.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 admin supports client_ca_file with the same semantics as the metrics listener
- [x] #2 Config schema/docs regenerated; covered by TLS handshake tests
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Root F1 freezes admin listener client-CA/client-auth fields matching the metrics listener; lane G later wires TLS and handshake tests.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Lane G added admin client-CA/client-auth TLS parity with fail-closed CA loading and real loopback handshake tests for missing, untrusted and trusted client certificates plus token-auth composition. Deliberate negative mutations proved the guard tests.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added admin listener client-CA and client-auth mTLS parity, fail-closed CA loading, schema and docs coverage, and real loopback TLS handshake tests. Implementation SHA 6d9c23c. Final integrated just check passed at 5b55617; exact-head CI run 33354208183 completed success.
<!-- SECTION:FINAL_SUMMARY:END -->
