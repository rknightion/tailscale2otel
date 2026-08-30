---
id: TSO-0040
title: Workload Identity Federation as an exporter auth method
status: To Do
assignee: []
created_date: '2026-08-30 09:10'
updated_date: '2026-08-30 18:32'
labels: []
milestone: m-3
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

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
OWNER DECISION 2026-08-30: live verification is AUTHORISED. The wave may use the read-write Tailscale credentials at ~/repos/chat-personal/tailscale/.secrets/creds.local.env and may CREATE whatever the task needs to succeed — specifically the federated identity (client_id) in the Tailscale admin console and its claim-matching rules. Those creds are scope "all" (read-write) against a real tailnet, so create only what WIF needs and do not touch ACLs, devices, keys or stream config.

PRE-WAVE-3 LIVE PROBE, 2026-08-30 — the endpoint EXISTS but is UNDOCUMENTED IN THE SPEC. Do not conclude from the spec that it is absent:
- POST https://api.tailscale.com/api/v2/oauth/token-exchange with an empty body returns HTTP 400 "invalid request" (request id returned). Two invented sibling paths (/oauth/token_exchange, /tailnet/-/workload-identity) return 404 on the same probe, so the 400 is the endpoint answering, not a catch-all.
- It is in NEITHER the vendored spec/tailscale-api.json NOR the live spec fetched the same day from ?outputOpenapiSchema=true (HTTP 200, 245422 bytes). Grep for token-exchange in either returns nothing. The only "workload" hit in the vendored spec is an unrelated GCS credentials field on log streaming.
- Because it is absent from the spec, tools/apidrift and internal/oas will never see it and the contract ledger has no row for it. Decide explicitly whether it gets one.

REQUEST SHAPE (Tailscale WIF docs, not the spec): Content-Type application/x-www-form-urlencoded, body client_id=<CLIENT_ID>&jwt=<SIGNED_OIDC_JWT>. client_id is generated when a federated identity is configured in the admin console. 200 returns a short-lived API token; 401 returns {"message":"Unauthorized. Visit <admin console link> for details"}. The endpoint returns an opaque 400 for any malformed request, so it cannot be reverse-engineered by probing — build against the documented shape.

Note the docs also state native WIF support exists in the Tailscale GitHub Action, the Terraform provider and tailscale-client-go-v2, so check whether the pinned client already exposes an exchange helper before hand-rolling one (confirm with go doc per the repo rule).
<!-- SECTION:NOTES:END -->
