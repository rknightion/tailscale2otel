---
id: TSO-0130
title: Self-fence promptly when a live coordination Lease is deleted or replaced
status: Done
assignee:
  - '@codex'
created_date: '2026-09-04 07:02'
updated_date: '2026-09-04 12:28'
labels:
  - needs-triage
dependencies: []
priority: high
type: bug
ordinal: 131000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
client-go leader election intentionally provides no fencing. If the live Lease is deleted or replaced beneath a leader, that process can continue active collection until renew_deadline while another replica acquires the recreated Lease, creating a duplicate-active window. Current tests cover caller cancellation and draining but not deletion or conflicting replacement.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A deterministic fake-client test reproduces Lease deletion and conflicting replacement while the active callback is running
- [x] #2 The old leader cancels active collection promptly when it observes deletion, replacement, or another holder rather than waiting the full renew deadline
- [x] #3 The chosen fencing boundary is documented against client-go no-fencing semantics and does not mutate the tailnet
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Add a deterministic fake-client reproduction for Lease deletion, replacement, and another holder while the active callback is running; observe it fail for the pre-fix reason.
2. Introduce one reusable Lease observation mechanism and promptly cancel active collection on deletion, replacement, or another holder without tailnet mutation.
3. Run focused coordination tests, CodeRabbit review, the integrated gate, and the same live Lease-deletion measurement used for the baseline.
4. Document the client-go no-fencing boundary and the baseline-versus-fixed duplicate-active window.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner decision 2026-09-04: self-fence promptly. Watch the Lease and cancel active collection as soon as deletion, replacement or another holder is observed, rather than waiting out the renew deadline. Rejected: accepting the window as by-design, and detect-and-alert without fencing. The reason is that duplicate collection double-counts flow and audit logs, which is the exact failure active-passive coordination exists to prevent, so a bounded window is still a correctness bug rather than an acceptable cost.

Build the Lease observation as a reusable mechanism: the owner also commissioned TSO-0131, whose handover counter is derived from the same observations, so design one watcher that serves both rather than two.

Baseline live standby scrape on the immutable current-image sibling: 7 metric families were served: tailscale2otel_build_info_ratio, tailscale2otel_coordination_leader_ratio, tailscale2otel_export_duration_seconds_bucket/count/sum, tailscale2otel_metrics_scrape_in_flight, and target_info. No collector or per-tailnet tailscale_* family was present. The contemporaneous leader served 110 metric families including device and per-tailnet series. The standby /metrics endpoint returned successfully while its readiness probe correctly remained 503.

Baseline live demotion measurement: after the Lease holder was changed to the prior standby, the old leader returned leader=1 plus collector series for 14 completed scrapes. Scrape 15, 10.637 seconds after the Lease change, was the first demoted scrape and carried leader=0 with the collector family absent. Thus the observed demotion lag was 10.637 s at this harness cadence, and zero post-demotion scrapes leaked the former full surface.

Baseline live Lease-deletion/conflicting-replacement measurement: after deleting the live Lease and creating the replacement with the prior standby as holder, the old leader still served collector series at 0.336 s. The replacement first served collector series at 5.409 s while the old leader was still active. The old leader stopped serving at 10.799 s (its port-forward disconnected as the process exited). Directly observed duplicate-active overlap: 5.390 s, measured from first scrape with both collector surfaces to the first scrape where the old surface was gone. The replacement remained Lease holder with the expected transition count.

Baseline API-server-unavailability measurement: a temporary egress-deny NetworkPolicy selected only the live leader. Lease renewTime did not advance after the policy applied. The leader still served collector series at 0.933 s and its process/port-forward disconnected by 12.875 s; the unblocked standby then acquired the Lease and leaseTransitions advanced. The policy was deleted, its exact name read back absent, and the pod label was removed. This proves fail-closed demotion under an unreachable API server; it does not claim ServiceAccount RBAC proof through the tailnet proxy.

Lane A implementation evidence: the deterministic test was observed red first in both deletion and another-holder cases after 2.00 s because the active callback remained live waiting for client-go renew deadline. After implementation, focused tests pass for deletion, Lease UID replacement, and another-holder update. One reusable Lease observer now feeds both self-fencing and ObserveLease; it performs no Kubernetes or tailnet writes. Integration requirement discovered: the observer needs Lease list/watch in addition to the chart current get/update/create grant.

Baseline configured clock-skew observation: the live Lease was changed to a synthetic holder with acquire/renew timestamps 5.000 s ahead of the observer clocks. The incumbent failed renewal and completed shutdown 11.056 s after injection (process log). A replica became the real holder 19.641 s after injection and subsequently served coordination leader=1 plus the collector surface, yielding an 8.585 s observed no-leader interval between incumbent shutdown and new holder. The future timestamp did not add five seconds to takeover because client-go ages an unchanged record from local observation time. The harness's 200 ms active-scrape deadline was too short for a full collector gather, so no scrape-based stop timestamp is claimed for this leg; Lease and process timestamps are the evidence. The synthetic record was replaced by the normally renewing real holder without further mutation.

Post-fix live Lease-deletion/conflicting-replacement repeat on the exact-head prerelease image, using the same concurrent 200 ms scrape cadence as baseline: the former leader's metrics listener had already closed by the first completed scrape at 204 ms, so its collector surface stopped within 204 ms of replacement creation. The replacement first served collector telemetry at 6.673 s. No scrape observed both collector surfaces active; directly observed duplicate-active overlap was 0 ms, down from the 5.390 s baseline. The resulting non-overlap interval between the first old-surface-absent observation and the replacement's first active scrape was 6.469 s. The replacement remained Lease holder and the recorded transition count matched the value created by the harness.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Completed in 84776837 with additive CI/replay correction fe3c3cc6. Deterministic fake-client coverage was observed failing before the fix and now proves deletion, Lease UID replacement, another holder, and replay deduplication. One process-lifetime Lease watcher feeds both fencing and telemetry and performs no Kubernetes or tailnet writes. The exact live repeat reduced duplicate-active overlap from 5.390 s to 0 ms at a 200 ms scrape cadence; the old collector surface was gone by the first completed scrape at 204 ms. just check, just gen, and just --fmt --check pass; exact-head CI 33870727006 succeeded on attempt 1.
<!-- SECTION:FINAL_SUMMARY:END -->
