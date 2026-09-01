---
id: TSO-0094
title: Fail startup for network receivers missing credentials in the next major
status: To Do
assignee: []
created_date: '2026-08-31 01:09'
updated_date: '2026-09-01 20:02'
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
- [ ] #1 Config validation rejects each enabled network-reachable streaming or webhook receiver route whose required credential is empty
- [ ] #2 Credential-free loopback receiver configurations remain explicitly supported or are separately decided and documented
- [ ] #3 Migration and release notes name the breaking behavior and the vNext module-path requirement is satisfied before release
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner decision 2026-09-01: no v5 is scheduled yet. The plan is to drain the whole board first, then cut v5 in one big bang, and the owner merges that release PR by hand - never automerge. Parked into the v5 milestone as a collection point; do not implement the breaking change until the major is actually called and the Go module path has moved to /v5.
<!-- SECTION:NOTES:END -->
