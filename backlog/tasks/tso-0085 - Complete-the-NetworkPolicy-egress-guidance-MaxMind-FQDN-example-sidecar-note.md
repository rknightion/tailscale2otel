---
id: TSO-0085
title: >-
  Complete the NetworkPolicy egress guidance (MaxMind, FQDN example, sidecar
  note)
status: Done
assignee: []
created_date: '2026-08-30 09:36'
updated_date: '2026-08-31 03:39'
labels: []
milestone: m-7
dependencies: []
priority: low
ordinal: 88000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The values.yaml egress comment (deploy/helm/tailscale2otel/values.yaml:484-505) lists API/OTLP/S3 destinations but omits the MaxMind download endpoint used by enrichment.geoip.download - a fourth silent-failure egress under allowAll: false. Add it, plus a worked Cilium/Calico FQDN example and a note on extraContainers sidecar grace-period ordering vs the 45s staged shutdown (values.yaml:173-215). Regenerate the chart README.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 MaxMind egress documented alongside the others; FQDN example and sidecar note added
- [x] #2 helm-docs regenerated (fail-on-diff green)
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Lane K adds MaxMind egress, FQDN-policy examples, and sidecar shutdown-order guidance, then regenerates the Helm README.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Lane K documented MaxMind egress, worked Cilium/Calico FQDN policies, and sidecar shutdown ordering, then regenerated the Helm README. Helm lint/template and drift checks passed; code tests were intentionally skipped for documentation/declarative config.

Required CodeRabbit pre-commit review attempted on the integrated staged diff after just check passed; the service failed before analysis with recoverable  and emitted no  line. Treated as a failed review, not a clean result. Root manually reviewed the full staged diff and found no blocking issue; this is an overnight review-service deviation.

Correction to the preceding note: the exact recoverable error was WebSocket closed, and the review emitted no complete status line.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Documented MaxMind egress, Cilium and Calico FQDN policy examples, and sidecar shutdown ordering, then regenerated and verified Helm documentation. Implementation SHA d3af40f. Final integrated just check passed at 5b55617; exact-head CI run 33354208183 completed success.
<!-- SECTION:FINAL_SUMMARY:END -->
