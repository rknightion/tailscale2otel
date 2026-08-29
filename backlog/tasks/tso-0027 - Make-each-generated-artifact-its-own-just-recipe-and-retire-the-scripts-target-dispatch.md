---
id: TSO-0027
title: >-
  Make each generated artifact its own just recipe and retire the script's
  target dispatch
status: To Do
assignee: []
created_date: '2026-08-29 12:44'
updated_date: '2026-08-29 12:44'
labels:
  - needs-triage
dependencies: []
references:
  - justfile
  - scripts/regen-generated.sh
  - .githooks/pre-commit
  - scripts/cloud-environment-setup.sh
priority: low
type: enhancement
ordinal: 30000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
`just gen *targets` is a passthrough to `scripts/regen-generated.sh`, which then does its own `case` dispatch over twelve target names. Two argument-parsing layers decide the same thing, and the target list lives in the layer nobody reads.

## Why it matters

**The targets are invisible where people look for them.** `just --list` shows one row — `gen *targets` — so neither a developer nor an agent can discover that `config-schema`, `promrules`, `counts` or `coverage` exist without opening a shell script. That is precisely the failure `just --list` is meant to end, and this repo has already been bitten by a generated artifact nobody knew was regenerable: `config.schema.json` was absent from the script's own target list for months (TSO-0026) and the drift only surfaced as an unrelated-looking test failure.

Secondary: no tab completion, no per-target `#` doc comment, and an unknown target fails inside the script with `exit 2` rather than as a `just` error.

## The shape to aim at

One recipe per artifact, in `[group('gen')]`, each with a `#` doc comment, each calling the underlying generator directly. `just` already runs several recipes in one invocation (`just gen-envref gen-config-schema` works today, verified), which is what the callers need.

Keep `just gen` as the regenerate-everything entry point. Keep `gen-check` and `helm-gen-check` composing the individual recipes rather than re-listing generators.

## What must survive — this is the hard part, not the recipe split

- **The version-pin verification and its loud-SKIP semantics.** `regen-generated.sh` checks `helm-docs` and `helm-values-schema-json` against the pinned versions and SKIPs rather than writing a wrong file, and a missing tool is a skip, never a block. That behaviour is the reason the script exists; a recipe that shells straight to `helm-docs` loses it silently.
- **`.githooks/pre-commit` selects targets at runtime** from the staged paths, then invokes them. It currently builds a `targets` array and calls `just gen "${targets[@]}"`. It would build recipe names instead. Do not regress its two recent fixes: deletions count, and a rename contributes both paths.
- **`scripts/cloud-environment-setup.sh` calls `scripts/regen-generated.sh tools` directly, and must keep doing so.** It provisions an environment that does not have `just` yet. Whatever happens to the dispatch, that one entry point stays script-callable, or the cloud agent environment breaks with no local signal.
- `//go:generate` in `cmd/tailscale2otel/generate.go` runs `scripts/setup.sh`, not this script, but check it before assuming.

## Open question for whoever picks this up

Whether the script keeps its `case` dispatch at all. Two defensible ends: recipes call the script with a single target each (dispatch stays, thin), or recipes call the generators directly and the script shrinks to the tool-version guard as a shared helper. The second is cleaner and is more work, and it is the one that risks losing the SKIP semantics. Decide it explicitly and record which, rather than half-doing it.

This is ergonomics, not a defect. The current arrangement is conformant with the fleet standard — `regen-generated.sh` is a real program behind a recipe, not a thin wrapper — so there is no urgency.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 just --list shows one discoverable recipe per generated artifact family, each in [group('gen')] with a # doc comment naming what it regenerates
- [ ] #2 just gen still regenerates every artifact, and gen-check plus helm-gen-check compose the individual recipes rather than re-listing generators
- [ ] #3 The helm tool-version check still SKIPs loudly instead of writing an artifact from a wrong tool version, proven by pointing PATH at a deliberately mismatched helm-docs and observing a skip rather than a modified file
- [ ] #4 A missing tool is still a loud SKIP and never blocks a commit, proven for at least one generator by removing it from PATH and watching the hook exit 0
- [ ] #5 .githooks/pre-commit still regenerates only the artifacts its staged changes touch, still counts deletions, and still contributes both paths of a rename; proven by staging a deletion and a rename out of an input directory
- [ ] #6 scripts/cloud-environment-setup.sh still works with no just on PATH
- [ ] #7 The chosen end state for the script's case dispatch (retained thin, or reduced to a shared tool-version guard) is recorded in the task with its reason
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
