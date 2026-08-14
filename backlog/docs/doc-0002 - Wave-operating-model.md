---
id: doc-0002
title: Wave operating model
type: guide
created_date: '2026-08-14 14:04'
updated_date: '2026-08-14 14:06'
---
This document carries **only what is true of tailscale2otel**. The campaign model itself — run
contract and run modes, the routing contract, authority and the thread pool, child lane briefs,
external-contract freezing, the blocker contract, the goal-file template, the run-end protocol and
the pre-flight checklist — is the *Codex fan-out protocol (canonical)* doc, and that doc wins on any
specific. Nothing here restates it. If a section below could be pasted into another repo unchanged,
it is in the wrong document.

Every rule here exists because something failed. The failure is kept with the rule; a rule without
its reason gets argued away by the next session.

---

## 1. Rules this project added

### Work lands on `main`. No branches, no worktrees.

This repo has no PR flow for its own work. Lanes commit to the shared checkout on `main`; the root
agent owns the push. The usual "branch first" reflex is overridden here, and so is any tooling
default that creates a worktree — including `/fork`, which since Claude Code 2.1.221 makes a *new
worktree* rather than sharing the checkout.

### One commit per feature on `origin/main`

Lanes may make local checkpoint commits. Before pushing, the root agent squashes them into exactly
one commit per feature with `git reset --soft origin/main`. `rebase -i` is not available in this
environment. Conventional Commits (`type(scope): subject`) is not a style preference — Renovate and
release-please parse it.

### A dispatch brief must forbid destructive git, not just `commit` and `push`

**2026-07-28: two lanes ran `git reset`/`git clean` on the shared tree and destroyed five other
lanes' uncommitted work.** Forbidding "don't commit" was not enough, because neither agent was
trying to commit. Every brief must name `git reset`, `git stash`, `git clean` and `go clean` as
prohibited on the shared tree.

The signature is distinctive: **tracked changes gone, untracked files still present.** Recovery is
`git fsck --lost-found`, and the first action is to **tag the dangling `WIP on main:` commit** before
anything else touches the object store — then restore only the lost files.

### A lane that hits a decision its brief does not cover stops and returns the question

It does not invent an answer. One round trip is cheaper than the rewrite. This is the escape hatch
for an ownership map that turns out to be wrong — a boundary with no escape hatch is a stop
condition wearing a safety label.

### Work that touches a live system stays on the root agent

Lanes do read-only investigation, code edits, tests and inventory sweeps. **SSH to the live deployment host, deploys,
Grafana pushes and any tailnet-side call stay with the root agent.** This is not only a blast-radius
rule: a dispatched lane inherits the parent's permission mode and cannot clear a soft block, because
clearing one requires a message from the user and a lane's transcript contains none. A brief is
explicitly refused as consent. A blocked lane must be run by the root agent, never re-dispatched.

### Specs and plans are never committed

They live in gitignored `docs/superpowers/`. Since the tracker landed, the queue is
`backlog task list --plain` and acceptance criteria live on the task — a plan file that re-enumerates
either one is a second source of truth that drifts.

---

## 2. Recurring defects in this codebase

These have each shipped at least once. Treat them as things to check for, not things to hope about.

### A local gate passing is not the same question as CI passing — four distinct instances

- **actionlint.** It shells out to whatever `shellcheck` is on `PATH`. Local shellcheck 0.11.0 does
  not emit SC2015 at all; the runner's older one does. A workflow edit passed locally on two
  actionlint versions and failed CI (`live-contract.yml`, 2026-07-28). The version gap runs the
  *wrong* way — local is newer and reports less — so "my tooling is current" is not reassurance.
  Prefer a plain `if` over `A && B || C` in any `run:` block.
- **`go test -race ./...` at the root is not the test suite.** There is no `go.work`, so it stops at
  the root module and never reaches the four tool modules. `tools/configcheck/go.sum` drifted 82
  lines out of tidy unnoticed (#437). Run `scripts/verify-modules.sh`.
- **promqlcheck against the module is a different question from promqlcheck against the artifacts.**
  Building and unit-testing the tool proves nothing about the dashboards and rules the repo ships.
  #526 landed **65 real failures in CI with every local gate green** for exactly this reason.
- **An offline validator written from the same assumption as its generator cannot catch that
  assumption being wrong.** `execErrState` was believed to spell its OK state `"OK"` in five places
  at once — the generator, two validators and three docs — so every offline gate agreed with itself
  and **all 19 advisory rules failed at push** with `Invalid value: "OK"` (it is `"Ok"`). `gcx
  resources validate` says outright that it does not validate the spec. **Only a real
  `gcx resources push` proves a rule is deployable**, and pushing here is pre-authorized.

### Guard tests over `.github/workflows` that pass while asserting nothing

Roughly three per campaign phase. A substring assertion matches an unrelated line or a filename; a
regression the test was written to catch gets deleted by the compiler. **Every guard test must be
negative-tested** — break the thing on purpose, watch the test go red, put it back.

### A green workflow run is not proof it published anything

`grafana-sync` used `git diff --quiet`, which sees only *tracked* files. When the dashboard artifact
was renamed into a pair, both new filenames were untracked, the check read "already matches", and
**three consecutive successful runs published nothing** (fixed in `f167a1c`: stage first, then diff
the index). Verify the far side by listing the GitSync repo tree, not by the workflow's conclusion.

### A renamed metric leaves a panel silently empty

It still loads; it just shows "No data". `internal/catalog/dashboardrefs_test.go` is the only thing
connecting the shipped artifacts to the in-code catalog. It has to subtract label and log-attribute
names too, because labels share the `tailscale_` prefix and a text scan cannot tell a metric from a
label by shape.

### The tool modules do not run the way their own help text says

`go run ./tools/metricscatalog` from the repo root **fails** — separate `go.mod`. Use
`go run -C tools/metricscatalog .` with an absolute `-file`.

### A major release breaks unless the module path moves first

release-please does not maintain the Go module path. A `vN` tag against a stale `/vN` path kills the
GoReleaser binaries job — it ate every archive at v2.0.0 (#174). Run `scripts/bump-module-major.sh`
and land it on `main` *before* merging the release PR.

Related and recurring: **generated artifacts that embed the release-managed version break the
release PR itself, and they arrive in a queue** — fixing one exposes the next. The sharpest is that
release-please's version regex has no global flag, so a line carrying the version twice (a
shields.io badge: label *and* URL) half-updates and still fails the diff.

---

## 3. Lane conventions

### Single-owner files — never two lanes, never concurrently

- `deploy/grafana/gen/build.py` and `deploy/alerts/gen/build_rules.py` — the generators. Serialize.
- `internal/app/collectors.go` and the rest of the composition root — wiring pass only.
- `internal/config/` and `config.example.yaml` — one owner; `docs/env-vars.md` is generated from the
  latter, so two lanes editing it produce a conflicting regeneration.
- `internal/catalog/` — descriptors. Note the one-way import rule: `internal/catalog` must not import
  `internal/app`, which is why app-layer descriptors live in the leaf `internal/appcatalog`.

### Generated files are never edited, and one of them is never blindly regenerated

Eight artifacts are committed but generated, each gated by a `fail-on-diff` check;
`scripts/regen-generated.sh` reproduces all of them byte-for-byte with CI. A lane that changes an
input regenerates in the same commit.

**`internal/catalog/signal_dispositions.json` is the exception.** Its dispositions are all *derived*
from the real dashboard and rule artifacts, so a new signal's disposition comes back empty and an
empty disposition always fails the gate. **There is no value a human may assign.** A signal on no
surface is settled by giving it a panel, not by editing the manifest — regenerating to turn a red
gate green does not work, and the three escape hatches that used to make it look like it did were
deliberately deleted (#526).

### Exclusive resources — one lane at a time, and only from the root agent

- **The m7kni Grafana stack.** Pushing rules and dashboards is pre-authorized and needs no asking.
  But `gcx resources push` is **additive** — it creates and updates, never deletes — so a rule
  dropped from the repo evaluates forever until removed by hand. Run
  `python3 scripts/verify_deployment.py` after any push (0 in sync, 1 drift, 2 unreachable).
- **Dashboards are delivered by GitSync, not by `gcx`.** Grafana writes UI saves back into the
  GitSync repo, so an API push is an out-of-band edit that leaves both sides disagreeing with no way
  to tell which is right. Rules go via `gcx`; dashboards go via `deploy/grafana/` and the workflow.
  Retire a dashboard by deleting the file, not through the API — the next sync recreates it.
- **The live lab tailnet is read-only.** `auto_configure` must never target a real tailnet. Lab
  names, addresses, identifiers, credentials and raw captures stay in ignored local paths.
- **The live deployment host.** Root agent only; it is named only in ignored local config.

---

## 4. Run-end against this tracker

The tracker *is* the report. There is no run-end file.

- Landed work: `backlog task edit <id> --check-ac N -s Done` **in one call**, with the commit SHA in
  the final summary. Splitting the criteria check from the status change lets an interrupted run
  leave finished work looking unfinished.
- Attempted and blocked: `-s Parked` with a concrete resume boundary — what was tried, what the next
  action is, what would unblock it. Parked is the status that exists to stop a long run's most
  valuable output from being flattened into "To Do".
- Untouched work needs no action; it is still `To Do` and self-evidently so.
- Discovered work: a new task labelled `needs-triage`. Never a note in a summary nobody queries.
- Notes and plans are appended (`--append-notes`, `--append-plan`), never set. The bare flags
  silently replace the whole section and destroy another lane's writes.

The run's closing terminal message carries only what no single task can: what this run learned as a
whole. Nothing durable may live only there.

Before any task goes to `Done`, the definition-of-done gate in `backlog/config.yml` must have
actually been run and its output seen — plus `scripts/verify-modules.sh` when a tool module changed,
and a real `gcx resources push` when an alert rule changed.
