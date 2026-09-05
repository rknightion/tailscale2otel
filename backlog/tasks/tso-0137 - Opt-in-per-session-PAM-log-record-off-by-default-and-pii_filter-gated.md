---
id: TSO-0137
title: 'Opt-in per-session PAM log record, off by default and pii_filter-gated'
status: Done
assignee:
  - '@codex'
created_date: '2026-09-05 17:16'
updated_date: '2026-09-05 18:41'
labels: []
dependencies: []
references:
  - internal/collector/pam
  - codex/report-2026-09-04-wave12.md
priority: low
type: feature
ordinal: 138000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Wave 12 cut the PII-carrying per-session log record first under its cut order; the shipped PAM surface is bounded session metrics plus the safe configuration snapshot. The owner decided on 2026-09-05 to build it as an opt-in log record. Who accessed which PAM service, from where, with what authorization result, is the question an operator asks after a session metric moves, and the identity fields that answer it are exactly the ones the metric allowlist forbids: that is why this is a log record and why it ships off. The session semantics and the PII fence are in the PAM API reference doc (sections 5b and 6): `result` is the authorization result and never connection health, a grant-layer denial produces no row at all, recordings populate asynchronously, and field presence varies by session_type. The sessions poller already computes the accepted-session delta against the durable evidence store; the record is emitted from that path, not from a second poll.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 `collectors.pam.session_log_enabled` (default false) emits one log record per newly accepted session from the existing delta path; replaying the same page or restarting against the same evidence store emits no duplicate record
- [x] #2 Identity, address, device-name and command attributes are governed by the existing pii_filter categories (emails, user_display_names, user_ids, hostnames, tailscale_ips or external_ips by address range, command_text) rather than a PAM-only switch, and a test proves each category removes its field
- [x] #3 The emitted attribute set is asserted against an allowlist; auth_info and events[].metadata are never emitted verbatim
- [x] #4 The record carries a catalog descriptor, its docs/metrics.md row, a derived coverage disposition and a dashboard log panel, and the docs state that result is the authorization outcome and that sessions denied at the grant layer produce no record
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 13 front-loaded Codex campaign: root owns tracker, config seams, integration, gates and explicit-path commits. Four flat lanes use the frozen goal sections 0, 4, 5 and 6; A/C/D overlap phase 0, B waits for its local commit and C acceptance before option wiring. Verify focused contracts, generated stability, full gate, CodeRabbit, exact-head CI/auto-rc and read-only stdout live runs before reconciliation and terminal file report.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Frozen live-proof wording is inverted relative to the existing API: pii.Categories documents true=emitted; default config sets every category true; false disables its fields. Preserve the mandated existing pii_filter semantics rather than introduce a PAM-only reversal. Live proof will count default-on field presence and all-off removal, explicitly report this contract correction, and batch it as an owner question.

Lane C completed all focused tests without skips: tracked session fixture across two pages, replay and persisted-store restart, seven mapped PII categories, strict attribute allowlist and catalog. Root integrated the panel and derived coverage, classified four new attributes in the existing PII registry, and updated public summaries to 30 log types. Integrated CodeRabbit requested existing semconv constants in the descriptor; corrected, with a fresh review underway.

Integration found generic emitter double-filtering: source.address would reclassify custom tailnet/RFC1918 addresses and the metric service key is free-text filtered. Root froze log-only socket_name and explicit client.tailnet_ip/client.external_ip/client.port attributes, retaining the existing metric policy and option signature. A real-emitter regression failed on wrong address classification before explicit registry mappings; collector tests also cover mixed category choices. Session body describes authorization without claiming connection success.

Delivery authority correction: the existing grafana-sync rules job ran on dashboard-only pushes, which would violate this wave no-rule-write fence. Root added a read-only changed-path gate to the existing job; only alert/pruner changes or explicit manual dispatch run rule steps. Dashboard GitSync delivery remains enabled. This necessary repository-local workflow adjustment is part of the session-panel integration; no rule artifacts change.

Workflow guard validation passed actionlint and executed four cases: unchanged paths false, historical rule change true, manual dispatch true, invalid comparison exits 128 without authorizing writes. Added a just lint-workflows recipe for this validation. Full-gate attempts were interrupted for final wiring and a new review finding; neither interrupted run is counted as a pass.

Completed integrated CodeRabbit review found one minor correctness issue: an incomplete session carried duration_seconds=0 while its body said unknown. Root accepts the finding but preserves the frozen always-present bounded-field contract by using an explicit unknown string for incomplete duration, retaining numeric strings only for known completed durations.

Final verbose full gate reached gen-check with all preceding tests/lint/module checks green, then failed because generated changes had been deliberately unstaged for per-task commit splitting. gen-check compares the working tree with the index, so this was an index-preparation failure, not a second-generation drift (the hash comparison was empty). Restaged the generated outputs and reran the gate; no source fix was made.

Session logs, app caller/test, PII registry mapping, catalog, panel, derived artifacts and workflow guard delivered together in c94ba92; the frozen opt-in config seam is in 7f4cbfad. CodeRabbit completed immediately before this commit with zero findings. The full verbose gate v6 passed and a second just gen produced zero changed hashes. Tracker reconciliation is deferred until read-only live evidence is collected rather than checking live-dependent outcomes early.

Live stdout proof at c94ba92 passed two fresh-state runs. Each observed 9 existing sessions, emitted 9 records in cycle one and 0 in cycle two, with unchanged history and cursor. Default categories retained 9 emails/display names/device names/tailnet IPs/ports, 8 SSH users and 7 commands; all categories false removed every mapped identity/IP/port/command field while retaining all bounded fields. No live external-IP session existed; external-IP behavior is unit/emitter-proven only. Each run made 9 successful GETs total, including two page=1&page_size=100 session requests; non-GET and other outbound connection counts were 0. The source contract true=emit/false=remove was preserved despite reversed live wording in the goal.

Implementation SHA c94ba92dbfa47f8f99c527cb8fc7ea9a032a8f4f; opt-in seam SHA 7f4cbfad7e7c940f1e0dd98fbac16a1f5ff80fee. CI 33984156766 and Grafana Sync 33984156808 succeeded on attempt 1. Both dashboard blobs matched the far-side tree; rule install/prune/push steps were skipped. Unit, real-emitter, live 9/0 replay, allowlist, generated stability and full gate proof satisfy all criteria. Final-head auto-RC remains a run-level verification recorded in the report.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Delivered opt-in deduplicated PAM session logs with category-governed fields, safe bounded context, catalog documentation and a derived visualized panel. Both live modes emitted nine records then zero on replay. Existing true=emit/false=remove semantics are preserved; no live external-IP sample existed, so that case is unit/emitter-proven only.
<!-- SECTION:FINAL_SUMMARY:END -->
