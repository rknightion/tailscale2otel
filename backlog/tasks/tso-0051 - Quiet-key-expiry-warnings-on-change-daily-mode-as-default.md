---
id: TSO-0051
title: 'Quiet key-expiry warnings: on-change + daily mode as default'
status: To Do
assignee: []
created_date: '2026-08-30 09:30'
labels: []
dependencies: []
priority: high
ordinal: 54000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The key-expiry WARN fires on every scrape per expiring device/key (internal/collector/devices/devices.go:1083-1094, internal/collector/keys/keys.go:252-266) - roughly 20k near-identical lines per expiring device key over a 14-day window at the 60s default interval. Design decided (owner, 2026-08-30): add an on-change + once-daily reminder mode mirroring the existing posture_log_mode: changes pattern (devices.go:207-217) and make the quiet mode the DEFAULT. Coordinate with the lifecycle-timeline task TSO-0050 so expiring-soon events do not double up.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Expiring keys/devices produce a warning on state change plus at most one daily reminder by default
- [ ] #2 The legacy every-scrape behaviour remains selectable via config
- [ ] #3 Docs/env reference regenerated for the new mode key
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
