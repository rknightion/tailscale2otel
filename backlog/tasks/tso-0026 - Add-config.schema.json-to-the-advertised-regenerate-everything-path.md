---
id: TSO-0026
title: Add config.schema.json to the advertised regenerate-everything path
status: To Do
assignee: []
created_date: '2026-08-28 22:24'
labels:
  - needs-triage
dependencies:
  - TSO-0025
references:
  - scripts/regen-generated.sh
  - internal/config/schema_test.go
  - AGENTS.md
priority: low
type: chore
ordinal: 29000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
`config.schema.json` at the repo root is a generated artifact. Its drift gate exists and works — `TestConfigSchemaInSync` runs inside the normal `go test -race ./...`, and regenerating is `go test ./internal/config -run TestConfigSchemaInSync -update`. Nothing ships broken.

What is missing is the *advertised* path. `scripts/regen-generated.sh` has no target for it — its `schema` target is the **Helm** `values.schema.json`, which is a different file — and `AGENTS.md`'s generated-artifact table says "Ten artifact families" while listing nine rows, none of them this one.

The result is this repo's signature false-pass shape: an agent follows `AGENTS.md`, runs the all-artifacts regeneration, gets a clean `git diff`, and then fails `go test` on an artifact the script never claimed to skip. TSO-0024 hit exactly this — the root had to regenerate the file by hand after the documented path reported success.

Both sibling `-update` test artifacts (`docs/env-vars.md`, `docs/signal-coverage.md`) already have `regen-generated.sh` targets that shell out to their `-update` tests, so the pattern to follow is in the file.

Sequenced after **TSO-0025**: that task's AC #4 freezes the `just check` leg list, and its AC #9/#10 rewrite the `AGENTS.md` section and `backlog/config.yml` lines that name the regeneration path. Landing this first means editing the same lines twice.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 scripts/regen-generated.sh gains a target for config.schema.json that shells out to its -update test, is included in the all target, and is named in the script's header comment alongside the other -update artifacts
- [ ] #2 The new target does not collide with the existing schema target, which regenerates the Helm values.schema.json; both are reachable by distinct names and the script's usage text distinguishes them
- [ ] #3 AGENTS.md's generated-artifact table gains a config.schema.json row, and its stated family count matches the number of rows
- [ ] #4 Running the all-artifacts regeneration on a tree with a deliberately stale config.schema.json makes the file correct, and go test ./internal/config then passes with no further step
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 go build ./... && go vet ./... && go test -race ./...
- [ ] #2 golangci-lint run
- [ ] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->
