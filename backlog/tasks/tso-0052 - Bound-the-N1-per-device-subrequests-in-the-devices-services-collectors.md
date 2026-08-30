---
id: TSO-0052
title: Bound the N+1 per-device subrequests in the devices/services collectors
status: To Do
assignee: []
created_date: '2026-08-30 09:30'
labels: []
dependencies: []
priority: high
ordinal: 55000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
collect_device_invites defaults ON (internal/config/defaults.go:183) and issues a sequential HTTP call per device inside each devices tick (internal/collector/devices/devices.go:928-933); collect_posture (devices.go:921-926) and services.collect_hosts (internal/collector/services/services.go:174-186) share the shape. On a large tailnet that is thousands of sequential round-trips per tick with no concurrency pool, and a slow API day makes the tick overrun its own interval. Add bounded concurrency and/or an independent longer interval for the subrequest families; consider flipping the invites default off above a fleet-size threshold. Respect the tsapi rate-limit/retry behaviour when parallelizing.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Per-device subrequests run under a bounded concurrency pool and/or their own interval
- [ ] #2 A tick can no longer overrun its interval solely because of sequential subrequests (test or measured evidence)
- [ ] #3 Default posture for large fleets is decided and documented
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
