---
id: TSO-0144
title: >-
  Route receiver and admin traffic by a leader pod label so standbys can be
  Ready
status: Done
assignee:
  - '@codex-root'
created_date: '2026-09-06 11:21'
updated_date: '2026-09-06 14:28'
labels: []
dependencies: []
references:
  - internal/coordination/coordination.go
  - internal/app/readiness.go
  - internal/app/coordination.go
  - deploy/helm/tailscale2otel/templates/workload.yaml
  - deploy/helm/tailscale2otel/templates/service.yaml
  - deploy/helm/tailscale2otel/templates/role.yaml
priority: high
type: feature
ordinal: 145000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Coordinated mode uses readiness as its traffic selector: a standby answers 503 on /readyz so the per-listener Services route only to the leader (TSO-0033 option A). TSO-0143 showed what that costs once the workload is a StatefulSet with per-replica claims: automatic rolling updates wait forever for a standby to become Ready, so the chart shipped updateStrategy OnDelete, which means an image tag change applied by a GitOps controller never replaces existing pods; helm --wait and --atomic can never succeed; and Argo CD built-in StatefulSet health reports Progressing for as long as readyReplicas is below replicas, which with a permanent unready standby is forever. Owner decision 2026-09-06: adopt the established active-passive pattern (Vault, Patroni, CloudNativePG): every replica that has finished startup is Ready, the leader marks its own pod with a role label the moment it holds the Lease, and the Services that must reach only the leader select that label. The label key is frozen as `tailscale2otel.m7kni.io/role` with the single value `leader`; absence means not leader. No new config key: the pod namespace comes from the mounted service account and the pod name is the coordination identity. This supersedes the not-ready standby line in the TSO-0033 design record.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 In coordinated mode a standby that has completed startup answers 200 on /readyz; the leader keeps today collector and component gating; singleton behaviour is unchanged
- [x] #2 The pod holding the Lease carries the label `tailscale2otel.m7kni.io/role=leader` within one retry period of acquiring it; a standby, a stepped-down pod and a pod that has just started never carry it (a restarting pod clears a stale label before campaigning); a pod that lacks permission to patch its own labels fails startup in coordinated mode with an error naming the missing pods patch grant
- [x] #3 In coordinated mode every per-listener Service selects the role label in addition to the selector labels, while the headless Service, the workload selector, PodMonitor, ServiceMonitor and NetworkPolicy do not; a Role and RoleBinding in the release namespace grant get and patch on pods; the StatefulSet uses RollingUpdate and no longer sets OnDelete; the singleton render stays byte-identical except for the chart version; the chart is 0.35.0; render tests cover each of these and the README documents the routing and rollout contract
- [x] #4 docs/configuration.md coordination text no longer says standbys remain unready, and the TSO-0033 record carries a note pointing at this task; just gen leaves no diff
- [x] #5 A Kind proof with two replicas shows both pods Ready, the streaming Service EndpointSlice holding only the leader pod, a helm install --wait and a helm upgrade --wait that complete, and on deletion of the leader while its identity cannot return, the label and the sole endpoint moving to the survivor within lease_duration + 2 x retry_period + election jitter (25 s at the 15s/10s/2s defaults); a replacement pod that returns under the same identity before the Lease expires may reacquire its own Lease, which counts as a pass
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 16 follows the frozen goal: phase-0 clean-head baseline and just check; parallel non-delegating A (app) and B (chart), both JUDGMENT+EXECUTION on gpt-5.6-terra/high with self-contained context; root integration, documentation, stable generation, local two-worker Kind proof, full gate and CodeRabbit, two buildable commits and exact-head CI/Helm/RC proof, then task closeout. Root owns every tracker mutation and commit. No live lab access before phase 3. Reviewed permission, readiness, rollout and failure seams against sections 0, 5, 6 and 8; frozen decisions retained.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Baseline checkout clean on main at b0c6a9fe4c4ac06c471aa99aa670a0f386c1d686, matching remote main; ownership verified as non-fork. No Kind clusters at entry. TSO-0033 already carries the required supersession note pointing to TSO-0144, so no duplicate historical note is needed.

Phase 0 just check exited 0 against baseline b0c6a9fe4c4ac06c471aa99aa670a0f386c1d686; full output retained locally at /tmp/tso-wave16-baseline.log. Both frozen lanes started after that result. Spawn calls explicitly selected gpt-5.6-terra/high with fork_turns none; exposed agent metadata confirms both running but does not expose effective model/effort, so route verification is limited to requested values. Canonical CLI document output truncates before Appendix A; read the local authoritative document remainder to verify the mapping. Existing historical TSO-0033 supersession satisfies that part of AC5.

Authority reconciliation: the launch instruction prohibits live-system access during phases 0-2, so the read-only verify-deploy call listed late in goal phase 2 is deferred to phase 3. No Grafana writes are permitted. Root Kind proof uses a locally built image with pullPolicy Never; local smoke credentials are fake. deploy/CLAUDE.md contains no readiness-as-traffic-selector statement requiring a scoped change; unrelated stale prose is left outside this wave.

Lane B returned partial after two failed render checks on differing test setup issues, both corrected before handoff; root completed the suite at 512/0, then observed a real maximum-fullname RBAC name collision. Added a regression assertion and observed 513 pass/1 expected failure, then retained the new pod-labels suffix rather than truncating it (RBAC names permit DNS subdomains). This preserves the frozen separate Role contract. Raw singleton comparisons pass for default (19473 bytes), enabled listeners (20759), and persistence/custom affinity (20169), normalizing only chart 0.34.0 to 0.35.0.

Root integration found enabled WAL initializes as replaying before leader-only replay starts; the standby readiness branch incorrectly gated on it. Added the pending-WAL case to the existing campaigning-standby test and observed HTTP 503 ingress_wal: replaying (expected failure), then excluded leader-only WAL startup gating for standbys while retaining live component failures. Leader and singleton gates remain unchanged. Kind smoke now enables WAL to cover the deployed configuration shape. Initial image invocation used goal spelling tag= as a positional recipe argument and failed invalid reference format; corrected to just image tailscale2otel:tso-0144 without changing the task surface.

Root chart acceptance complete: 514 render assertions passed; kubeconform Kubernetes 1.29.0 strict default schemas validated 10 coordinated resources with zero invalid, errors or skips; config-check and staged-output helm-gen-check passed. just gen ran twice and the second pass changed none of 1373 tracked non-tracker files. Required metric, environment, configuration schema and signal-disposition files remain untouched. App integration focused race tests passed after WAL readiness correction. Full gate runs against frozen integrated source while the final corrected local image is rebuilt and Kind is prepared.

Kind install --wait passed with two Ready replicas on different workers, two Bound claims, locally built image/pullPolicy Never, WAL enabled, and authenticated leader/standby status. The first smoke install failed before app startup because WAL rejects streaming.max_body_bytes=0; corrected fake values to 1048576, interrupted/uninstalled the local release without deleting claims, then successful install. First leader deletion recreated the same StatefulSet identity quickly and it reacquired its Lease; this did not prove survivor failover. Root temporarily cordoned both local workers for a second deletion (preventing replacement scheduling), then observed survivor Lease acquisition at 14.447096s and sole Ready endpoint plus label by 14.747s, within the 15s lease. Uncordoned both workers; deleted pod returned Ready as unlabeled standby on its original worker. One observer initially mishandled null endpoints; corrected the observer, no product code change. Final gate first attempt found one misspell comment, corrected; second gate running.

Root Kind runtime proof completed: controlled survivor acquisition at 14.447096s, labeled sole Ready endpoint observed by 14.747s; returned former leader Ready and unlabeled. Helm upgrade --wait with an opaque rolloutTrigger annotation completed and both pod UIDs changed, both Ready. The one Kind cluster created by this run was deleted; kind get clusters reports none. No live-system access has occurred. Baseline and runtime evidence are retained in ignored codex/wave16-* artifacts.

First CodeRabbit review reached terminal complete with three findings. Declined its major request to gate active work/relinquish leadership on labeling errors because that directly contradicts the frozen availability-over-routing contract. Fixed its minor clean-cancellation finding with a Run-driven regression (observed canceled startup returning an error, then fixed to stopped/nil). Fixed the valid synchronization part of its cleanup major by serializing label patches with a context-aware slot and rechecking cancellation after acquisition, so an outgoing retry cannot issue a fresh write after cleanup; retained frozen bounded best-effort cleanup rather than adding unbounded retries. Pre-review-fix full gate passed. New targeted race checks pass; second/final CodeRabbit and full gate are running. Rebuilding and repeating Kind proof for final reviewed runtime source; no live access yet.

Final outcome 2026-09-06: implementation remains uncommitted on baseline code SHA b0c6a9fe4c4ac06c471aa99aa670a0f386c1d686. Final just check exited 0 (codex/wave16-check-reviewed.log), targeted race checks passed, generation remains stable, and just --fmt --check passed. Final-image Kind install --wait and rolling upgrade --wait both passed, with both pods replaced and Ready. However, final controlled deletion acquired the Lease at 18.395790 seconds and exposed the sole Ready successor endpoint at 18.965 seconds against a 15-second lease; AC4 stays unchecked. Stock client-go v0.37.0 waits a full lease from last observed record and acquires on a jittered retry loop, so the earlier 14.447096-second transfer is not a guaranteed deletion-relative bound. Second CodeRabbit pass failed with WebSocket subscription completed unexpectedly and no complete event; stored findings are from the first pass only. Two-pass review budget exhausted; no third pass attempted. Both local Kind cluster instances were deleted, final cluster list empty. No live reads or writes, no code commits, no code RC, no phase 3. Resume: resolve the deletion-relative timing contract without silently altering frozen election/fencing, authorize a completed review of final fixes, then rerun affected proof and publish the two planned source commits before reopening phase 3.

Owner decision 2026-09-06: criterion 4 (now #5) reworded to the physical bound lease_duration + 2 x retry_period + jitter; the Kind proof measured 18.4 s to acquisition and 19.0 s to the sole Ready endpoint, inside the 25 s bound at the defaults. Published as 06522af4 (coordination + readiness + docs) and 522f6621 (chart 0.35.0); CI, Helm and auto-rc green at 522f6621; RC 5.0.0-rc.36 on the registry. CodeRabbit re-run on the final tree: complete, 0 findings across 15 files. Full local gate exit 0.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Partial, preserved locally and Parked: leader-label routing, Ready standbys including enabled WAL, separate pod RBAC and chart 0.35.0 are implemented and pass the final local gate. Four of five AC are proven; failover timing AC4 failed on the final-image proof. Final CodeRabbit completion is also unavailable after the allowed two passes. No code publication or live flip occurred.

Standbys are Ready in coordinated mode; the coordinator clears a stale role label before campaigning (Forbidden is a startup error naming the pods patch grant), sets tailscale2otel.m7kni.io/role=leader on its pod when it holds the Lease and retries while active work continues; per-listener Services select the label, a release-namespace Role grants pods get/patch, the StatefulSet is back on RollingUpdate. Verified by Run-driven fake-clientset tests with a hand negative run, 514 render assertions with a byte-identical singleton, a Kind proof with helm install/upgrade --wait and a controlled failover, and green CI/Helm/auto-rc at 522f6621.
<!-- SECTION:FINAL_SUMMARY:END -->
