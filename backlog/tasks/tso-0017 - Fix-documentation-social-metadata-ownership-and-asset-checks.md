---
id: TSO-0017
title: Fix documentation social metadata ownership and asset checks
status: To Do
assignee: []
created_date: '2026-08-26 11:02'
labels:
  - needs-triage
  - user-friendliness
milestone: m-0
dependencies: []
references:
  - docs.toml
  - docs/.meta.yml
priority: low
type: docs
ordinal: 21000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
docs.toml and docs/.meta.yml both declare site metadata and both reference a missing assets/social-card.png. Confirm what the documentation hub consumes, remove the ambiguity, and make missing declared assets fail deterministically.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The owning documentation build has one current product description covering OTLP and Prometheus pull
- [ ] #2 Every declared social-image path resolves, or unsupported image metadata is deliberately removed
- [ ] #3 A deterministic check in the owning build path catches missing metadata assets
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 go build ./... && go vet ./... && go test -race ./...
- [ ] #2 golangci-lint run
- [ ] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->
