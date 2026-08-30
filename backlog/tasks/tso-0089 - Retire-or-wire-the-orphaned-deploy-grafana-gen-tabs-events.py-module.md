---
id: TSO-0089
title: Retire or wire the orphaned deploy/grafana/gen/tabs/events.py module
status: Done
assignee:
  - '@codex'
created_date: '2026-08-30 13:38'
updated_date: '2026-08-30 16:32'
labels:
  - needs-triage
milestone: m-2
dependencies: []
priority: medium
type: bug
ordinal: 52500
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
deploy/grafana/gen/tabs/events.py (208 lines) defines tab_events() and is imported by NOTHING. Verified 2026-08-30 at 3e9b937: dashboards.py carries 21 `from tabs.<x> import` lines for 22 non-helper tab modules, and `events` is the one missing. Nothing in build.py, dashboards.py or any generator test references tab_events; the only file mentioning the symbol is events.py itself.

Its docstring says "moved out of build.py in the module split", so it was extracted during #526 and never re-wired — or it was superseded by tabs/security_audit_trail.py and never deleted.

The cost is not just dead weight. tabs/health_ingestion.py:72-75 carries a comment treating events.py as a live, owned module ("Not importable from here: events.py is READ-ONLY to this lane"), and duplicates helper definitions verbatim to avoid reaching into it. A future lane reading that comment will believe the panels in events.py are shipping. They are not: neither generated dashboard contains them.

This also means any signal whose ONLY panel lives in events.py is invisible to operators while still being counted as covered by nothing — check whether the signal-coverage gate ever credited it (it should not, since the gate derives dispositions from the built artifacts, not from the generator source, but confirm).

Decide one way and record it: wire tab_events into the tailnet dashboard family if its panels are wanted, or delete the module and collapse health_ingestion.py:72-75 back to a normal builder import. Do not leave it as it is.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 tabs/events.py is either imported by dashboards.py and its panels appear in a built artifact, or the file is deleted
- [x] #2 The stale ownership comment at tabs/health_ingestion.py:72-75 is corrected or removed, and any helper it duplicated to avoid importing events.py lives in builder.py
- [x] #3 A generator test fails if a module under tabs/ is never imported by dashboards.py, so a future orphan is caught rather than discovered by hand
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
C1: resolve the orphaned tab with a negative-tested generator guard; complete the five TSO-0086 polish items; regenerate every affected family; return focused-check evidence without tracker writes or commits.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Latitude deviation: the run contract called for one commit per feature, but root retained the already-integrated shared-tree feature commit fa6a465 plus review-fix commit a18a5dd rather than performing prohibited destructive history surgery after integration and push. All task evidence is tied to the verified implementation head a18a5dd06f9ac9c8b84fda73bba653ded2398d5a.

Latitude deviation: lane C1 was interrupted, so root completed orphan deletion, helper relocation, and the negative-tested import guard.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Removed the orphaned events tab module, relocated its live helpers to owned modules, and added an import guard that was negative-tested against reintroduction. Verified by generator tests, final just check, and exact-head CI run 33322449434 at a18a5dd06f9ac9c8b84fda73bba653ded2398d5a (success).
<!-- SECTION:FINAL_SUMMARY:END -->
