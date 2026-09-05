---
id: TSO-0139
title: Expose safe log-stream destination configuration alongside delivery health
status: To Do
assignee: []
created_date: '2026-09-05 17:36'
labels:
  - needs-triage
dependencies: []
references:
  - spec/tailscale-api.json
  - internal/tsapi/logstream.go
priority: medium
type: feature
ordinal: 140000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The logstream collector reports delivery status but cannot distinguish the configured destination type from an absent or inaccessible sink. Discovery found the vendored getLogStreamingConfiguration GET unused while internal/tsapi/logstream.go only writes this configuration and reads delivery status. This is a distinct configuration surface, not a replacement for existing delivery counters. Expose only bounded configured state and destination type; the response can contain URLs and credentials.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A read-only configuration lookup reports bounded destination type and confirmed configured state for each supported log type without issuing writes.
- [ ] #2 The documented ambiguous 404 does not become a false assertion that streaming is disabled; tests distinguish successful configuration from inaccessible or unknown state.
- [ ] #3 No URL, token, credential or other unbounded destination field enters metric labels or log bodies; an allowlist test proves this.
- [ ] #4 Every adopted signal has catalog documentation, a dashboard panel and derived coverage; existing delivery-health signals remain intact.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
