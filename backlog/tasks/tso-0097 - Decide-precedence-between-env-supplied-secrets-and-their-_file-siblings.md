---
id: TSO-0097
title: Decide precedence between env-supplied secrets and their _file siblings
status: Done
assignee: []
created_date: '2026-08-31 10:55'
updated_date: '2026-09-01 19:10'
labels:
  - needs-triage
milestone: m-9
dependencies: []
priority: medium
ordinal: 98000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
resolveSecretFiles records a conflict whenever a secret value and its "*_file" sibling are both set, and Validate then hard-fails with "set only one, not both (value XOR file)" (internal/config/secretfile.go:108, internal/config/validate.go:979-980). applyTailnetEnvOverlays runs before that (internal/config/config.go:1911-1913), so a secret supplied through the documented TS2OTEL_ env convention collides with a client_secret_file that a chart or compose template wrote, and the process refuses to start.

This is a genuine design fork, not an obvious bug. The repo layering rule is defaults < YAML < environment, which argues env should win. The value-XOR-file rule is a deliberate guard against ambiguous credential sources, which argues the error is correct. TSO-0079 made it more reachable by expanding env injection to list-valued credentials.

Decide and document: either exclude env-overlaid entries from the conflict set so environment wins consistently with every other key, or keep the hard failure and make its message name the environment variable that caused it, so an operator can see which layer supplied the colliding value. Found by the post-Wave-3 sharded CodeRabbit pass.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The precedence between an env-supplied secret and its _file sibling is decided, implemented and documented
- [x] #2 Whichever way it resolves, the diagnostic names the specific env var or file that produced the collision
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
- Evaluate the env secret versus `_file` precedence fork against existing config contracts and security behavior.
- Choose the narrowest reversible rule, document the owner-level decision, and implement it test-first.
- Regenerate affected reference artifacts and return changed paths plus evidence without committing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
- Decision: preserve the existing hard value-XOR-file refusal. Silent precedence would make a credential source ambiguous; refusal is the narrowest reversible security choice.
- Conflict diagnostics now name the exact `TS2OTEL_*` environment variable when an environment value supplied one side and name the resolved secret-file path for the sibling, without retaining or printing credential values. Name-keyed `tailnets[]` OAuth overlays follow the same rule.
- CodeRabbit found a real adjacent coverage gap: global and per-tailnet Kubernetes-audit object-store `access_key_id_file`, `secret_access_key_file`, and `session_token_file` fields were never resolved. Existing/new regressions failed before registration and now pass for all six pairs.
- Final CodeRabbit config shard completed with 0 findings; the integrated `just check` passed.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Kept security-preserving value-XOR-file refusal and made collisions name the exact environment variable and file source without exposing values. Added the missing Kubernetes-audit object-store file-secret resolution; focused tests, final review and the full gate passed.
<!-- SECTION:FINAL_SUMMARY:END -->
