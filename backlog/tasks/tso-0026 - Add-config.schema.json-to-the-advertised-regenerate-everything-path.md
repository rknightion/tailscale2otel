---
id: TSO-0026
title: Add config.schema.json to the advertised regenerate-everything path
status: Done
assignee: []
created_date: '2026-08-28 22:24'
updated_date: '2026-08-29 11:28'
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
- [x] #1 scripts/regen-generated.sh gains a target for config.schema.json that shells out to its -update test, is included in the all target, and is named in the script's header comment alongside the other -update artifacts
- [x] #2 The new target does not collide with the existing schema target, which regenerates the Helm values.schema.json; both are reachable by distinct names and the script's usage text distinguishes them
- [x] #3 AGENTS.md's generated-artifact table gains a config.schema.json row, and its stated family count matches the number of rows
- [x] #4 Running the all-artifacts regeneration on a tree with a deliberately stale config.schema.json makes the file correct, and go test ./internal/config then passes with no further step
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 go build ./... && go vet ./... && go test -race ./...
- [x] #2 golangci-lint run
- [x] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Landed in b203e16.

`scripts/regen-generated.sh` gains a `config-schema` target that shells out to `go test ./internal/config -run TestConfigSchemaInSync -update`, included in `all` and named in the header artifact list beside the other -update artifacts. It is deliberately NOT called `schema`: the script's existing `helm-schema` target means the CHART's `values.schema.json`, and the two filenames are one word apart, so the header, the usage text and the AGENTS.md row each say which is which.

AC #4 was proven by staling the artifact on purpose rather than by inspection: injecting a `$comment` key made `go test ./internal/config -run TestConfigSchemaInSync` fail with its regenerate hint; `just gen` then fixed it and the test passed with no further step; the result diffed byte-identical to the pre-stale file.

SCOPE ADDITION, deliberate and reported: counting the table turned up a second generated artifact in the same defect class. `docs/alert-profiles.md` is emitted by `build_rules.py --docs-out` and was drift-gated NOWHERE — `just gen-check`'s `git diff --exit-code` covered `deploy/grafana`, `deploy/alerts` and `internal/catalog/capability_counts.json`, and that path sits outside all three. It happened to be in sync, so adding it to the gate was safe rather than a red build. Fixed here because it is the same one-line hole this task exists to close; a separate task would have left a known-ungated artifact in place while the generated-artifact story was open on the desk.

So the true family count is ELEVEN, not the ten AGENTS.md claimed while listing nine rows — the count was wrong because two rows were missing, not one.

Gate: `just check` exit 0 with the new alert-profiles path in gen-check; `just --fmt --check` 0; `bash -n` and `shellcheck` clean on the script; CodeRabbit `findings: 0` across all three changed files on the rknightion plan.

Definition-of-done items carry the pre-TSO-0025 wording (this task was created before `backlog/config.yml` switched to `just check` / `just gen`), but all three are satisfied by the single `just check` run: it executes the root build, vet and race tests, `golangci-lint`, and `gen-check`, which regenerated every artifact and left no diff.
<!-- SECTION:FINAL_SUMMARY:END -->
