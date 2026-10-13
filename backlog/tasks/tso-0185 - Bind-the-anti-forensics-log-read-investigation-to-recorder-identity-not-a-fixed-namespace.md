---
id: TSO-0185
title: >-
  Bind the anti-forensics log-read investigation to recorder identity, not a
  fixed namespace
status: Done
assignee: []
created_date: '2026-10-11 12:09'
updated_date: '2026-10-13 22:59'
labels:
  - needs-triage
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: medium
type: bug
ordinal: 185000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The current query selects any log subresource read in a fixed namespace and never restricts the recorder object identity, despite claiming recorder-specific anti-forensics. Found during TSO-0172 (comprehensive audit of both Grafana dashboards against the live stack); not admitted into loop26.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A recorder deployed in a nondefault namespace is discovered by the investigation, or the panel is explicitly relabeled as a generic namespace-log-read view.
- [x] #2 An unrelated pod log read in the same namespace is not represented as a recorder-log read.
- [x] #3 The fixture covers recorder and unrelated object identities, namespace changes, and relevant PII redaction behavior.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Relabel the panel as generic pod log read attempts across namespaces; retain PII gates and verify recorder/unrelated/nondefault/redacted contrasts.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner commissioned ordinary work on main on 2026-10-13. Root owns integration, delivery and final proof; bounded workers own disjoint source/evidence paths. Replay remains blocked.

Nine-record fixture against real Loki returns five expected generic log reads, including nondefault namespace and redacted identities. Failed-before namespace and redaction contrasts retained. Evidence: /Users/rob/repos/tailscale2otel/codex/dashboard-closeout/browser/recorder-proof/summary.json. Full just check passed on final source; generation clean; justfile formatting gate passed, no new recipes. Source commit 760fda5e7c725ae2e1f6499a77bf681bfcd49585; exact-source CI and GitSync succeeded. CodeRabbit Go shard completed with zero findings; Python shard failed No files to review and is not claimed clean.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Replaced unsupported recorder-specific claim with accurate generic log-read investigation.
<!-- SECTION:FINAL_SUMMARY:END -->
