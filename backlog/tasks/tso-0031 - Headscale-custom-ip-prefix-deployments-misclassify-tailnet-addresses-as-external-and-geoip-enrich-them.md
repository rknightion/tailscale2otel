---
id: TSO-0031
title: >-
  Headscale custom ip-prefix deployments misclassify tailnet addresses as
  external and geoip-enrich them
status: To Do
assignee: []
created_date: '2026-08-30 08:45'
labels: []
dependencies: []
priority: medium
type: bug
ordinal: 34000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
enrich.IsTailscaleAddr hardcodes the Tailscale CGNAT/ULA ranges (internal/enrich/devicecache.go:566-572) and internal/geoip/geoip.go:619-651 repeats them. A Headscale deployment using a non-default ip-prefix therefore gets every tailnet address labelled external, and those private addresses are then geo/ASN-enriched as if public. The enrich package doc admits the classification limitation, but geolocating private tailnet addresses looks unintended either way. Suspected during a product-surface review (2026-08-30), unproven - verify with a fixture using a custom prefix. Likely fix: derive the tailnet-address bounds from a configurable prefix set (Headscale exposes its prefixes) shared by both packages instead of two hardcoded copies.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 With a custom Headscale ip-prefix configured, tailnet addresses are classified as tailnet, not external
- [ ] #2 Private/tailnet addresses are never geoip-enriched
- [ ] #3 The address-range logic has one source of truth shared by enrich and geoip
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
