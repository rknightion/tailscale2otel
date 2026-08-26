---
id: TSO-0014
title: Make optional ingestion paths easier to choose
status: To Do
assignee: []
created_date: '2026-08-26 11:02'
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
- [ ] #1 Streaming and Webhooks opens with a choice between default poll, low-latency stream, durable/backfill object storage, and event-only webhooks
- [ ] #2 Kubernetes audit is linked from primary feature navigation as an advanced optional feed, with prerequisites before configuration
- [ ] #3 Kubernetes audit, configuration, metrics, dashboards, and alert context link to each other
- [ ] #4 The authoritative compatibility matrix and no-double-counting warning remain intact
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 go build ./... && go vet ./... && go test -race ./...
- [ ] #2 golangci-lint run
- [ ] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->
