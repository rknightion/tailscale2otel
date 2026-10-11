---
id: TSO-0183
title: >-
  Remove fixed historical cap and live-population claims from cardinality panel
  descriptions
status: To Do
assignee: []
created_date: '2026-10-11 12:09'
labels:
  - needs-triage
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: medium
type: bug
ordinal: 183000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Descriptions assert a 10k cap, a fixed group population and a historical series population/normal overflowing state rather than the deployment's configured limit and current catalog. Found during TSO-0172 (comprehensive audit of both Grafana dashboards against the live stack); not admitted into loop26.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Description text makes no fixed 10k cap assumption and refers to the exported configured limit, including unlimited mode.
- [ ] #2 Group/family descriptions are portable across enabled collector sets and replicas, with no historical live population presented as a contract.
- [ ] #3 Verify generated descriptions and unchanged query/signal coverage.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
