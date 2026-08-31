---
id: TSO-0074
title: Include a bounded recent-log tail in the support bundle
status: Done
assignee: []
created_date: '2026-08-30 09:34'
updated_date: '2026-08-31 03:39'
labels: []
milestone: m-5
dependencies: []
priority: medium
ordinal: 77000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
internal/app/admin_bundle.go collects config/diagnostics/state but not the last N slog lines, so "what just happened" debugging needs separate log access. Add an in-memory bounded ring of recent log records (respecting existing redaction) and include its tail in the bundle.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The bundle contains a bounded, redaction-safe tail of recent process logs
- [x] #2 Ring size bounded and configurable; no PII leakage beyond existing log policy
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Root F1 freezes a bounded support-bundle log-tail size; lane G later implements the redaction-safe ring and bundle output.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Lane G asked whether support-bundle archive ownership may include internal/supportbundle/** and whether process-wide logger capture may use minimal internal/app/app.go wiring. Root decision: yes to both. The bundle entry belongs with the existing archive implementation, and a minimal composition-root logger wrapper is the narrowest coherent seam; Lane G owns those paths for this lane, with no concurrent owner.

Lane G added a configurable record-bounded, concurrency-safe, redaction-preserving process log ring and recent_logs.jsonl support-bundle entry; bundle format is now v2. Focused race tests and leak/bounds negative sentinels passed.

CodeRabbit's major bounded-memory finding was verified: the ring bounded record count but one slog record could still be arbitrarily large. Fixed capture to cap each JSONL entry at otlp.limits.log_body_bytes and replace oversized entries with a valid JSON truncation marker carrying original_bytes; live logging remains untouched. The new guard was negative-tested by removing the bound, observing a 1,091-byte record exceed the 128-byte test ceiling, then restoring and passing.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added a configurable process-wide bounded JSON log-tail ring to support bundles, preserving slog redaction and archive ordering while advancing the bundle format. Implementation SHA 6d9c23c. Final integrated just check passed at 5b55617; exact-head CI run 33354208183 completed success.
<!-- SECTION:FINAL_SUMMARY:END -->
