---
id: TSO-0079
title: 'Env-var injection for list-valued credentials (tailnets, routes, targets)'
status: Done
assignee: []
created_date: '2026-08-30 09:35'
updated_date: '2026-08-31 03:39'
labels: []
milestone: m-6
dependencies: []
priority: medium
ordinal: 82000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
tailnets:, streaming.routes, webhook.routes and node_metrics.targets are file-only; an operator whose secret tooling only injects env vars cannot supply per-tailnet secrets. Add a name-keyed convention (e.g. TS2OTEL_TAILNET_<name>__CLIENT_SECRET) merging into matching list entries, covering at minimum per-tailnet OAuth secrets. Define precedence (env over file) consistently with the existing layering.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A per-tailnet client secret can be supplied via env with no secret in YAML
- [x] #2 Merging/precedence rules documented and tested, including the no-matching-entry case
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
F1 records that no persistent config key is needed: this task adds an environment overlay convention for existing list-valued credential fields; lane J later implements merge and precedence tests.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Lane J implemented and documented TS2OTEL_TAILNET_<NORMALIZED_NAME>__AUTH__OAUTH__CLIENT_SECRET. Environment wins over YAML; unknown or ambiguous normalized names fail Load. Red-first overlay/default tests and focused config checks passed.

Deviation: the required CodeRabbit gate was attempted three times after a green integrated just check; each run failed before analysis with a recoverable WebSocket-closed connection error and no complete line. No finding was produced or treated as clean. Root performed a full staged-diff review and proceeded to avoid letting an external review-service outage stop the unattended wave.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added name-keyed per-tailnet OAuth secret environment overlays with env-over-YAML precedence and fail-closed unknown or ambiguous matching, leaving no secret in YAML. Implementation SHA 882b4cf. Final integrated just check passed at 5b55617; exact-head CI run 33354208183 completed success.
<!-- SECTION:FINAL_SUMMARY:END -->
