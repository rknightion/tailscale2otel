---
id: TSO-0098
title: >-
  Webhook router caches its tokenless and auth-mix decision across secret
  rotation
status: To Do
assignee: []
created_date: '2026-08-31 10:55'
labels:
  - needs-triage
milestone: m-9
dependencies: []
priority: high
ordinal: 99000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
NewRouter computes r.tokenless and r.invalidAuthMix ONCE at construction from route.Server.currentSecret() (internal/webhook/webhook.go:326-343). TSO-0058 made that secret hot-reloadable, so the value it was derived from now changes at runtime while the derived decision does not.

An empty-to-non-empty rotation therefore leaves the router permanently in tokenless mode: it keeps applying the loopback browser-shaped rejection path (webhook.go:391-398) instead of verifying the signature it now has. The reverse transition leaves it believing a route is signed when the secret has been emptied. Rotating between two non-empty secrets, the ordinary case, is unaffected, which is why the wave tests did not catch it.

Either evaluate tokenless and invalidAuthMix per request from the current provider value, or reject a provider whose value crosses the empty boundary at all - both are defensible, but a security decision cached across the rotation feature built to change it is not. Found by the post-Wave-3 sharded CodeRabbit pass.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Router auth state reflects the current secret after an empty-to-non-empty rotation and the reverse, proven by a test that rotates across the boundary
- [ ] #2 The chosen behaviour is documented next to the rotation feature so the interaction is discoverable
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
