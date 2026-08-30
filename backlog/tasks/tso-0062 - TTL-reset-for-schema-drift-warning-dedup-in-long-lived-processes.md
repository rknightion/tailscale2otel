---
id: TSO-0062
title: TTL reset for schema-drift warning dedup in long-lived processes
status: To Do
assignee: []
created_date: '2026-08-30 09:31'
labels: []
dependencies: []
priority: low
ordinal: 65000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
internal/k8saudit/processor.go:311-337 caps distinct schema-drift warnings at 128 for the process lifetime (same pattern in internal/audit); a months-old process goes permanently quiet on NEW drift - the metric still counts but the diagnosable log stops. Add a daily reset or TTL so the log signal stays alive without reopening the original flood the cap prevents.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 New drift observed after the cap/TTL boundary logs again in a long-lived process
- [ ] #2 The original flood-protection property is preserved (bounded distinct warnings per window)
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
