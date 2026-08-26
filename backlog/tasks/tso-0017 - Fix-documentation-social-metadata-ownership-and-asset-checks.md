---
id: TSO-0017
title: Fix documentation social metadata ownership and asset checks
status: Parked
assignee:
  - '@codex'
created_date: '2026-08-26 11:02'
updated_date: '2026-08-26 16:58'
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
- [x] #1 The owning documentation build has one current product description covering OTLP and Prometheus pull
- [x] #2 Every declared social-image path resolves, or unsupported image metadata is deliberately removed
- [x] #3 A deterministic check in the owning build path catches missing metadata assets
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 go build ./... && go vet ./... && go test -race ./...
- [x] #2 golangci-lint run
- [x] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Resolve docs metadata ownership and add deterministic missing-asset validation in the existing docs build checker; return the root-owned description block.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Documentation metadata ownership and deterministic asset validation are committed in bundled pause snapshot 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522; checker tests include missing, ignored, and commented declarations. Resume with exact-head CI.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Metadata ownership and social-asset validation are committed in 2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522; parked pending exact-head CI.
<!-- SECTION:FINAL_SUMMARY:END -->
