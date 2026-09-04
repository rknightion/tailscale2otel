---
id: TSO-0127
title: Alert on consecutive profiling upload failures
status: Done
assignee: []
created_date: '2026-09-04 06:35'
updated_date: '2026-09-04 08:41'
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
- [x] #1 A generated advisory non-paging rule detects more than two consecutive upload failures for 15 minutes
- [x] #2 The rule is quiet when profiling is disabled or a later upload succeeds, with executable fixtures for both cases
- [x] #3 Grafana-managed and Prometheus artifacts plus runbook documentation regenerate, deploy successfully, and verify in sync
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Done in 7841543f. ts2o-profiling-upload-failing: max(tailscale2otel_profiling_upload_consecutive_failures_ratio) > 2 for 15m, advisory, non-paging, policy optional. New runbook section profiling-upload-health, panel 'Profile upload consecutive failures'.

The threshold is low on purpose and safe because the signal is a streak, not a rate: any successful upload resets it to zero, so a sustained non-zero value means the failures are current and consecutive. The fixture that matters is the recovery case, 5 then 0, which stays silent - a rule reading this as a rate would keep firing after the outage cleared. Negative-tested individually.

The gate caught a real defect in the first draft: 'grafana.net' in an annotation is a banned tenant-specific identifier and test_annotations_carry_no_tenant_specific_identifiers failed on it. Reworded to 'a hosted target'. Live-verified in the same push as TSO-0124.
<!-- SECTION:NOTES:END -->
