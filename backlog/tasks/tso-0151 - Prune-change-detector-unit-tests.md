---
id: TSO-0151
title: Prune change-detector unit tests
status: To Do
assignee: []
created_date: '2026-09-25 08:05'
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
- [ ] #1 Each listed candidate is deleted, consolidated or kept with a one-line reason in the notes
- [ ] #2 Other tests in the same pattern found during the work are handled the same way
- [ ] #3 The repo's check recipe passes
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
