---
id: TSO-0127
title: Alert on consecutive profiling upload failures
status: To Do
assignee: []
created_date: '2026-09-04 06:35'
labels:
  - needs-triage
dependencies: []
priority: low
type: feature
ordinal: 128000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The health dashboard visualizes tailscale2otel.profiling.upload.consecutive_failures, but no rule watches a sustained Pyroscope upload outage. The streak already resets on success and points operators toward endpoint, authentication, TLS, or rate-limit failures.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A generated advisory non-paging rule detects more than two consecutive upload failures for 15 minutes
- [ ] #2 The rule is quiet when profiling is disabled or a later upload succeeds, with executable fixtures for both cases
- [ ] #3 Grafana-managed and Prometheus artifacts plus runbook documentation regenerate, deploy successfully, and verify in sync
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
