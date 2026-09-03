---
id: TSO-0094
title: Fail startup for network receivers missing credentials in the next major
status: Parked
assignee: []
created_date: '2026-08-31 01:09'
updated_date: '2026-09-03 21:07'
labels: []
milestone: m-11
dependencies: []
priority: medium
type: enhancement
ordinal: 95000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Wave 3 intentionally preserved v4 startup compatibility: a network-reachable streaming or webhook receiver without its token or signing secret starts but refuses every request with HTTP 403, with warnings, status visibility, and self-observability. In the next planned major, convert this condition into a Config.Validate error after the Go module path and release plan are ready for the breaking change.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Config validation rejects each enabled network-reachable streaming or webhook receiver route whose required credential is empty
- [x] #2 Credential-free loopback receiver configurations remain explicitly supported or are separately decided and documented
- [x] #3 Migration and release notes name the breaking behavior and the vNext module-path requirement is satisfied before release
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Add failing-first config validation cases for enabled network-reachable streaming and webhook receivers whose required credential is empty.
2. Settle the commissioned loopback fork narrowly, implement the validation and migration/release documentation, and regenerate config-derived artifacts through the just task surface.
3. Run focused config/receiver and generation checks, inspect the diff, and return the decision and evidence to the root for the feat! integration commit and tracker finalization.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner decision 2026-09-01: no v5 is scheduled yet. The plan is to drain the whole board first, then cut v5 in one big bang, and the owner merges that release PR by hand - never automerge. Parked into the v5 milestone as a collection point; do not implement the breaking change until the major is actually called and the Go module path has moved to /v5.

Implementation 399b67a0 plus fixture follow-up b1ed7823: Config validation now rejects enabled network-reachable legacy and routed streaming/webhook listeners when their effective token or secret is empty, including empty credential files and every configured route. Credential-free loopback receivers remain supported as the narrowest compatible v5 choice for local development and trusted local proxies; warnings retain the local-process injection risk. Migration documentation, configuration documentation, generated schema and environment reference all describe the startup failure and /v5 requirement. Failing-first credential cases returned nil under the old code. Focused race tests, generation/config/docs checks, exact-commit builds, regeneration drift check, and full just check passed. The integrated gate first exposed older fixtures that enabled wildcard listeners without credentials; b1ed7823 supplies explicit test credentials or loopback intent. CodeRabbit completed for config and the follow-up config/app fixtures with no unresolved valid findings; its /v4 claims were shard-context false positives.

Wave integration stop: pushed code head b1ed782322fc66cc9c14a5a6be09d00fe3071c68 passed exact-head CI run 33805396385 attempt 1. Release workflow 33805397230 ran release-please and updated PR #585, but the PR remains open as release 4.1.0 rather than retargeting to 5.0.0. The pushed header is feat!(config): require credentials for network receivers; the Conventional Commits breaking form is feat(config)!:, so release-please did not classify it as breaking. Goal section 8 requires stopping and reporting rather than editing release configuration. Resume only with the owner's choice of an additive, parseable breaking-metadata commit or another safe non-history-rewriting correction; then confirm PR #585 reads 5.0.0 before any lab work or task closure.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
The v5 receiver validation and documentation are implemented at 399b67a0 with test-fixture integration at b1ed7823, and all task acceptance criteria and gates are proven. The task remains Parked because PR #585 did not retarget from 4.1.0 to 5.0.0 after the malformed breaking header, triggering the mandatory Wave 9 stop.
<!-- SECTION:FINAL_SUMMARY:END -->
