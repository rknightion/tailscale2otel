---
id: TSO-0152
title: Prune change-detector unit tests
status: Done
assignee: []
created_date: '2026-09-25 08:05'
updated_date: '2026-10-08 20:38'
labels:
  - testing
dependencies: []
ordinal: 152000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
From the 2026-09-25 fleet test-signal audit (sampled read-only). Delete or consolidate tautological tests (restating the implementation) and change-detector tests (pinning incidental text, markup, counts or internals). Keep parsing, state-machine, retry, security/PII, wire-contract and incident regression tests. Re-verify each candidate before deleting it; the list below comes from a sample and is not exhaustive. Candidates: internal/app/statushtml/statushtml_test.go:24-40 (byte-exact CSS block against a design-doc fence); internal/portservice/portservice_test.go:48-53 TestTableSize (len >= 1000 sanity check). This suite was the strongest sampled (about 80% valuable), so the task is small.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Each listed candidate is deleted, consolidated or kept with a one-line reason in the notes
- [x] #2 Other tests in the same pattern found during the work are handled the same way
- [x] #3 The repo's check recipe passes
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Deleted internal/portservice TestTableSize: TestLookupName_WellKnownPorts already proves the embedded table loads, and the scheduled IANA drift job diffs the whole CSV against upstream, so a truncated file is caught there. Kept internal/app/statushtml TestFamilyTokenBlockMatchesTheSpec: it is the deliberate cross-console design-token contract (palette changes must land in design/console-v2/implementation-spec.md and be copied to sibling consoles), not incidental markup. Same-pattern find: provider_batching_test.go asserted the OTEL_GO_X_METRIC_EXPORT_BATCH_SIZE env was restored; that env shim is gone with OTel 1.47 (sdkmetric.WithMaxExportBatchSize), so the assertion was removed alongside it.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Pruned one redundant change-detector test, kept the CSS token contract test with its reason, removed the obsolete env-restore assertion. Verified with just check (green) and go test ./internal/portservice ./internal/telemetry.
<!-- SECTION:FINAL_SUMMARY:END -->
