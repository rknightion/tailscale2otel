---
id: TSO-0181
title: Merge redundant current-utilization and overflowing-family dashboard inventory
status: Done
assignee:
  - '@loop31'
created_date: '2026-10-11 12:09'
updated_date: '2026-10-13 13:01'
labels:
  - needs-triage
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: medium
type: bug
ordinal: 181000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The current utilization table and bargauge execute the same expression; overflow inventories are distributed across multiple rows answering the same affected-family question. History and discrepant overflow evidence are still actionable and should be retained. Found during TSO-0172 (comprehensive audit of both Grafana dashboards against the live stack); not admitted into loop26.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 One current-utilization presentation gives affected metric family and configured-limit fraction, alongside the retained history view.
- [x] #2 One diagnostic row reconciles self-reported overflow flags with overflow-label series and identifies affected families.
- [x] #3 Every currently covered catalog signal retains panel coverage after consolidation; record query/panel count reduction.
- [x] #4 Keep native fallback navigation if an optional visualization plugin is missing.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
loop31: implement only the frozen owner-approved seam in isolated ownership; retain exact candidate and behavior evidence, named gate where applicable; independent guarded review then root integration/CI/delivery. Read-only diagnosis has no remediation or code gate.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop27: 0 implementation attempts in this loop; held by TSO-0172 (live dashboard audit) is parked and its required landing has not occurred. Frozen goal chains cannot start; resume after that audit is accepted and landed, then preceding chain tasks settle.

loop28: 0 implementation attempts; held by TSO-0172 (live dashboard audit), whose local source proof passed but historical semantic/prerequisite acceptance remains incomplete and whose candidate did not land. Resume only after the audit is accepted and landed, then preceding frozen chain tasks settle. No candidate work started.

loop29: 0 implementation attempts; undispatched dependency park. TSO-0172 (comprehensive live dashboard audit) cannot start before 2026-10-11T21:05:15Z under its required minimum fresh window and has not landed. Resume only after that audit is accepted, landed and composed/CI/delivery green, then preceding frozen chain tasks settle. No candidate work or live call occurred.

loop30: 0 implementation attempts; read-only preflight and root inspection establish a frozen-contract blocker before mutation. Shared test_dashboard_accuracy.CardinalityAccuracyTest.test_cap_panels_use_the_exported_limit_not_a_10k_literal requires both Active series vs cap (top $topn) and Per-metric utilization (top-N) panels and queries, contradicting the required one genuine current-utilization presentation. Protected test edits cannot be ownership-amended away; no hidden/orphan duplicate, test weakening or half-land. Resume after owner permits intended consolidated panel/test contract and scope for regenerated affected alert links; predecessor must remain green. Before aggregate counts: family515 panels/631 queries, health175/242, cardinality21/27. No reduction/after count, G, review or source candidate claimed. Evidence /Users/rob/repos/tailscale2otel/codex/loop30/tso0181-preflight.md.

loop31 preparation, Rob 2026-10-13: recommission approved. Previous blocker was a protected test requiring duplicate current-utilization panels. Changed premise: retain Per-metric utilization (top-N), retire Active series vs cap (top $topn), update only the retired-title contract, preserve history, signal coverage and native fallback; generated affected alert-link ownership is included. Discriminating check: one current presentation, lower measured panel/query counts, both distinct overflow evidence sources and valid regenerated links. Lifetime implementation attempts 0 of 4; worker, retry, rescue-astra-medium, rescue-astra remain. Task remains Parked until launch admission.

loop31: 1 implementation attempt, single current native utilization table, preserved range history/native fallback/treemap/coverage and paired distinct overflow evidence. Family panels/queries515/631 to514/630, health175/242 to174/241, cardinality21/27 to20/26. Five generated alert destinations resolve unchanged titles. Local real query fixtures and green gate; full-hunk guarded review PASS. Sharded Python CodeRabbit No files defect explicitly unreviewed, not clean. Component8ce801cd049f8a0c5ef531917050e4be0d60d738 published in exact tested batch9927d3962b49aef5d13f3196c76f05cf722872b8; fresh composed gate, CI38383777876 ci-success and sync38383777852 success. Exact far-side health blob4d1d98624f64a05f8fddbd1c4655d97577649f6e matches. Helm absent by paths. Native plugin-independent fallback validated structurally, not absent-plugin runtime/browser proof.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Removed redundant current-utilization panel/query without removing signal coverage/history; paired discrepant overflow evidence and refreshed five generated alert links. Counts reduced, real queries and exact composed/CI/delivery verified. Attempts1; complete.
<!-- SECTION:FINAL_SUMMARY:END -->
