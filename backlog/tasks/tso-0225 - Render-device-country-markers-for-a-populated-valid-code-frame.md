---
id: TSO-0225
title: Render device-country markers for a populated valid-code frame
status: To Do
assignee: []
created_date: '2026-11-01T22:45:00Z'
labels:
  - dashboards
  - follow-up
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen/tabs/devices_inventory.py
priority: medium
type: bug
ordinal: 227000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A retained live Devices by country geomap shows a bare basemap while two bounded same-window source probes prove a positive country population and valid ISO2-coded groups. This is not explained by absent location telemetry. The layer location/options and transformed field mapping need validation against the supported installed Geomap schema; the exact cause is not yet asserted. Filed by TSO-0172 (comprehensive live dashboard audit), outside the approved loop33 implementation set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A sanitized nonempty frame with valid country codes produces visible country markers in the shipped panel on the supported Grafana schema.
- [ ] #2 The layer location and measurement field configuration are validated against the real plugin schema rather than a self-mirroring expectation.
- [ ] #3 Legitimate empty location data has a visible neutral explanation; before/after rendered evidence distinguishes the populated defect from empty-source behavior.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
