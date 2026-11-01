---
id: TSO-0231
title: Show top node-pair talkers as an identifiable ranking
status: To Do
assignee: []
created_date: '2026-11-01T22:55:00Z'
labels:
  - follow-up
  - dashboards
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: medium
type: bug
ordinal: 233000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Top node-pair talkers (flow logs) renders a selected series as repeated byte-value rows, without visible time or node-pair ranking identity. The retained panel does not answer its advertised ranking question. Filed by TSO-0172 (comprehensive live dashboard audit); outside the approved loop33 implementation set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Rows identify node pairs and their comparable byte measure over a stated window.
- [ ] #2 The source query evaluation and transformations yield a ranking rather than repeated unlabelled time samples.
- [ ] #3 A multi-pair synthetic fixture and rendered evidence preserve ordering, identities and byte units.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
