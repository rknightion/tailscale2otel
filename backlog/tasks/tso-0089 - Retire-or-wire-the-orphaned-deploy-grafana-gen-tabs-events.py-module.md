---
id: TSO-0089
title: Retire or wire the orphaned deploy/grafana/gen/tabs/events.py module
status: To Do
assignee: []
created_date: '2026-08-30 13:38'
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
- [ ] #1 tabs/events.py is either imported by dashboards.py and its panels appear in a built artifact, or the file is deleted
- [ ] #2 The stale ownership comment at tabs/health_ingestion.py:72-75 is corrected or removed, and any helper it duplicated to avoid importing events.py lives in builder.py
- [ ] #3 A generator test fails if a module under tabs/ is never imported by dashboards.py, so a future orphan is caught rather than discovered by hand
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
