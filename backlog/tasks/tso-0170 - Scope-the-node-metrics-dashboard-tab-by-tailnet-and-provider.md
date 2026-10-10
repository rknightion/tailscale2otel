---
id: TSO-0170
title: Scope the node-metrics dashboard tab by tailnet and provider
status: To Do
assignee: []
created_date: '2026-10-10 21:46'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 170000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Loop23 security review of TSO-0168 found that a new node-metrics panel summed traffic from every deployment on the stack because it ignored the dashboard `$tailnet` and `$provider` variables; the fix there used `builder.sel(...)` for that one panel only. Every other query on the node-metrics tab (deploy/grafana/gen/tabs/nodemetrics.py: the raw tailscaled_* throughput, packet and drop panels, the advertised/approved route, health-message, DERP home region and endpoint tables, the curated tailscale_node_* path/packet/drop/peer-relay panels, and the node-up and discovery stats) still uses bare metric names, so on a stack holding more than one tailnet or exporter the tab shows the sum of all of them while the selector at the top says otherwise. Loop23 left this out of scope deliberately rather than widen its single repair cycle.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Every Prometheus query on the node-metrics tab filters on both `tailscale_tailnet=~"$tailnet"` and `tailscale2otel_provider=~"$provider"` (via `builder.sel` or `TNP`), including queries built through `lot(...)`, `derp_byte_fraction(...)` and the drop-ratio scalar expression; a Python generator test fails if any node-metrics query omits either filter
- [ ] #2 Before the change, a promtool unit test over a synthetic multi-deployment fixture shows at least one existing node-metrics panel query returning the summed value for an unselected deployment (fails); after the change the same test returns only the selected deployment (passes). Fixture values are synthetic, with no lab identifier
- [ ] #3 Each node-metrics metric family the tab queries is shown, by an existing or extended collector test, to carry the tailscale_tailnet and tailscale2otel_provider attributes, so the new filters cannot blank a panel
- [ ] #4 Panel IDs, layout, variables and alert panel links are unchanged: `just gen` twice leaves no diff after the second run, and deploy/alerts/grafana-managed/*.json are byte-unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
