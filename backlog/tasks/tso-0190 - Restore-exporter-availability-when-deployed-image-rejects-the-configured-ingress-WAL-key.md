---
id: TSO-0190
title: >-
  Restore exporter availability when deployed image rejects the configured
  ingress WAL key
status: In Progress
assignee: []
created_date: '2026-10-11 17:22'
updated_date: '2026-10-11 18:10'
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
- [x] #1 Identify the exact deployed image version and configuration revision and prove the selected image recognizes every configured key.
- [x] #2 With explicit deployment authority, restore both replicas without weakening unknown-key validation or discarding retained ingress state.
- [ ] #3 Verify readiness, fresh exporter heartbeat/scrape telemetry, and actual configured collectors on the restored process; retain exact image digest and observation identities in ignored evidence.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-10-11 main thread: cause was chart/image skew in the lab GitOps deploy. The Argo source rendered this chart from main while the image tag stayed on an older 5.2.0 rc, and the deploy values' Renovate had stopped advancing it. Main's chart writes ingress_wal.max_permanent_rejections, which that binary rejects. Proof: the older image's -validate reproduces the unknown-key error on the rendered lab config; 5.2.0-rc.35 exits 0 (placeholder env for every credentials-Secret key name). Fix in the deploy repo: chart targetRevision and image.tag pinned to the same release and moved by one Renovate dependency. Validation stays strict and ingress state was preserved (WAL drain pending_entries=0). Readback: Argo Synced/Healthy, both replicas Ready with 0 restarts, leader lease acquired, streaming/admin Services select the leader. AC #3 is partial: exporter-side families are fresh (238 vs 280 a day earlier), but the streaming-ingest families (stream records, ingest, flows, config audit, logstream) had not resumed within the first five minutes. Retained evidence is local, in ignored codex/loop29/restore-evidence.md.
<!-- SECTION:NOTES:END -->
