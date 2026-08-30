---
id: TSO-0085
title: >-
  Complete the NetworkPolicy egress guidance (MaxMind, FQDN example, sidecar
  note)
status: To Do
assignee: []
created_date: '2026-08-30 09:36'
updated_date: '2026-08-30 09:48'
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
- [ ] #1 MaxMind egress documented alongside the others; FQDN example and sidecar note added
- [ ] #2 helm-docs regenerated (fail-on-diff green)
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
