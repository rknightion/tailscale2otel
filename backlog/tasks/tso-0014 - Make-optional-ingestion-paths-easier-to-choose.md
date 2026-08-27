---
id: TSO-0014
title: Make optional ingestion paths easier to choose
status: In Progress
assignee:
  - '@codex'
created_date: '2026-08-26 11:02'
updated_date: '2026-08-27 06:48'
labels:
  - needs-triage
  - user-friendliness
milestone: m-0
dependencies: []
references:
  - docs/streaming-webhooks.md
  - docs/kubernetes-audit.md
priority: medium
type: docs
ordinal: 18000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Streaming, object-store ingestion, and webhooks are accurately documented in detail, but a new operator reaches durability mechanics before a concise choice and Kubernetes audit ingestion is nearly orphaned from navigation.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Streaming and Webhooks opens with a choice between default poll, low-latency stream, durable/backfill object storage, and event-only webhooks
- [x] #2 Kubernetes audit is linked from primary feature navigation as an advanced optional feed, with prerequisites before configuration
- [x] #3 Kubernetes audit, configuration, metrics, dashboards, and alert context link to each other
- [x] #4 The authoritative compatibility matrix and no-double-counting warning remain intact
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 go build ./... && go vet ./... && go test -race ./...
- [x] #2 golangci-lint run
- [x] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Add the ingestion-path chooser and cross-links while preserving the compatibility matrix and no-double-counting warning; return any root-owned nav block.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
The ingestion chooser and cross-links are committed in bundled pause snapshot 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522; the compatibility matrix and no-double-counting warning were preserved. Resume with exact-head CI.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Optional ingestion-path guidance is committed in 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522; parked pending exact-head CI.
<!-- SECTION:FINAL_SUMMARY:END -->
