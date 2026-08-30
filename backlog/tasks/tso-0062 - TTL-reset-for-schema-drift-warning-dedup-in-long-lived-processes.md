---
id: TSO-0062
title: TTL reset for schema-drift warning dedup in long-lived processes
status: Done
assignee:
  - '@codex'
created_date: '2026-08-30 09:31'
updated_date: '2026-08-30 16:32'
labels: []
milestone: m-4
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
- [x] #1 New drift observed after the cap/TTL boundary logs again in a long-lived process
- [x] #2 The original flood-protection property is preserved (bounded distinct warnings per window)
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Latitude deviation: the run contract called for one commit per feature, but root retained the already-integrated shared-tree feature commit fa6a465 plus review-fix commit a18a5dd rather than performing prohibited destructive history surgery after integration and push. All task evidence is tied to the verified implementation head a18a5dd06f9ac9c8b84fda73bba653ded2398d5a.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Changed schema-drift warning deduplication to expire after the configured TTL so long-lived processes report recurring drift without flooding. Verified by fake-clock transition tests, final just check, and exact-head CI run 33322449434 at a18a5dd06f9ac9c8b84fda73bba653ded2398d5a (success).
<!-- SECTION:FINAL_SUMMARY:END -->
