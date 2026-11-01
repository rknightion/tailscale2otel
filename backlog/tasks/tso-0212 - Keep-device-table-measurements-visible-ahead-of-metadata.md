---
id: TSO-0212
title: Keep table measurements visible ahead of metadata
status: To Do
assignee: []
created_date: '2026-11-01T22:06:00Z'
updated_date: '2026-11-01T23:26:00Z'
labels:
  - dashboards
  - follow-up
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen/tabs/devices_inventory.py
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen/builder.py
priority: high
type: bug
ordinal: 214000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
At the shipped Inventory and Hygiene table widths, Device key expiry (time until) and Last seen (time since) show metadata columns while their question-answering duration measurements are outside the visible table area. Filed by TSO-0172 (comprehensive live dashboard audit); no lab identifiers or raw values are included. Outside the approved loop33 fix set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Each affected table shows a device identifier and its named duration or update/version measurement together at the default shipped width.
- [ ] #2 Extra resource metadata does not push the named measurement offscreen; necessary metadata remains accessible.
- [ ] #3 Synthetic frames with additional metadata columns demonstrate the prior failure and corrected default presentation.
- [ ] #4 The named status, age, scope or measurement remains visible alongside its entity key in the additional affected health, identity and PAM tables at shipped widths.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
The same pattern also affects OAuth client tag restriction class, Key expiry (time until), PAM Service health/Service settings/Policy settings/Organization settings, Health PII filter status, OAuth scope preflight by capability, Node-metrics scrape health by target, Last scrape age (summary), and Last activity age by type. Tailnet settings also needs unambiguous setting names and its Enabled measure. This is a cross-dashboard table contract, not only device inventory.

Additional exact default-placement bindings: raw Node Metrics Advertised routes, Approved routes, Health messages, Home DERP region and Peer relay endpoints hide their named measurement behind metadata. Backing host identity shows Presence and service dimensions but no Node ID/Host/Device ID. The primary result may be an identity or text label; do not require a numeric Value for one-hot info gauges. Build info, Lease leadership, capability-state tables, Connector builds, Plan and configured destinations already show their meaningful label/state/count fields and must not be indiscriminately changed.

Exhaustive repair review withdraws the earlier Updates available table claim: its query selects only devices with update_available == 1, and Host/OS/OS version are visible. Membership answers the question; the one-hot numeric Value is intentionally hidden. Do not change that table merely to expose Value.
<!-- SECTION:NOTES:END -->
