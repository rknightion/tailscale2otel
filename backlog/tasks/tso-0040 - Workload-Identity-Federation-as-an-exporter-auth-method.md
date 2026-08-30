---
id: TSO-0040
title: Workload Identity Federation as an exporter auth method
status: To Do
assignee: []
created_date: '2026-08-30 09:10'
labels: []
dependencies: []
priority: medium
ordinal: 43000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Tailscale Workload Identity Federation (GA ~2026-02) exchanges an external OIDC token for a short-lived Tailscale API token via POST /api/v2/oauth/token-exchange. Add auth.method: workload_identity to internal/tsapi so k8s/cloud deployments need no static OAuth secret or API key. Security-surface change: gets the adversarial review tier (auth flows, token caching/refresh, failure modes when the OIDC issuer is unreachable). Config/docs/env-var reference regeneration required.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Exporter authenticates end-to-end using an externally issued OIDC token with automatic exchange/refresh
- [ ] #2 Failure modes (issuer unreachable, exchange 4xx) degrade with clear diagnostics, not silent auth loss
- [ ] #3 Config schema, docs and env-var reference regenerated; adversarial review performed
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
