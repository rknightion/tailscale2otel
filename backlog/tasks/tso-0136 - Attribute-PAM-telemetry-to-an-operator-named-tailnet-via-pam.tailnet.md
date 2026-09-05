---
id: TSO-0136
title: Attribute PAM telemetry to an operator-named tailnet via pam.tailnet
status: Done
assignee:
  - '@codex'
created_date: '2026-09-05 17:16'
updated_date: '2026-09-05 18:41'
labels: []
dependencies: []
references:
  - internal/app/collectors.go
  - internal/config/config.go
  - codex/report-2026-09-04-wave12.md
priority: medium
type: feature
ordinal: 137000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The PAM collector registers on the primary (first-configured) tailnet runtime, so every tailscale.pam.* series and PAM log record carries the primary tailnet's tailscale.tailnet attribute. Border0 is one organization per process, not one per tailnet, so in a multi-tailnet deployment the attribution is an accident of list order: the org may belong to a tailnet that is not first. Wave 12 raised this as owner question 1; the owner decided on 2026-09-05 to add an explicit `pam.tailnet` key rather than keep the implicit primary or drop the attribute. Default empty keeps today's primary-runtime behaviour so no deployment migrates. The registration site is internal/app/collectors.go (the `d.primary` gate) and the config seam is `PAMConfig` in internal/config/config.go; a new config key touches about eleven non-test files (example, schema, docs, Helm values and schema, env-var reference), all generated or root-owned.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 `pam.tailnet` (YAML and TS2OTEL_PAM__TAILNET) selects which configured tailnet runtime hosts both PAM schedules; empty keeps the primary runtime and existing deployments are unchanged
- [x] #2 A value naming no configured tailnet (tailscale.tailnet or any tailnets[].name) fails config.Validate with an error listing the configured names; a matching value is accepted in both single- and multi-tailnet mode
- [x] #3 In a two-runtime test driven through telemetrytest.Recorder, every tailscale.pam.* metric and PAM log record carries the selected tailnet's tailscale.tailnet attribute and exactly one copy of each series is emitted
- [x] #4 config.example.yaml, docs/configuration.md, the Helm values and the generated schema and env-var docs carry the key with the primary-default semantics stated
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
Phase 0 choices: validation compares only active configured runtime names (tailnets list wins over the legacy default sentinel); chart patch version bumped as required by deploy instructions. Goal requires a local phase-0 commit but also four task commits and forbids reset; preserve the phase-0 checkpoint additively rather than rewrite history. Final report will identify the extra seam commit.

Phase-0 CodeRabbit completed over four config files with one major finding requesting mandatory pam.tailnet in multi-tailnet mode. Rejected as false positive: frozen owner contract explicitly requires empty to retain the primary runtime. Focused config tests passed, including accepted single/multi names, unknown-name diagnostics, defaults and environment keys.

Root resolved the phase-0 documentation dependency cycle by taking ownership of docs/configuration.md for the whole run; Lane B will own wiring/tests only. Full gate requires the new key documented before the phase-0 commit, whereas B cannot start until that commit. This narrow ownership adjustment keeps the pre-commit gate intact. An accidental mid-run clarification was withdrawn; no answer is required or used.

Lane B Recorder acceptance passed for default-primary and explicit-second selection: 64 unique PAM metric series and one existing PAM snapshot log on the selected runtime, zero PAM telemetry on the other runtime. Selection test remains independent of the new session-log symbol so the B commit can build before C. The C option and session-record attribution proof are assigned to the following task commit.

Committed phase-0 source export built successfully. Its temporary copy inside ignored codex was nevertheless discovered by the repository module/fuzz inventory tests, causing a gate failure. Removed only that root-created source export after retaining its build result; no tracked source changed. Subsequent per-commit source exports will be removed immediately after each build so they cannot contaminate repository inventories.

Runtime selection delivered in 076ac92, with config seam in 7f4cbfad. The immediately preceding app-scoped CodeRabbit review completed with zero findings. B selection code and snapshot-based attribution tests are committed without the new session-log caller; the C caller/test remain together for the next commit.

Live two-runtime stdout proof at c94ba92: default-filter run exported 64 unique PAM metric points across 19 families; every point and all 9 session records carried the explicitly selected second-runtime tailnet. No duplicate series occurred within the export. All-off filtering reduced the PAM export to 31 points across 10 families and removed tailnet labels as expected from existing PII behavior. Exact private tailnet value is retained only in ignored mode-600 evidence.

Implementation SHA 076ac9287c79427a709893c02db0ee433b5ebd80; configuration seam SHA 7f4cbfad7e7c940f1e0dd98fbac16a1f5ff80fee. Integrated CI 33984156766 at c94ba92dbfa47f8f99c527cb8fc7ea9a032a8f4f succeeded on attempt 1. Recorder and live stdout attribution checks passed. The tracker closeout follows live acceptance; final-head workflow verification will be recorded in the terminal report.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added explicit PAM runtime selection with the unchanged empty-primary default and validation of active runtime names. Recorder and live proof each showed 64 unique PAM points on the selected runtime; no duplicate or unselected-runtime telemetry.
<!-- SECTION:FINAL_SUMMARY:END -->
