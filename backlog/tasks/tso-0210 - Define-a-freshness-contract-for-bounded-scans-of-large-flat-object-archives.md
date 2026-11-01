---
id: TSO-0210
title: Define a freshness contract for bounded scans of large flat object archives
status: To Do
assignee: []
created_date: '2026-11-01T21:45:00Z'
labels:
  - objectstore
  - design
  - follow-up
dependencies: []
references:
  - >-
    /Users/rob/repos/tailscale2otel/internal/collector/objectstore/objectstore.go
  - /Users/rob/repos/tailscale2otel/internal/collector/objectstore/key.go
priority: low
type: spike
ordinal: 212000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A loop33 review of the shared log horizon identified a pre-existing flat-layout performance limitation. Bounded metadata listing happens before timestamp filtering, so expired keys can occupy listing pages and delay discovery of eligible current objects in large archives. Existing key parsing derives timestamps from final path components; whole-key chronological ordering is not guaranteed. Continuation and completed-walk wrapping intentionally discover late keys before the previous lexical position. This is not an expired-object ingestion-budget regression: the horizon admission fence excludes those candidates before GET/ingestion. A stronger discovery-freshness contract needs separate design, not an inferred timestamp seek that could omit records. Outside the approved loop33 fix set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Quantify the current discovery delay and metadata-page work using a deterministic flat-key fixture with expired keys, eligible current keys, and a late key before the prior scan position.
- [ ] #2 Document feasible bounded-freshness options and required provider or key-order capabilities, explicitly preserving late-key discovery and avoiding assumptions about whole-key chronological order.
- [ ] #3 Record a recommended source contract and its loss, latency, storage and compatibility tradeoffs; no implementation or deployment is implied by this design task.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
