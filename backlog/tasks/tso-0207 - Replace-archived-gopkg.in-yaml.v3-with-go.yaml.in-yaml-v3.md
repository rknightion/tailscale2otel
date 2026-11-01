---
id: TSO-0207
title: Replace archived gopkg.in/yaml.v3 with go.yaml.in/yaml/v3
status: Done
assignee: []
created_date: '2026-11-01T20:55:00Z'
updated_date: '2026-11-01T21:24:00Z'
labels: []
dependencies: []
priority: low
type: chore
ordinal: 209000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
gopkg.in/yaml.v3 is archived upstream but still a direct requirement (cmd/tailscale2otel/effectiveconfig.go, internal/config/duration.go, internal/config/envref.go and tests). go.yaml.in/yaml/v3 is already in the graph. Tidy every module per AGENTS.md; do not run a casual go get.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 No module requires gopkg.in/yaml.v3 directly; go mod tidy leaves no diff in all five modules
- [x] #2 Effective-config output and config decode are byte-identical against a golden covering durations, env refs and nested maps
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Loop33 YAML migration lane: golden effective-config equivalence, maintained imports and direct dependency, tidy every module, gate.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop33 landed0dac3592c2fd90a149d63f237f5f51f954e53791 in tip9f2abc6efa1283270cbc7b1774d04aa46e9cc85d. Five modules tidy idempotently, archived-library effective-config golden byte-identical, composed remote run20261101T211921Z-tailscale2otel-8f1b2d1c exit0 with full regeneration/no-diff. Mechanical dependency review exception.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Moved direct YAML use to maintained go.yaml.in/yaml/v3, preserving decoded config and complete redacted CLI output.
<!-- SECTION:FINAL_SUMMARY:END -->
