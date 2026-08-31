---
id: TSO-0102
title: 'Split the shared TS_WIF secret: one name, two incompatible scope requirements'
status: To Do
assignee: []
created_date: '2026-08-31 12:00'
labels:
  - needs-triage
milestone: m-9
dependencies: []
priority: high
ordinal: 103000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
secrets.TS_WIF_CLIENT_ID and TS_WIF_AUDIENCE are consumed by FOUR workflows that need MUTUALLY EXCLUSIVE identities, so whichever consumer is fixed last silently breaks the others.

Needs auth_keys + a tag, to JOIN THE TAILNET via the broker-token action:
  release-please.yml:65-66, trigger-docs-sync.yml:40-41, grafana-sync.yml:68-69
Needs all:read, to call the Tailscale API directly:
  live-contract.yml:68,101-102 - and line 161 says so outright, naming read scope (all:read)

A federated identity cannot hold both scope sets, so one secret cannot serve both groups.

Timeline, established 2026-08-31 from run history and secret metadata:
- live-contract had failed on EVERY scheduled run from at least 2026-08-22 through 2026-08-30.
- The secrets were updated 2026-08-31T03:25:21Z. live-contract succeeded at 03:25 by workflow_dispatch.
- release-please succeeded at 03:13 and has failed on every run since 03:33, with
  "unexpected error while creating authkey: Status: 403, calling actor does not have enough
  permissions to perform this function" - the tailnet join, not OpenBao and not camden.
- grafana-sync and trigger-docs-sync last succeeded at 02:54 and 03:13; both will fail on their
  next run for the same reason.

So Wave 3 lane B repaired a nine-day live-contract outage and, through one shared secret name, broke
three workflows. Neither half was wrong in isolation; the secret name is the defect.

Identities available on the tailnet (verified live):
  auth_keys + tag:gha, subject repo:rknightion/tailscale2otel:ref:refs/heads/main  <- broker candidate
  auth_keys + tag:gha, subject repo:rknightion*                                    <- shared broker
  all:read, no tags, subject repo:rknightion/tailscale2otel:ref:refs/heads/main    <- live-contract
All three broker consumers trigger only on main (plus workflow_dispatch on main), so the
repo-and-branch-pinned identity covers them and is the least-privilege choice.

Fix: give the broker path its own secret pair, leave TS_WIF_* on the all:read identity for
live-contract, and add a guard test asserting no workflow using broker-token shares a WIF secret
name with live-contract. Flipping the existing secret back is NOT the fix - it re-breaks
live-contract.

BLOCKED ON A HUMAN: writing repository Actions secrets is refused by the agent permission
classifier. The two gh secret set commands must be run by the owner.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The broker workflows and live-contract use different WIF secret names, each pointing at an identity with the scopes it actually needs
- [ ] #2 release-please, trigger-docs-sync and grafana-sync all succeed on a real run after the change
- [ ] #3 live-contract still succeeds after the change, proving the fix did not simply move the breakage
- [ ] #4 A guard test fails if a broker-token consumer and live-contract are ever pointed at the same WIF secret name
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
