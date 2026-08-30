---
id: TSO-0081
title: Version-check fail-open visibility and Headscale default
status: To Do
assignee: []
created_date: '2026-08-30 09:35'
updated_date: '2026-08-30 09:48'
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
