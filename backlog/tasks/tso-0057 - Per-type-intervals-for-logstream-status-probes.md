---
id: TSO-0057
title: Per-type intervals for logstream status probes
status: To Do
assignee: []
created_date: '2026-08-30 09:30'
labels: []
dependencies: []
priority: low
ordinal: 60000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Both the configuration and network log-stream status probes share one 600s cadence (internal/collector/logstream/logstream.go:141-166). An operator caring only about flow delivery health pays for both at the same rate. Allow per-type intervals (defaulting to the shared value).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Configuration and network probes can run on independent intervals
- [ ] #2 Existing single-interval configs keep working unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
