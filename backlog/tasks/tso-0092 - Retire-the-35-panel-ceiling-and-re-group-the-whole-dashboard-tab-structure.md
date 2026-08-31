---
id: TSO-0092
title: Retire the 35-panel ceiling and re-group the whole dashboard tab structure
status: In Progress
assignee: []
created_date: '2026-08-30 18:32'
updated_date: '2026-08-31 00:28'
labels:
  - needs-triage
milestone: m-7
dependencies: []
priority: high
ordinal: 93000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
OWNER DECISION 2026-08-30: there is no panel-count ceiling. The ~35-panel guideline in deploy/grafana/gen/dashboards.py:55 and :94 is retired. Panel count per tab and per tab group does not matter; what matters is that groupings are logical and operationally meaningful and that every panel earns its place. Rows are the tool for density — a tab may carry many rows, and a row may ship COLLAPSED by default when its detail is not needed on first open.

This supersedes the sizing pressure that degraded two Wave 2 deliverables: TSO-0060 folded two WAL panels into one capacity panel and TSO-0065 folded the eviction-age signal into an existing dedup diagnostics panel, both purely to stay under 35. Both consolidations are to be reconsidered on their merits now the constraint is gone; keep a merge only where the merged panel is genuinely the better panel.

Current per-module panel counts (grep of panel( calls, 2026-08-30): health_ingestion 35, health_collection 35, nodemetrics 32, k8saudit 32, tailnet_overview 31, devices_inventory 31, network 28, health_runtime 25, health_delivery 24, health_overview 21, devices_connectivity 21, security_identity 20, cardinality 20, policy_integrations 19, policy_identity 16, security_compliance 15, policy_dns 15, security_audit_trail 14, devices_posture 13, policy_access 10, security_risk 9.

The regroup is a whole-structure pass across both dashboards, not a per-tab tweak, so it is a single-owner job and every other lane that needs a panel returns the panel spec to it rather than editing tabs/ directly.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The ~35-panel ceiling is removed from dashboards.py comments and from any doc or task text that cites it as a constraint
- [ ] #2 Both dashboards' tab and domain structure is re-grouped so each tab is a coherent operational question, with the rationale recorded on this task
- [ ] #3 Rows are used for density, with collapsed-by-default rows for detail that is not needed on first open, and the collapse decision stated per row
- [ ] #4 The two Wave 2 consolidations (TSO-0060 WAL panels, TSO-0065 eviction age) are re-examined and either split back out or kept with a stated reason
- [ ] #5 Every signal the wave adds lands on a real panel, and internal/catalog signal-coverage stays green with no empty dispositions
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
F2 lane L owns the whole dashboard regroup as a standalone delivery. It removes the retired panel-ceiling assumptions, re-examines the Wave 2 density compromises, lands logical rows/tabs, and returns a module ownership map naming the Wave 3 lane owner for every deploy/grafana/gen/tabs/*.py module. Root records that map append-only before later lanes edit assigned tabs.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
WAVE 3 SEQUENCING: this task is freeze pass F2 and runs FIRST, before any other lane produces a panel.

Deliverable is TWO things, not one:
1. The whole-structure regroup, landed as its own commit.
2. A MODULE OWNERSHIP MAP published in these notes — which lane owns which deploy/grafana/gen/tabs/*.py file for the rest of the wave, covering every lane that will add a signal.

After that commit, each lane edits its own assigned tab modules directly. builder.py, dashboards.py, maps.py and the layout stay with this task for the whole wave; a lane needing a new sentinel, helper or layout node returns that one request rather than editing shared files.

This deliberately avoids routing every panel back through the root agent. In an unattended overnight run a per-panel round trip serialises eleven lanes behind one and lands the entire signal-coverage gate at the end of the night with nobody awake to fix it.

F2 structure rationale and ownership map (effective after this commit):\n\nOperational grouping: Tailnet keeps Overview plus direct domains for Fleet operations, Network and service telemetry, Security identity and governance, and Policy and configuration. Health keeps Overview plus Data pipeline and Runtime and capacity. Nested fourth-level sub-tabs were removed; rows carry density.\n\nCollapsed-by-default rows are only investigation detail: per-device connectivity; exit-node inventory; subnet routes; DERP detail; object-store throughput and faults; per-entity subrequest fan-out; receiver loss detail; cross-source dedup; log truncation; audit latency; audit schema drift; rollup topology; raw throughput and talkers; raw node-pair talkers; flow-log stream; audit actor correlation; and the raw log explorer. First-open status and primary operational summaries stay expanded.\n\nWave 2 consolidation decisions: split ingress-WAL byte-capacity and entry-capacity views because they have different failure limits and remedies. Split dedup fill from youngest-eviction age and overlap horizon because population pressure and eviction correctness are different questions. The single ingress-WAL alert UID remains intentionally combined: its limit label distinguishes bytes from entries, and it links to the first capacity drill-down with the adjacent entry panel carrying the other view; splitting the deployed rule would create a larger live-resource migration outside F2.\n\nExclusive tab-module ownership for the rest of Wave 3:\n- Lane A: tabs/_devices_common.py, devices_inventory.py, devices_posture.py, devices_connectivity.py\n- Lane B: tabs/tailnet_overview.py\n- Lane C: tabs/network.py, nodemetrics.py\n- Lane D: tabs/security_audit_trail.py, security_risk.py, security_compliance.py, security_identity.py\n- Lane E: tabs/k8saudit.py\n- Lane F: tabs/policy_access.py, policy_dns.py\n- Lane G: tabs/policy_identity.py, policy_integrations.py\n- Lane H: tabs/health_collection.py\n- Lane I: tabs/health_ingestion.py\n- Lane J: tabs/health_delivery.py\n- Lane K: tabs/health_runtime.py, cardinality.py\n- Lane L: tabs/__init__.py plus shared deploy/grafana/gen/builder.py, dashboards.py, maps.py and layout seams.\n\nGuard evidence: the new structure, required-leaf, optional-gate, collapsed-row and separated-ingest-panel guards include explicit negative tests that inject flattened, dropped, ungated, uncollapsed or re-merged counterexamples and observe the assertions fire.

Formatting correction: the immediately preceding escaped-text block is superseded by this rendered ownership record.

F2 structure rationale and ownership map (effective after this commit):

Operational grouping: Tailnet keeps Overview plus direct domains for Fleet operations, Network and service telemetry, Security identity and governance, and Policy and configuration. Health keeps Overview plus Data pipeline and Runtime and capacity. Nested fourth-level sub-tabs were removed; rows carry density.

Collapsed-by-default rows are only investigation detail: per-device connectivity; exit-node inventory; subnet routes; DERP detail; object-store throughput and faults; per-entity subrequest fan-out; receiver loss detail; cross-source dedup; log truncation; audit latency; audit schema drift; rollup topology; raw throughput and talkers; raw node-pair talkers; flow-log stream; audit actor correlation; and the raw log explorer. First-open status and primary operational summaries stay expanded.

Wave 2 consolidation decisions: split ingress-WAL byte-capacity and entry-capacity views because they have different failure limits and remedies. Split dedup fill from youngest-eviction age and overlap horizon because population pressure and eviction correctness are different questions. The single ingress-WAL alert UID remains intentionally combined: its limit label distinguishes bytes from entries, and it links to the first capacity drill-down with the adjacent entry panel carrying the other view; splitting the deployed rule would create a larger live-resource migration outside F2.

Exclusive tab-module ownership for the rest of Wave 3:
- Lane A: tabs/_devices_common.py, devices_inventory.py, devices_posture.py, devices_connectivity.py
- Lane B: tabs/tailnet_overview.py
- Lane C: tabs/network.py, nodemetrics.py
- Lane D: tabs/security_audit_trail.py, security_risk.py, security_compliance.py, security_identity.py
- Lane E: tabs/k8saudit.py
- Lane F: tabs/policy_access.py, policy_dns.py
- Lane G: tabs/policy_identity.py, policy_integrations.py
- Lane H: tabs/health_collection.py
- Lane I: tabs/health_ingestion.py
- Lane J: tabs/health_delivery.py
- Lane K: tabs/health_runtime.py, cardinality.py
- Lane L: tabs/__init__.py plus shared deploy/grafana/gen/builder.py, dashboards.py, maps.py and layout seams.

Guard evidence: the new structure, required-leaf, optional-gate, collapsed-row and separated-ingest-panel guards include explicit negative tests that inject flattened, dropped, ungated, uncollapsed or re-merged counterexamples and observe the assertions fire.

F2 review and live-rule disposition:

CodeRabbit completed with five findings. Root fixed the two valid dashboard-copy findings: dedup fill now has a size-series prerequisite rather than the eviction-age prerequisite, and the audit metric-vs-log description now states that the metric is the classified security/lifecycle subset rather than the full log population. No tests were added for prose-only fixes; just test-python and the full just check gate passed afterward.

Three findings were left after verification: the TSO-0092 section-marker report was false (the task has exactly one NOTES begin/end pair and the ownership map is inside it); the ACL auto-approve alert correctly links to the unique Auto-approvers by kind panel at the generated ID, not the different Auto-approved exit nodes panel; and TSO-0082 already carries append-only superseding notes that define interval zero as automatic sweep cadence.

Alert manifests changed because the dashboard regroup renumbered panel IDs. The authorised real push reported 126 resources and zero errors. Direct read-back of ts2o-ingress-wal-near-capacity, ts2o-dedup-set-saturated, and ts2o-dedup-youngest-eviction confirmed their updated dashboard/panel references and live resource timestamps. Dashboards were not pushed through gcx; GitSync remains their only delivery path.
<!-- SECTION:NOTES:END -->
