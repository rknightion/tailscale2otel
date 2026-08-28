---
id: TSO-0024
title: Support per-tag metrics port overrides in node-metrics discovery
status: Done
assignee:
  - '@codex'
created_date: '2026-08-28 18:55'
updated_date: '2026-08-28 21:05'
labels:
  - needs-triage
dependencies: []
references:
  - internal/app/nodediscovery.go
  - internal/collector/nodemetrics
  - internal/config/config.go
  - docs/node-metrics.md
  - config.example.yaml
priority: medium
type: feature
ordinal: 27000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
`node_metrics.discovery` applies ONE global `port` (default 5252) to every discovered device, so a tailnet whose nodes serve client metrics on more than one port cannot be fully covered.

The concrete case is the Tailscale Kubernetes operator. Its proxy pods are ordinary tailnet devices and are discovered normally, but they serve `/metrics` on **9002**, not 5252 — upstream `cmd/k8s-operator/sts.go` sets `TS_LOCAL_ADDR_PORT=$(POD_IP):9002`. Verified live on this project's own lab tailnet (2026-08-28): all six operator-managed nodes are discovered and every one reports `tailscale_node_up_ratio 0`, while the nine standalone `tailscaled` hosts on 5252 report 1. Raising the global port to 9002 is not an option — it would break the nine that work.

The ask is a per-tag override: a user maps a tag to one or more ports, and a device carrying that tag yields a target per port. Arbitrary port counts per tag, so a single device can produce several targets.

## Environment prerequisites, already done on the lab tailnet

These are operator-side and NOT part of this task's code, but the docs must state them and the live check depends on them:

- The proxy pods bind metrics to the pod's CLUSTER IP by default, invisible from the tailnet. `ProxyClass.spec.statefulSet.pod.tailscaleContainer.env` must set `TS_LOCAL_ADDR_PORT: "[::]:9002"`. Keep the port at 9002 — the operator hardcodes 9002 into the metrics `Service` it creates.
- The tailnet ACL must grant the scraping host's tag to the proxy tags on `tcp:9002`. A missing grant presents as a **timeout**, not a refusal, so it reads like a dead host.
- **kube-apiserver ProxyGroups cannot be scraped over the tailnet at all.** They run `cmd/k8s-proxy`, a different binary that ignores `TS_LOCAL_ADDR_PORT` and binds `POD_IP` unconditionally (`k8s-proxy.go:289`, `addr := podIP`; `cfg.GetLocalAddr()` is only the fallback when `POD_IP` is empty, and the operator always sets it). Those nodes stay `up=0` by design and the docs must say so, or the next reader will treat it as a bug in this feature.

## Design traps to resolve, not assume

- **Instance-label collision.** `nodeDiscoverer.toTarget` derives one `Instance` per device. Once a device yields several targets, `instance_source: name` and `hostname` collapse them onto a single `tailscale.node` label, merging their series and corrupting the scraper's per-series delta baselines. `disambiguateInstances` today only handles the `hostname` source and only against non-uniqueness within a batch.
- **`max_targets` semantics** shift from devices to targets once the two stop being 1:1.
- A map of tag to port list is file-only config, like `targets` — a list-valued key cannot come from `TS2OTEL_*`. Say so in the reference rather than inventing an env encoding.
- Existing configs that set no overrides must behave byte-identically to today.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A device carrying an override tag yields one scrape target per port listed for that tag, and a device carrying several override tags yields the deduplicated union of their ports in a deterministic order
- [x] #2 A device matching no override tag continues to use discovery.port exactly as today; a config with no overrides produces an identical target set to the current code
- [x] #3 Every target from a multi-target device carries a distinct identity label under each instance_source (name, hostname, address), so no two targets of one device collapse onto a single tailscale.node value
- [x] #4 max_targets caps emitted TARGETS rather than devices, and the cap is applied deterministically
- [x] #5 Validate() rejects an out-of-range port, an empty port list and a malformed tag with a specific error naming the offending key, and tools/configcheck exercises the new block
- [x] #6 docs/node-metrics.md gains a Tailscale Kubernetes operator section covering the 9002-not-5252 difference, the ProxyClass TS_LOCAL_ADDR_PORT override, the required ACL grant, the timeout-versus-refused diagnostic, and the kube-apiserver ProxyGroup being unreachable by design
- [x] #7 config.example.yaml documents the new key inline, and docs/env-vars.md plus any other generated artifact are regenerated and in sync
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 go build ./... && go vet ./... && go test -race ./...
- [x] #2 golangci-lint run
- [x] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Implement the frozen node_metrics.discovery.port_overrides schema, defaults, validation, example config, and configcheck coverage with test-first validation.
2. Expand node discovery to deterministic per-device port unions, preserve single-target labels byte-for-byte, disambiguate multi-target labels, and cap emitted targets with test-first coverage.
3. Document Kubernetes operator port 9002, ProxyClass, ACL diagnostics, kube-apiserver limitation, and the file-only override key.
4. Integrate the three disjoint lanes, regenerate root-owned artifacts, run CodeRabbit for code changes, and execute the full local and module gates.
5. Commit and push one feature commit on main, verify exact-SHA CI, deploy only to the authorized host, capture read-only before/after pull-endpoint counts, then finalize the task in one call.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Implemented and pushed feature commit e4c9e46057e8b24b60b901505fb98cbea23b49b9. Local verification passed: go build ./..., go vet ./..., go test -race ./..., golangci-lint run (0 issues), scripts/verify-modules.sh across all five modules plus artifact promqlcheck, scripts/regen-generated.sh, git diff --exit-code, and git diff --check. CodeRabbit used the organization plan and reported one Major documentation omission; the missing ProxyClass.spec.metrics.enable: true prerequisite was confirmed against current Tailscale documentation and corrected. No Info findings were dismissed. Feature-SHA CI run 33205607170 was cancelled before jobs started after it was superseded. Exact-head run 33206114889 at e86aa8fe16ba7709817edc4d9b6404a6bfa1ae20 contains the feature and remains queued, so CI is not proven green. Read-only live evidence is unchanged at 34 targets: 8 up and 26 down. The required live port_overrides YAML edit and process refresh are writes outside this run authority. Resume when that edit/restart is authorized or performed, then read the pull endpoint and prove 34 total, 12 up, 22 down, including the four containerboot proxies up.

Rob authorized the previously parked live configuration change on 2026-08-28. Resuming the narrow deployment-host lane: add the frozen three-tag port_overrides map, refresh only the tailscale2otel service, and verify the authenticated pull-endpoint counts.

Live completion (Rob-authorized, 2026-08-28): atomically added the frozen three-tag port_overrides map to the deployment-host config, validated YAML read-back (3 override keys) and `config OK` with the new binary, then recreated only the tailscale2otel service. The healthy service runs revision 8ff24304b6a42f8967741fb394f30dd5d7b4650d from image sha256:e1f93581dcff4396149011d02df61333eca74fdfda071051daf213d02874ccd5. Pull-endpoint evidence moved from 34 total / 8 up / 26 down before deployment to 34 total / 12 up / 22 down after deployment. Status-to-metrics identity joining found 6 port-9002 targets, all matched: 4 up and 2 down; this proves the four containerboot proxies recovered while the two kube-apiserver proxies remain down by design. Exact-head Release run 33206928924 succeeded. Exact-head CI run 33206928418 at 8ff24304b6a42f8967741fb394f30dd5d7b4650d completed successfully, including ci-success. The temporary rollback config backup was removed after the final stable read-back.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Delivered deterministic per-tag node-metrics port overrides, collision-free multi-target identities, target-based max_targets, validation/schema/Helm support, and operator documentation. Source and local gates are proven; the task is Parked because exact-head CI is still queued and read-only host authority prevents the live config edit/restart needed to prove the 26-to-22 down-target delta.

Completion proof: feature commit e4c9e46057e8b24b60b901505fb98cbea23b49b9 and integration/tracker head 8ff24304b6a42f8967741fb394f30dd5d7b4650d are published. Local gates, exact-head CI 33206928418, exact-head Release 33206928924, compatible image publication, configuration validation, healthy deployment, and live 34/12/22 plus port-9002 6/4/2 evidence are all proven. TSO-0024 is complete.
<!-- SECTION:FINAL_SUMMARY:END -->
