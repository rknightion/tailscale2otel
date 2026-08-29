---
id: TSO-0027
title: >-
  Make each generated artifact its own just recipe and retire the script's
  target dispatch
status: Done
assignee:
  - '@claude'
created_date: '2026-08-29 12:44'
updated_date: '2026-08-29 17:51'
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
- [x] #1 just --list shows one discoverable recipe per generated artifact family, each in [group('gen')] with a # doc comment naming what it regenerates
- [x] #2 just gen still regenerates every artifact, and gen-check plus helm-gen-check compose the individual recipes rather than re-listing generators
- [x] #3 The helm tool-version check still SKIPs loudly instead of writing an artifact from a wrong tool version, proven by pointing PATH at a deliberately mismatched helm-docs and observing a skip rather than a modified file
- [x] #4 A missing tool is still a loud SKIP and never blocks a commit, proven for at least one generator by removing it from PATH and watching the hook exit 0
- [x] #5 .githooks/pre-commit still regenerates only the artifacts its staged changes touch, still counts deletions, and still contributes both paths of a rename; proven by staging a deletion and a rename out of an input directory
- [x] #6 scripts/cloud-environment-setup.sh still works with no just on PATH
- [x] #7 The chosen end state for the script's case dispatch (retained thin, or reduced to a shared tool-version guard) is recorded in the task with its reason
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
## Open question RESOLVED: the script KEEPS a dispatch, but reduced to a 1:1 name->function lookup with no composites and no default

Chosen end state — `scripts/regen-generated.sh` stays the program that knows how to build each artifact and how to skip safely; what was retired is its *decision-making*:

- `main()` no longer has a twelve-way `case`. A target resolves reflectively to the `regen_<target>` function (dashes -> underscores) via `declare -F`, so the function names ARE the target list and there is nothing to forget to extend (the TSO-0026 failure mode). `known_targets()` derives the usage text from the same source.
- The COMPOSITES moved to the justfile as recipe dependencies: `gen-all` (private) lists every family in order, `gen-helm` depends on `gen-helm-docs gen-helm-schema`. The script no longer defines what "everything" or "helm" means and takes no default target (bare invocation prints usage, exit 2).
- Every target resolves BEFORE any runs, so a typo in the second argument cannot leave the first half-applied. Previously `exit 2` fired mid-loop.

### Why not the other end (recipes calling generators directly, script shrunk to a tool-version guard)

Three reasons, in order of weight:

1. **The repo's own CI contract test forbids it.** `internal/ci/workflowcontract_test.go:636` (`TestGeneratedGrafanaArtifactsAreDriftGated`) expands the `gen-check` recipe chain and asserts the body contains the literal `regen-generated.sh dashboards`, with the message "the CI gate and the local command must be the same code path, or one of them drifts". Moving the generators into recipes fails that test, and `internal/` was out of scope for this task.
2. **The SKIP semantics are shared by ten generators.** `have_tool`/`version_of` plus five `command -v go|python3` guards would have to be copy-pasted into each recipe, or re-expressed as a guard script each recipe has to remember to consult. That is exactly how the version-pin check gets lost silently, which the task named as the risk.
3. **Fleet standard.** `justfile-task-surface` says absorb thin wrappers and keep real programs as files. `regen-generated.sh` is a real program (pinned-version verification, ldflag install, skip-vs-fail policy), not a wrapper — the task itself said so.

### What the task actually diagnosed, and what fixed it

The complaint was that the target list was invisible, not that the generators lived in a script. Moving the LIST is what fixes it: `just --list` now shows twelve `[gen]` rows, each with a doc comment naming the artifact, and an unknown target fails as `error: justfile does not contain recipe \`gen-bogus\`` instead of the script's opaque exit 2.

### Backwards compatibility (deliberate, not accidental)

`just gen <target>...` still works and forwards each target to `just gen-<target>`. It is kept because ~15 tracked files outside this task's scope print that spelling in failure messages and docs — `internal/app/shutdownbudget_test.go`, `internal/config/envref_test.go`, `internal/catalog/capability_counts_test.go`, `scripts/check_doc_commands.py`, `scripts/check-capability-counts.py`, `deploy/alerts/gen/build_rules.py`, `deploy/alerts/gen/test_rules.py`, `deploy/CLAUDE.md`, `deploy/alerts/README.md`, `docs/alerts.md`, `docs/env-vars.md`, `docs/alert-profiles.md`, `README.md`. All eleven spellings verified working (`helm`, `dashboards promrules counts`, `metrics`, `envref`, `config-schema`, `counts`, `coverage`, `promrules`, `all`, `tools`, bare).

`scripts/cloud-environment-setup.sh` still calls `scripts/regen-generated.sh tools` directly and must: that environment installs no `just`.

## Verification evidence (all run locally, output observed)

**AC1 — one discoverable recipe per family.** `just --list` now shows a `[gen]` block of 12 rows:
`gen`, `gen-tools`, `gen-helm`, `gen-helm-docs`, `gen-helm-schema`, `gen-config-schema`,
`gen-metrics`, `gen-envref`, `gen-coverage`, `gen-dashboards`, `gen-promrules`, `gen-counts`.
`just --dump --dump-format json` audited programmatically: every one carries `attributes:[{group:gen}]`
and a non-empty `doc`; across the WHOLE justfile there are now zero recipes missing a doc comment or
carrying anything but exactly one of the six allowed groups. `gen-all` is `[private]`, so it does not
clutter the list but `just gen all` still resolves.

**AC2 — composition, not re-listing.** `gen-check: gen-dashboards gen-promrules gen-counts` and
`helm-gen-check: gen-helm`; neither body names a generator any more, only the git-diff assertion.
`gen-all` composes the eight families in dependency order (gen-coverage last). All eleven legacy
spellings verified exit 0 with the artifacts a fixed point afterwards: `just gen` (bare), and
`just gen` with `helm` / `dashboards promrules counts` / `metrics` / `envref` / `config-schema` /
`counts` / `coverage` / `promrules` / `all` / `tools`.

**AC3 — version-pin SKIP survives.** A fake `helm-docs` was put first on PATH reporting
`helm-docs version 1.14.1` and writing `CORRUPTED BY A WRONG-VERSION helm-docs` into the chart README
if it were ever invoked for generation. `just gen-helm-docs` printed
`regen: SKIP helm-docs is 1.14.1 but CI uses 1.14.2 -> not regenerated (its output would differ). Fix: just gen-tools`,
exited 0, and README.md's sha was byte-identical before and after (`be12c8f3...`), with `git status`
clean for that path. Repeated through `.githooks/pre-commit` with a staged `values.yaml` change: same
SKIP, README unmodified, hook exit 0.

**AC4 — a missing tool is a SKIP, never a block.** Two generators, both through the hook:
(a) PATH with `~/go/bin` removed so `helm-values-schema-json` is absent, staged `values.yaml` ->
`regen: SKIP helm-values-schema-json not installed`, hook exit 0, values.schema.json sha unchanged.
(b) PATH with no Go toolchain at all, staged `internal/config/duration.go` ->
`SKIP go not installed` for both `gen-metrics` and `gen-config-schema`, hook exit 0, both artifacts
sha-unchanged.

**AC5 — hook target selection.** Run in an isolated clone so the shared git index was never touched.
- staged DELETION `D deploy/helm/tailscale2otel/README.md.gotmpl` -> selected `gen-helm`, exit 0.
- RENAME OUT `R100 config.example.yaml -> config.renamed.yaml` (only the OLD path matches) ->
  selected `gen-envref gen-config-schema`, exit 0.
- RENAME OUT `R100 internal/catalog/signal_dispositions.json -> internal/catalog/archived_dispositions.json`
  -> selected `gen-coverage`, exit 0.
- RENAME IN `R100 LICENSE -> internal/config/LICENSE.txt` (only the NEW path matches) ->
  selected `gen-config-schema`, exit 0. Both halves of a rename therefore still contribute.
- an unrelated staged path (`M LICENSE`) -> silent no-op, exit 0.
- real end-to-end, no shim: staged `config.example.yaml` -> ran `gen-envref gen-config-schema` for
  real, re-staged, and left `docs/metrics.md` and the chart README untouched.

**AC6 — cloud provisioning without just.** `scripts/cloud-environment-setup.sh` still calls
`scripts/regen-generated.sh tools`; the script contains no `just` invocation (only a comment saying
why). With a PATH containing git/go/python3/coreutils but NO `just`, `scripts/regen-generated.sh tools`
installed both pinned tools and exited 0.

**Gate.** `just --fmt --check` exit 0. `just --dump --dump-format json` exit 0 (50 recipes).
`just check` ran clean through fmt-check, lint, vet, test, test-modules, test-python, tidy-check and
vuln, then failed at `gen-check` for a PRE-EXISTING reason (see below). The legs it never reached were
run separately and all passed together, exit 0: `just docs-check helm-lint helm-gen-check config-check
promql rules-check hygiene compose-check build`. `go test ./internal/ci` (the workflow/justfile
contract suite, including `TestGeneratedGrafanaArtifactsAreDriftGated`) passes.

## OUT OF SCOPE, BLOCKING DoD 1: main is already red on `just gen-check`

Commit `3843477` ("docs: repair two links that fail the hub's strict build") hand-edited the GENERATED
`docs/alert-profiles.md`, changing the `deploy/alerts/README.md` link from the generator's relative
`../deploy/alerts/README.md` to an absolute github.com URL. `deploy/alerts/gen/build_rules.py --docs-out`
still emits the relative form, so the committed file and its generator disagree.

Proven pre-existing on a PRISTINE clone of HEAD with none of this task's changes: `just gen-check`
fails with exactly that one-line diff. CI's `dashboards-drift` job runs `just gen-check`, so it is red
on main independently of this work. This is the THIRD occurrence — the commit message itself records
that `18f38cb` added the absolute URL and `45e489c` reverted it, i.e. a regeneration has already
silently undone the hand-edit once.

The durable fix is a one-line change in `deploy/alerts/gen/build_rules.py` to emit the absolute URL, so
the generator and the hub's strict build agree. `deploy/` was explicitly out of scope for this task, so
`docs/alert-profiles.md` was left byte-identical to HEAD and DoD 1 is left unchecked. Regenerating the
file to make the gate green would re-break the documentation hub for the third time — do not do that.

## Final summary

Split `just gen *targets` into one `[group('gen')]` recipe per generated-artifact family (12 rows in
`just --list`, each with a doc comment), kept `just gen` as the regenerate-everything entry point, and
made `gen-check`/`helm-gen-check` compose those recipes as dependencies instead of re-listing
generators. `scripts/regen-generated.sh` keeps the generators and the loud-SKIP guard but its
twelve-way `case` is gone: a target now resolves reflectively to its `regen_<target>` function, the
composites (`all`, `helm`) moved into the justfile, and it takes no default target.
`.githooks/pre-commit` builds recipe names and calls `just gen-helm gen-metrics ...`.
`scripts/cloud-environment-setup.sh` still calls the script directly for `tools`, which is required
because that environment has no `just`. Files changed: `justfile`, `scripts/regen-generated.sh`,
`.githooks/pre-commit`, `scripts/cloud-environment-setup.sh`, `AGENTS.md`.
<!-- SECTION:NOTES:END -->
