---
id: TSO-0146
title: Promotion-time ingress WAL replay blacks out the leader
status: Done
assignee: []
created_date: '2026-09-07 12:23'
updated_date: '2026-09-07 13:17'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 147000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Live incident on the lab, 2026-09-07. tailscale2otel-1 (leader) was force-evicted at 11:48; tailscale2otel-0 acquired the Lease at 11:48:01 and inherited an undrained ingress WAL left on its own PVC by its previous incarnation as leader (a standby never replays, so it had sat on that backlog for 3h48m). Replaying it took over 28 minutes, and for that whole window the exporter was completely dark: runActive blocks on ingressWAL.ReplayStartup before any collector, self-obs reporter, heartbeat or metrics server starts, so no collector had ever run, tailscale2otel.up was never emitted, and the ts2o-exporter-down rule fired on NoData with no other signal to explain it. Not one log line was written between "Successfully acquired lease" and the end of the replay.

On top of that /readyz answered 503 ingress_wal: replaying for the duration, because ingressWALFailure treats every state except disabled and ready as a component failure. The tailscale2otel-streaming and tailscale2otel-admin Services select tailscale2otel.m7kni.io/role: leader, so their EndpointSlices dropped to zero ready endpoints and all inbound HEC and webhook ingress was refused on the only pod allowed to accept it - the opposite of what TSO-0144 set out to achieve. The status page also renders the component as failed, when replaying is a normal transient.

The pending-entry gauges that would have explained the whole thing (tailscale2otel.ingress_wal.pending.entries and friends) are emitted by runIngressWALReporter, which is itself started after the blocking replay. The one signal that diagnoses the outage is gated behind the outage.

The alert rule itself is correct and needs no change - the exporter genuinely was down.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A promoted leader emits tailscale2otel.up and runs its collectors while a startup ingress-WAL drain is still running, so ts2o-exporter-down does not fire for a drain
- [x] #2 runIngressWALReporter and the other self-observability reporters start before, not after, the startup drain, so the ingress_wal pending entries and bytes gauges are exported while a backlog drains
- [x] #3 The startup drain logs at INFO when it begins (pending entries and bytes) and when it completes (duration), and at WARN when it outlasts the receiver budget (remaining entries and bytes)
- [x] #4 A drain in progress is a transient, not a failed component: componentFailureReasons excludes state replaying and the status page does not mark ingress_wal Failed for it
- [x] #5 Receiver startup waits for the startup drain but no longer waits unboundedly: the wait is capped by a fixed 30s in-process budget (ingressWALPromotionBudget, a compile-time constant with an App-field seam for tests, measured with a single time.Timer from the moment the drain begins), after which the stream and webhook listeners bind and the drain continues under the live worker
- [x] #6 Ordering and duplication are unchanged by that early bind: ingress arriving after the receivers open is appended to the same oldest-first WAL and applied after the older entries, and replay stays at-least-once as documented - no new duplicate suppression is claimed or required
- [x] #7 A permanent WAL failure known BEFORE the budget elapses (corrupt, incompatible, unowned, unsupported, closed, or an unknown persisted route) aborts startup exactly as today: the receivers never open, /readyz reports ingress_wal: failed, and the error reaches runActive's return so the process exits non-zero
- [x] #8 A permanent WAL failure that surfaces AFTER the budget already let the listeners bind cannot acknowledge data: the appender fails closed on the failed state both before and after the underlying write, so the receiver refuses the request with wal_unavailable, and /readyz reports ingress_wal: failed so the pod leaves the leader-labeled Services
- [x] #9 just check passes
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
The startup drain now runs in a goroutine (App.startIngressWAL, internal/app/ingresswal_app.go) instead of blocking runActive. Only the two receiver goroutines wait on it, via waitIngressWALOpen, and that wait is capped at ingressWALPromotionBudget (30s; App.walPromotionBudget is the test seam). Everything else - collectors, heartbeat, self-obs reporters, admin and Prometheus listeners - starts immediately.

Deliberately NOT done: cancelling the active lifecycle on a permanent WAL failure. The inline code parked on <-ctx.Done() and stayed up unready rather than exiting, and in coordination.mode=kubernetes a returning runActive propagates out of the Lease callback and ends the process, so cancelling would turn a corrupt WAL into a CrashLoopBackOff. The error is still recorded (App.walStartupFatal) and joined into runActive's return, so the exit code is unchanged; the failure is visible immediately through componentError, the failed WAL state and /readyz.

The budget introduced a window the gate cannot close: the drain can outlast the budget (receivers bind) and only THEN return a permanent error. Closed on the append path instead of by serialising the hot path - ingressWALCoordinator.appender now fails closed on the failed state both before and after the underlying Append, so a request is never acknowledged into a WAL that can no longer replay it. That also fixes a pre-existing hole: the LIVE worker could reach the failed state with the listeners already bound, and appends kept succeeding.

CodeRabbit findings addressed: the post-budget failure hole and the append/failure interleaving (both major, both now have negative-tested regression tests), plus a test assertion that could have passed on cancellation rather than drain completion. Two skipped: passing the permanent startup error to cancelActive (rejected above, with reason), and a final finding restating a change already implemented and pinned by TestIngressWALReplayingIsNotAFailedComponent.

Not changed: deploy/alerts. ts2o-exporter-down is correct - the exporter genuinely was down. IngressWALNearCapacity already covers a large backlog and will now actually be able to fire during one, because the gauges it reads are exported while a drain is in progress.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Split the ingress WAL's startup drain off the active lifecycle's critical path and stopped treating a drain in progress as a component failure.

Root cause on the lab: runActive blocked on ingressWAL.ReplayStartup ahead of every collector, the heartbeat and every self-obs reporter, so a leader promoted onto an inherited backlog emitted nothing for 28 minutes (ts2o-exporter-down fired on NoData with no other signal), while ingressWALFailure counted the normal transient replaying state as a failure and held /readyz at 503 - which emptied the leader-labeled streaming and admin EndpointSlices and refused all ingress on the only pod allowed to accept it.

Verified with go test ./internal/app -race and the full just check (exit 0). Every guard was negative-tested by reverting the fix under it: TestAppRunEmitsTelemetryWhileIngressWALReplays reproduces the outage exactly (tailscale2otel.up never emitted while replaying) against the old inline replay; TestReadyzHandler_GatesOnIngressWALLifecycle/replaying, TestReadyzHandler_StartupDrainGatesReadinessThenClears and TestIngressWALReplayingIsNotAFailedComponent all fail when replaying is put back in the failure set; TestStartIngressWAL_BudgetOpensReceiversMidDrain fails when the budget is removed; TestStartIngressWAL_PermanentFailureAfterBudgetFailsClosed, TestIngressWALCoordinator_AppendFailsClosedOnPermanentFailure and TestIngressWALCoordinator_AppendRacingPermanentFailureIsNotAcknowledged all fail when the appender's fail-closed checks are dropped. CodeRabbit run to a clean pass on the code.

Not yet deployed - the lab is still on 5.0.0-rc.42 and pod-0 was still draining when this landed.
<!-- SECTION:FINAL_SUMMARY:END -->
