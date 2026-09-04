---
id: TSO-0131
title: >-
  Emit a coordination handover counter so leadership flapping can be alerted
  truthfully
status: To Do
assignee: []
created_date: '2026-09-04 07:30'
labels:
  - needs-triage
dependencies:
  - TSO-0130
priority: medium
type: feature
ordinal: 132000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Wave 10 rejected a leadership-flapping alert rule and was right to. The only coordination signal is tailscale2otel.coordination.leader, a last-value gauge, and changes() over a retained gauge series does not count completed handovers: it counts sample transitions in whatever the series retained, which conflates a real handover with a scrape gap, a restart, or a series appearing and disappearing.

Flapping is worth alerting on because its likeliest cause is mundane and fixable: a renew_deadline too tight for the API server's observed latency, rather than any real failure. TSO-0129 now rejects timings client-go cannot run at all, but a configuration that is legal and still marginal produces exactly this.

Owner decision 2026-09-04: add the counter, and derive it from the same Lease observation TSO-0130 builds for self-fencing rather than adding a second watcher.

Scope note. A new signal is not free in this repository: it needs a descriptor in the catalog, a docs/metrics.md regeneration, a dashboard panel to satisfy the visualized disposition, and a row in the coverage manifest. The manifest derives every disposition from the real artifacts, so the panel is what settles it and there is no value a human may assign.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A monotonic counter records completed leadership handovers, incremented from the Lease observation TSO-0130 introduces rather than a second watcher
- [ ] #2 The counter distinguishes a handover from a process restart, so a rolling deployment does not read as flapping
- [ ] #3 A flapping alert rule is generated from the counter, ships advisory and non-paging like the three Wave 10 rules, and is proved by a real gcx resources push
- [ ] #4 The signal has a dashboard panel and its coverage-manifest row is derived, not hand-assigned
- [ ] #5 The rule's threshold is justified against a measured or reasoned handover rate, not picked round
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
