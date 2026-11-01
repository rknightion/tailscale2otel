---
id: TSO-0207
title: Replace archived gopkg.in/yaml.v3 with go.yaml.in/yaml/v3
status: To Do
assignee: []
created_date: '2026-11-01T20:55:00Z'
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
- [ ] #1 No module requires gopkg.in/yaml.v3 directly; go mod tidy leaves no diff in all five modules
- [ ] #2 Effective-config output and config decode are byte-identical against a golden covering durations, env refs and nested maps
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
