---
id: TSO-0040
title: Workload Identity Federation as an exporter auth method
status: Done
assignee: []
created_date: '2026-08-30 09:10'
updated_date: '2026-08-31 03:39'
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
- [x] #1 Exporter authenticates end-to-end using an externally issued OIDC token with automatic exchange/refresh
- [x] #2 Failure modes (issuer unreachable, exchange 4xx) degrade with clear diagnostics, not silent auth loss
- [x] #3 Config schema, docs and env-var reference regenerated; adversarial review performed
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Root F1 verifies and freezes the existing workload_identity config seams; lane B later implements and adversarially verifies exchange, refresh, diagnostics, workflow, and authorised live identity creation.

Treat POST /api/v2/oauth/token-exchange as a documented out-of-spec contract exception: do not add it to the generated OpenAPI operation ledger; add dedicated tests pinning the path, form-encoded client_id and jwt fields, success token response, and message-bearing error response.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
OWNER DECISION 2026-08-30: live verification is AUTHORISED. The wave may use the read-write Tailscale credentials at ~/repos/chat-personal/tailscale/.secrets/creds.local.env and may CREATE whatever the task needs to succeed — specifically the federated identity (client_id) in the Tailscale admin console and its claim-matching rules. Those creds are scope "all" (read-write) against a real tailnet, so create only what WIF needs and do not touch ACLs, devices, keys or stream config.

PRE-WAVE-3 LIVE PROBE, 2026-08-30 — the endpoint EXISTS but is UNDOCUMENTED IN THE SPEC. Do not conclude from the spec that it is absent:
- POST https://api.tailscale.com/api/v2/oauth/token-exchange with an empty body returns HTTP 400 "invalid request" (request id returned). Two invented sibling paths (/oauth/token_exchange, /tailnet/-/workload-identity) return 404 on the same probe, so the 400 is the endpoint answering, not a catch-all.
- It is in NEITHER the vendored spec/tailscale-api.json NOR the live spec fetched the same day from ?outputOpenapiSchema=true (HTTP 200, 245422 bytes). Grep for token-exchange in either returns nothing. The only "workload" hit in the vendored spec is an unrelated GCS credentials field on log streaming.
- Because it is absent from the spec, tools/apidrift and internal/oas will never see it and the contract ledger has no row for it. Decide explicitly whether it gets one.

REQUEST SHAPE (Tailscale WIF docs, not the spec): Content-Type application/x-www-form-urlencoded, body client_id=<CLIENT_ID>&jwt=<SIGNED_OIDC_JWT>. client_id is generated when a federated identity is configured in the admin console. 200 returns a short-lived API token; 401 returns {"message":"Unauthorized. Visit <admin console link> for details"}. The endpoint returns an opaque 400 for any malformed request, so it cannot be reverse-engineered by probing — build against the documented shape.

Note the docs also state native WIF support exists in the Tailscale GitHub Action, the Terraform provider and tailscale-client-go-v2, so check whether the pinned client already exposes an exchange helper before hand-rolling one (confirm with go doc per the repo rule).

LANE MAPPING, live-verified 2026-08-30 against the current API — do not re-derive.

THE FEDERATED IDENTITY IS CREATABLE BY API. There is no separate WIF management endpoint, which is why grepping the 60 spec paths for "workload" or "federated" finds nothing: it is created through the SAME endpoint as auth keys and OAuth clients.

  POST /api/v2/tailnet/{tailnet}/keys      OAuth scope required: federated_keys
  {
    "keyType": "federated",              // enum: auth | client | federated
    "description": "<= 50 alnum chars, hyphens and spaces allowed",
    "scopes": ["all:read"],              // >= 1 REQUIRED for federated
    "issuer": "https://...",             // REQUIRED
    "subject": "pattern-with-*",         // REQUIRED, * wildcards, matched against the sub claim
    "audience": "optional",              // OMIT — Tailscale generates a secure audience by default
    "customClaimRules": {"claim": "pattern*"}
  }

Listing needs federated_keys:read, and GET /keys returns federated identities only when the calling token is itself derived from one.

THE CONSTRAINT THAT DECIDES THE LANE: the spec states issuer "must be a valid and publicly reachable https:// URL". A fake local OIDC issuer therefore CANNOT be used to create the identity — only to unit-test the exchange client. The only real issuer available to this repo is GitHub Actions OIDC (https://token.actions.githubusercontent.com), mintable by any workflow with permissions: id-token: write.

So the task splits, with different acceptance bars:
- OFFLINE HALF, must complete: implement auth.method: workload_identity against the documented exchange shape (form-encoded client_id + jwt; 200 returns a short-lived token; 401 returns a message field). Cover refresh, issuer-unreachable and exchange-4xx against a fake issuer and fake exchange server. Check go doc on the pinned tailscale-client-go-v2 FIRST — Tailscale docs say native WIF support exists in that client, the GitHub Action and the Terraform provider, so an exchange helper may already exist.
- LIVE HALF: creating the identity is one authorised API call. Proving the exchange end-to-end needs a real signed OIDC JWT, so it can only run from a GitHub Actions job with id-token: write — add it as an advisory, non-PR-gating workflow shaped like live-contract.yml.

If the live half cannot finish in one run, the offline half plus a created identity plus the workflow is a complete honest delivery: check AC#1 against the integration test and say plainly that live end-to-end is pending the first scheduled run. Do not park the whole task for it.

Never record the created identity secret on the task — description and scopes only.

Root answered the contract-ledger fork: because the exchange endpoint is absent from both vendored and live OpenAPI, it stays outside the generated operation ledger and is guarded by dedicated auth-path contract tests.

CodeRabbit requested narrowing the live credential authority. Root declined that finding because it conflicts with the owner's explicit frozen authorization for this run. Live work remains limited to creating what WIF needs and excludes ACLs, devices, existing keys, and stream configuration; no credential value will enter the tracker.

Root live verification created one federated identity described as tailscale2otel live contract with scopes all:read, GitHub Actions issuer, and repository-main subject; no identifier or credential value is recorded here. Existing advisory live-contract workflow run 33353811115 completed success at d3af40f, proving wrong-audience rejection, successful OIDC exchange, and read-scope API access. Required CodeRabbit review was attempted after integrated just check passed, but the service failed before analysis with recoverable WebSocket closed and no complete status line. Root treated it as failed and performed a manual adversarial review of the wire contract, response bounds, credential redaction, error classification, and cache/refresh behavior; no blocking issue was found.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Implemented the exact two-field WIF exchange contract, token caching and refresh, bounded responses, clear issuer and 4xx diagnostics, and JWT-safe errors. Created one scoped federated identity and proved the real GitHub OIDC exchange in advisory run 33353811115. Implementation SHA 5b55617. Final integrated just check passed at 5b55617; exact-head CI run 33354208183 completed success.
<!-- SECTION:FINAL_SUMMARY:END -->
