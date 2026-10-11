---
id: TSO-0190
title: >-
  Restore exporter availability when deployed image rejects the configured
  ingress WAL key
status: To Do
assignee: []
created_date: '2026-10-11 17:22'
labels:
  - needs-triage
dependencies: []
priority: high
type: bug
ordinal: 190000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Read-only loop28 observation found both exporter replicas in CrashLoopBackOff. Previous startup logs reject ingress_wal.max_permanent_rejections as an unknown configuration key, while current source recognizes that key. This indicates a deployed image/configuration compatibility mismatch, not a dashboard query failure. Current telemetry and process-status prerequisite attestation are unavailable until availability is restored. No cluster or tailnet mutation was authorized in loop28; this follow-up is not admitted into its frozen envelope. Keep deployment identities and raw captures in ignored evidence only.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Identify the exact deployed image version and configuration revision and prove the selected image recognizes every configured key.
- [ ] #2 With explicit deployment authority, restore both replicas without weakening unknown-key validation or discarding retained ingress state.
- [ ] #3 Verify readiness, fresh exporter heartbeat/scrape telemetry, and actual configured collectors on the restored process; retain exact image digest and observation identities in ignored evidence.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
