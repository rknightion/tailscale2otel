---
id: TSO-0198
title: Keep connectivity ratio populations consistent under OS filtering
status: Done
assignee: []
created_date: 2026-10-13 22:59
updated_date: '2026-11-01T21:17:00Z'
labels: []
dependencies: []
references:
  - >-
    /Users/rob/repos/tailscale2otel/deploy/grafana/gen/tabs/devices_connectivity.py
priority: medium
type: bug
ordinal: 200000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Direct-capable % and Hard-NAT % dashboard expressions use fleet-wide aggregate numerators while their device-count denominator receives the OS selector. Selecting one OS can therefore compare different populations. Found while qualifying synthetic browser fixtures; this is a query semantic gap, not evidence of a production data leak.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A non-All OS selection compares matching numerator and denominator populations, or the panels explicitly remain fleet-scoped with consistent queries.
- [x] #2 A mixed-OS fixture demonstrates the prior mismatch and verifies the corrected behavior without fabricating OS labels on fleet-wide aggregates.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Loop33 connectivity lane: mixed-OS reproduction and consistent ratio populations, gate and review.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop33 landed source acdce128b942d8f69b1710dd6fd6955f80594fdf and generated output1646978409c509a5eedaf1ac8f4bab1e9db75b1f. Exact composed remote run20261101T211407Z-tailscale2otel-cb40d4c5 exit0, regeneration idempotent. Mixed-OS PromQL red400% vs fleet80%, corrected fleet ratios preserve tailnet/provider and exclude OS/host consistently. dsreview and independent guarded review PASS; six Python files CodeRabbit-unreviewed covered independently. Live delivery watched separately.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Connectivity ratio panels explicitly use consistent fleet populations under OS selection; mixed-OS/isolation/absence fixtures and exact composed gate pass.
<!-- SECTION:FINAL_SUMMARY:END -->
