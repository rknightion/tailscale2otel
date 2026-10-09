---
id: TSO-0158
title: >-
  Wire delivery-health diagnostics so export outage and recovery lines actually
  log
status: Done
assignee:
  - '@loop19'
created_date: '2026-10-09 18:11'
updated_date: '2026-10-09 19:31'
labels:
  - telemetry
  - observability
dependencies: []
priority: medium
type: bug
ordinal: 158000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
internal/telemetry/delivery.go deliveryTracker.setDiagnostics (#365) has no non-test caller on main (checked at 2a00edb4 and 54915a36), so first-failure, still-failing and recovery delivery-health log lines never print in the running exporter. Found by the loop19 TSO-0153 lane. Also: the InstallExportErrorHandler comment in internal/telemetry/selfobs.go still says the backend body is included, which is stale after TSO-0153.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Every provider's delivery tracker has its logger and emitter bound in production, proven by an app-level test that observes a first-failure and a recovery log line from a real failing then recovering exporter
- [x] #2 Those lines carry only bounded classes (TSO-0153), never backend response text
- [x] #3 The stale selfobs.go comment is corrected
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop19: loop-created; 1 implementation attempt; landed 79779879; security review approve; CodeRabbit 0 findings; composed just check green; CI green on f759c418 (79779879's own run was cancelled by a later push).
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
NewProvider binds the delivery tracker's logger (and emitter with self-obs on) for every provider before the reader starts; outage/recovery lines now print; no OTLP feedback or extra collections. Landed 79779879.
<!-- SECTION:FINAL_SUMMARY:END -->
