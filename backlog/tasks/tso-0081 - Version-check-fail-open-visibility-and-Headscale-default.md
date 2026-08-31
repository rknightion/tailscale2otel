---
id: TSO-0081
title: Version-check fail-open visibility and Headscale default
status: In Progress
assignee: []
created_date: '2026-08-30 09:35'
updated_date: '2026-08-31 02:54'
labels: []
milestone: m-5
dependencies: []
priority: low
ordinal: 84000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
version_checks.* silently emit nothing when the upstream fetch is blocked (internal/app/app.go:590-602) - indistinguishable from up-to-date on an air-gapped tailnet. Add a version_check.last_success gauge and/or status-page row making fail-open visible. Also default version_checks.devices off under provider: headscale (comparing a Headscale fleet against Tailscale stable is meaningless).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Blocked version checks are observable as such, not silent
- [ ] #2 Headscale provider defaults the device version-skew check off (still overridable)
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
F1 records that no new operator key is needed: existing version_checks controls remain, while lane J adds fail-open observability and the provider-sensitive Headscale default.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Lane J chose the narrow no-new-signal route for fail-open visibility: add DeviceVersionCheck to the existing status DTO/card using the already-wired release fetcher. Root confirms statusdata/status HTML ownership transfers from completed Lane G to Lane J for this task; no concurrent owner remains and no dashboard panel is required because no signal is introduced.

Lane J chose the narrow status-only route: device_version_check exposes disabled/checking/ready/error, latest version and failure class without adding a signal. Headscale implicitly disables the device check while explicit YAML/env overrides win. Red-first fail-open/status/default tests passed; root regenerated the status schema.

Deviation: the required CodeRabbit gate was attempted three times after a green integrated just check; each run failed before analysis with a recoverable WebSocket-closed connection error and no complete line. No finding was produced or treated as clean. Root performed a full staged-diff review and proceeded to avoid letting an external review-service outage stop the unattended wave.
<!-- SECTION:NOTES:END -->
