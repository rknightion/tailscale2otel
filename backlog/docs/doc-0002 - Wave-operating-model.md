---
id: doc-0002
title: Wave operating model
type: guide
created_date: '2026-08-14 14:04'
updated_date: '2026-09-05 20:12'
---
This document carries **only what is true of tailscale2otel**. The campaign model itself - run
contract and run modes, the routing contract, authority and the thread pool, child lane briefs,
external-contract freezing, the blocker contract, the goal-file template, the run-end protocol and
the pre-flight checklist - is the *Agent fan-out protocol (canonical)* doc, and that doc wins on any
specific. Nothing here restates it. If a section below could be pasted into another repo unchanged,
it is in the wrong document.

That protocol is harness-neutral and names no model: it describes lanes by **role**, and its
Appendix A (Codex) or Appendix B (Claude Code) resolves a role into a concrete route. Waves on this
repo have historically been written by Claude and executed by Codex, so **name the harness in the
run contract and resolve every lane's route from that harness's profile** - a lane brief carrying a
role name alone is not routed. Appendix B defers Claude routing to the always-loaded global rules
and carries only the structural differences; read it for those, not for model tiers.

Every rule here exists because something failed. The failure is kept with the rule; a rule without
its reason gets argued away by the next session.

---

## 1. Rules this project added

### Work lands on `main`. No branches, no worktrees.

This repo has no PR flow for its own work. Lanes commit to the shared checkout on `main`; the root
agent owns the push. The usual "branch first" reflex is overridden here, and so is any tooling
default that creates a worktree - including `/fork`, which since Claude Code 2.1.221 makes a *new
worktree* rather than sharing the checkout.

### One commit per feature on `origin/main`

Lanes may make local checkpoint commits. Before pushing, the root agent squashes them into exactly
one commit per feature with `git reset --soft origin/main`. `rebase -i` is not available in this
environment. Conventional Commits (`type(scope): subject`) is not a style preference - Renovate and
release-please parse it.

**A squash must not separate a symbol from the code that calls it.** Wave 5's wiring lived in
`internal/app/app.go`, which the wiring pass owns, so its Kubernetes checkpoint calls were squashed
into `feat(coordination): add Kubernetes Lease leadership` (`1195f4b`) while the functions they call
landed in the next commit. **That commit does not compile**, and nothing can fix it now - the history
is pushed and carries rc tags. CI only ever builds the tip, so no gate saw it. When the wiring pass
edits a file on behalf of a feature that lands later, that hunk belongs in the later commit.
Build-check every squashed commit before pushing, not only the tip.

### A dispatch brief must forbid destructive git, not just `commit` and `push`

**2026-07-28: two lanes ran `git reset`/`git clean` on the shared tree and destroyed five other
lanes' uncommitted work.** Forbidding "don't commit" was not enough, because neither agent was
trying to commit. Every brief must name `git reset`, `git stash`, `git clean` and `go clean` as
prohibited on the shared tree.

The signature is distinctive: **tracked changes gone, untracked files still present.** Recovery is
`git fsck --lost-found`, and the first action is to **tag the dangling `WIP on main:` commit** before
anything else touches the object store - then restore only the lost files.

### A lane that hits a decision its brief does not cover stops and returns the question

It does not invent an answer. One round trip is cheaper than the rewrite. This is the escape hatch
for an ownership map that turns out to be wrong - a boundary with no escape hatch is a stop
condition wearing a safety label.

### Work that touches a live system stays on the root agent

Lanes do read-only investigation, code edits, tests and inventory sweeps. **SSH to the live deployment host, deploys,
Grafana pushes and any tailnet-side call stay with the root agent.** This is not only a blast-radius
rule: a dispatched lane inherits the parent's permission mode and cannot clear a soft block, because
clearing one requires a message from the user and a lane's transcript contains none. A brief is
explicitly refused as consent. A blocked lane must be run by the root agent, never re-dispatched.

### Specs and plans are never committed

They live in gitignored `docs/superpowers/`. Since the tracker landed, the queue is
`backlog task list --plain` and acceptance criteria live on the task - a plan file that re-enumerates
either one is a second source of truth that drifts.

---

## 2. Recurring defects in this codebase

These have each shipped at least once. Treat them as things to check for, not things to hope about.

### A local gate passing is not the same question as CI passing - five distinct instances

- **actionlint.** It shells out to whatever `shellcheck` is on `PATH`. Local shellcheck 0.11.0 does
  not emit SC2015 at all; the runner's older one does. A workflow edit passed locally on two
  actionlint versions and failed CI (`live-contract.yml`, 2026-07-28). The version gap runs the
  *wrong* way - local is newer and reports less - so "my tooling is current" is not reassurance.
  Prefer a plain `if` over `A && B || C` in any `run:` block.
- **`go test -race ./...` at the root is not the test suite.** There is no `go.work`, so it stops at
  the root module and never reaches the four tool modules. `tools/configcheck/go.sum` drifted 82
  lines out of tidy unnoticed (#437). Run `just check`, whose `just test-modules` leg covers
  all four.
- **promqlcheck against the module is a different question from promqlcheck against the artifacts.**
  Building and unit-testing the tool proves nothing about the dashboards and rules the repo ships.
  #526 landed **65 real failures in CI with every local gate green** for exactly this reason.
- **An offline validator written from the same assumption as its generator cannot catch that
  assumption being wrong.** `execErrState` was believed to spell its OK state `"OK"` in five places
  at once - the generator, two validators and three docs - so every offline gate agreed with itself
  and **all 19 advisory rules failed at push** with `Invalid value: "OK"` (it is `"Ok"`). `gcx
  resources validate` says outright that it does not validate the spec. **Only a real
  `gcx resources push` proves a rule is deployable**, and pushing here is pre-authorized.
- **`just lint` used to run whatever `golangci-lint` was on `PATH` with nothing checking it against the pin** (enforced since Wave 13, `0763467a`: a mismatch or missing binary now hard-errors naming `just setup`).
  The justfile carries `golangci_version := "v2.13.2"` and `just setup` installs it, but the lint
  recipe asserts nothing at run time - unlike the two helm generators, which verify the installed
  version against their pin and loudly SKIP rather than write a wrong file. A machine left on 2.12.2
  ran `just lint` green across all five modules while CI's 2.13.2 failed two SA1019 deprecations in
  `internal/coordination/lease_observer.go` (CI 33868900332, Wave 11). **After any pin bump, `just
  setup` before trusting a local lint.**

### `pii_filter` categories are RETAIN flags: true emits the field, false removes it

Wave 13's goal told the live run to "confirm the redactor removed each category's field" under the
default `pii_filter`. That is backwards: every category defaults to `true` and `true` means keep.
The run preserved the real semantics and proved both modes, but the wording cost a reconciliation
paragraph in the report. Write live-verification steps as "default run proves retention, all-false
run proves removal", and never describe the default as "redacted".

### The grafana-sync workflow pushes rules on every push to main, and a goal must not forbid that

`grafana-sync.yml` runs `gcx resources push` unconditionally on every push. A goal that lists "no
rule push" as a hard stop is therefore forbidding something the repository does by itself the
moment the root pushes. Wave 13 read that contradiction as an uncommissioned write and added a
changed-path guard to the workflow; the owner reverted it (`57f38dce`) because the push is additive
and `just verify-deploy` is the drift backstop. Phrase the prohibition as "no rule push by hand"
and enumerate what each mandated procedure touches before writing a prohibition, per the protocol's
pre-flight list.

### The lab runs `tag: main` with no rollout trigger, so it is always behind

The lab Argo application pins the image to the mutable `main` tag with `pullPolicy: Always`. A pod
only pulls on (re)start, and nothing restarts it when `main` moves, so the running digest is
whatever was current at the last rollout. Wave 13's 30-day stack sweep found 116 shipped families
with no samples; the pod was seven days behind `main` and its values enable none of PAM, the
objectstore paths, the snapshot logs, the device change-log or the Kubernetes audit collector.
**A zero-sample family on the stack says nothing about the code until the lab's digest and values
have been checked**, and a wave that ships a feature has not exercised it on the lab until the pod
has restarted on an image that contains it. TSO-0140 and TSO-0141 carry the fix.

### auto-rc fires after CI, so a wave can finish green and leave main red

Wave 11's exact-head CI 33873152349 passed on `ba8ba6f5` at 12:30 and the run reported itself
complete. auto-rc 33873859808 then fired off that green CI, cut `v5.0.0-rc.11` at 12:39:58, and its
`binaries` job failed at 12:45:35 when `wait-for-module-proxy.sh` gave up at its 300s default. The
tag was real and the proxy ingested it about 14 minutes after creation, so a re-run went green with
no code change - exactly what the script's own message predicts.

Two durable points. **A wave's closing check must cover the workflows that fire off CI, not only CI
itself**, because the RC tag does not exist until after the run has declared itself done. And
**`WAIT_TIMEOUT=300` is a guess about infrastructure nobody here controls**: rc.10 was ingested
inside it and rc.11 was not, on tags 29 minutes apart.

auto-rc is also the noisiest workflow on this repository. It cuts a full publish - two image arches,
chart, SBOM, notices, cosign, and a goreleaser cross-compile - on **every** green CI on main, so
11 RC tags exist for one unreleased 5.0.0 and 8 of them are from a single day. Six of 39 non-skipped
runs failed, on four unrelated external causes: proxy ingestion lag, a StepSecurity agent timeout, a
registry `not found` on a manifest digest, and a cosign version guard. Read an auto-rc red as
infrastructure until proven otherwise, and never as a signal about the commit.

### A test with a wall-clock margin will eventually fail on a loaded runner

Five CI runs on 2026-09-02 produced **four different** failing tests, on commits that could not have
caused them - a backlog-markdown-only commit, a merge commit, a dependency bump and a workflow-pin
bump. None reproduced locally under `-race -count=8` or `GOMAXPROCS=1`. The shared cause is CI I/O and
scheduling pressure across 26 concurrent jobs, not a logic race.

The damage is not the individual failures. **A gate that fails about half the time on unrelated
changes trains everyone to retry rather than read**, so a real regression is dismissed as noise and a
green run stops being evidence. Wave 5 retried past one of these; Wave 6 retried past another.

**The fix is to remove the timing dependency, never to widen the margin.** A longer timeout is a
guess about a runner you do not control, and it postpones the failure instead of ending it. A test
that has no wall-clock margin cannot flake on timing, which also makes "is it fixed?" answerable
without a statistical argument. Where a test genuinely needs time to pass, use `testing/synctest`, as
`internal/app/heartbeat_test.go` does.

**Do not merge past a flake, and do not keep rerunning until green.** Both are the same reflex, and
it is the reflex that hides regressions.

**Assert the invariant, not its side effect on the clock.** Wave 7's four fixes all took the same
shape once diagnosed. The sqlite conversion test inferred "the VACUUM ran outside the query timeout"
from a 32 MiB blob taking longer than 25 ms; the real invariant is context lineage, and a fake SQL
driver observes which context the VACUUM inherited with no disk in the loop. The self-observation
test synchronised on `scrape.success` and then read two metrics emitted after it; calling `RunTick`
synchronously makes completion the barrier. **The fake-driver context probe is the standard pattern
here** for anything asserting which context an operation inherited - owner decision 2026-09-03. Reach
for it before reaching for a duration.

### `gh run rerun` can never pick up a new base commit

A pull-request CI run is built from a **merge commit GitHub froze when the run was created**. Rerunning
replays that same commit, so a fix landed on `main` afterwards is not in it, however many attempts you
burn. Wave 7 parked on exactly this: PR #605's run was pinned to merge commit `53eac5a`, whose base
parent was the pre-fix `56c046e`, and the reachability check could not have passed at any attempt
count.

The symptom is a rerun failing on the very thing you just fixed, which reads as "the fix did not
work" rather than "the fix was not present". **Check the run's merge commit parents before believing
a rerun.** To get a run that contains current `main`, move the PR head - `gh pr update-branch <n>` -
which creates a new head and a genuinely fresh run. A rerun is only valid evidence when nothing the
run depends on has changed outside it, which is what makes it the right tool for a *reusable*
workflow fix and the wrong one here.

### Guard tests over `.github/workflows` that pass while asserting nothing

Roughly three per campaign phase. A substring assertion matches an unrelated line or a filename; a
regression the test was written to catch gets deleted by the compiler. **Every guard test must be
negative-tested** - break the thing on purpose, watch the test go red, put it back.

### A green workflow run is not proof it published anything

`grafana-sync` used `git diff --quiet`, which sees only *tracked* files. When the dashboard artifact
was renamed into a pair, both new filenames were untracked, the check read "already matches", and
**three consecutive successful runs published nothing** (fixed in `f167a1c`: stage first, then diff
the index). Verify the far side by listing the GitSync repo tree, not by the workflow's conclusion.

### The tailnet Kubernetes proxy answers `auth can-i --as` as the operator

An RBAC negative test run through the tailnet API proxy context is a **false negative every time**.
The proxy authenticates the operator and the impersonation header does not change the answer, so a
denied verb comes back `yes`. Verified against a ServiceAccount that does not exist:

```
kubectl --context <tailnet-proxy> auth can-i delete secrets -n <ns> --as=system:serviceaccount:<ns>:nonexistent   -> yes
kubectl --context <direct-cluster-endpoint> ... same probe                                                        -> no
```

**Every RBAC proof, positive or negative, goes through the direct cluster endpoint.** A wave that
verifies a chart's Role through the proxy has proved nothing, and the failure mode is the dangerous
direction: over-permissive answers that look like the Role working.

### A bare `gcx` command picks a stack, and it is not necessarily yours

The account holds **four** Grafana Cloud stacks. A bare `gcx resources push` authenticated against
the wrong one and failed 401 before mutating anything, and a bare verification run selected the
unrelated `rkaidev` stack and failed the same way. The 401 is the lucky outcome; the dangerous one is
a command that succeeds against a stack you did not mean.

**Pass the context explicitly, every time**: `gcx --context m7kni resources push -p deploy/alerts/grafana-managed`
and `just verify-deploy m7kni`. TSO-0123 threaded an optional context through every check and pull
without changing the user-level current context, so there is no longer a reason to rely on whatever
that happens to be.

### A gauge cannot count events, and `changes()` over one is not a workaround

`tailscale2otel.coordination.leader` is a last-value gauge, so `changes()` over it counts sample
transitions in whatever the series retained: a scrape gap, a restart, or a series appearing and
disappearing all read as transitions. It cannot truthfully count completed leadership handovers, and
a flapping rule built on it would fire on deployments rather than on flapping.

Wave 10 rejected that rule rather than shipping a plausible-looking one, which was right. **When a
question is about how many times something happened, the signal has to be a counter incremented at
the event.** Deriving it from a gauge's shape produces a rule that is confidently wrong, and an alert
nobody can trust is worse than no alert.

### A renamed metric leaves a panel silently empty

It still loads; it just shows "No data". `internal/catalog/dashboardrefs_test.go` is the only thing
connecting the shipped artifacts to the in-code catalog. It has to subtract label and log-attribute
names too, because labels share the `tailscale_` prefix and a text scan cannot tell a metric from a
label by shape.

### `scripts/verify-modules.sh` does not exist, and four goal files told a wave to run it

It was absorbed into the justfile by TSO-0025. `just check` runs the module legs through
`just test-modules`, and `internal/ci`'s coverage contract keeps the module list complete. Every
wave goal from Wave 6 onward repeated the stale instruction, and each run discovered it independently
at the moment it mattered.

The durable point is not the filename. **A command copied forward from one goal file to the next is
never re-verified**, so a task-surface change silently invalidates every future goal at once. Check
each command a goal file names against `just --list` before the goal is written, not while the run is
in flight.

### The tool modules do not run the way their own help text says

`go run ./tools/metricscatalog` from the repo root **fails** - separate `go.mod`. Use
`go run -C tools/metricscatalog .` with an absolute `-file`.

### A major release breaks unless the module path moves first

release-please does not maintain the Go module path. A `vN` tag against a stale `/vN` path kills the
GoReleaser binaries job - it ate every archive at v2.0.0 (#174). Run `scripts/bump-module-major.sh`
and land it on `main` *before* merging the release PR.

Related and recurring: **generated artifacts that embed the release-managed version break the
release PR itself, and they arrive in a queue** - fixing one exposes the next. The sharpest is that
release-please's version regex has no global flag, so a line carrying the version twice (a
shields.io badge: label *and* URL) half-updates and still fails the diff.

### The standby Prometheus surface is settled: process-only, selected per scrape

A replica in `coordination.mode: kubernetes` serves its pull endpoint continuously, before and after
campaigning, and the gatherer is chosen **on every Gather** rather than when the listener starts. A
leader serves the full gatherer; a standby, including a former leader after demotion, serves process
telemetry only.

Both halves are load-bearing. Serving continuously is what makes a standby distinguishable from a
dead pod on the pull path, which it previously was not. Selecting per scrape is what stops a demoted
leader from keeping its collector and per-tailnet series scrapeable: those series describe a tailnet
it is no longer polling, so presenting them as live is worse than presenting nothing.

Settled 2026-09-04. Do not widen the standby surface beyond process metrics, and do not move the
gatherer choice back to listener start.

### A marker on a shared object is not a durable migration signal

The Kubernetes checkpoint migration marked the legacy ConfigMap to record that migration had
completed, then chose its merge semantics from that marker. **The pre-sharding release removes the
marker when it writes**, so after a rollback and re-upgrade the store took the interrupted-migration
path and merged only keys absent from the shards - silently discarding the cursors the older release
had advanced. The newest-wins path shipped for exactly this case was unreachable in it.

Offline tests all passed, because every one of them modelled the older client as a reader. Only the
live rollback cycle wrote through it.

**A signal about the current release's state must live where only the current release writes.** When
a design puts one on a shared object, the test that matters is not "can we read it back" but "what
does the other release do to it while writing". Model the older client's writes, not just its reads.

### The release posture is deliberate, and it is not the wave's to change

The owner merges the release PR **by hand. Never automerge it, and no wave may merge it.** Waves
validate from the `rc` images, do not tag, and do not touch release-please configuration. A wave that
finds an unreleased backlog of work does not "fix" it by releasing.

**The board drained on 2026-09-04 and the repository is on `/v5`.** Wave 9 landed TSO-0118 and
TSO-0094 and moved the module path; TSO-0036 shipped in Wave 12 (`9994c0a`) once PAM went live on the lab; only TSO-0133 (dated) and
TSO-0135 remained from that drain.
PR #585 now reads `chore(main): release 5.0.0` with the receiver-credential break as its single
`BREAKING CHANGES` entry. The call to merge it is the owner's and nobody else's.

**The `/v5` path is proven end to end, so the #174 cliff cannot recur at 5.0.0.** `v5.0.0-rc.2`
published **17 assets** including archives for all five platforms, `SHA256SUMS`, the sigstore
signature, the in-toto attestation, per-archive SBOMs and the Helm chart. That is exactly the set
v2.0.0 lost. GoReleaser builds with `gomod.proxy: true`, so the tagged module really was fetched from
the proxy under the new path. A snapshot build would not have proved this; a real rc tag did.

### `feat!(scope):` is not Conventional Commits and release-please silently drops it

The bang goes **after** the scope: `feat(config)!:`, never `feat!(config):`. The malformed form does
not error and does not warn. release-please simply does not recognise it, so the commit contributes
nothing: no changelog entry, no major bump, and the work looks unreleased while sitting on `main`.
Verified at `399b67a0`, which is invisible in the 5.0.0 changelog while the empty follow-up commit
that carried the correct header appears in its place.

Repair it **additively**, with a second commit carrying the correct header. Never rewrite published
history to fix a commit message; the wrong header costs a slightly odd provenance line in the
changelog, and a force-push costs every consumer that already fetched.

### The client-go binary cost is settled, at +117%

Wave 5 made `k8s.io/client-go` a direct dependency of the root module for Lease coordination and
ConfigMap checkpoints. Measured identically on both sides - `-trimpath -s -w`, `CGO_ENABLED=0` - the
shipped binary went from **28,323,154 to 61,461,650 bytes**. The owner accepted that on 2026-09-02:
no build tag, no second image variant, no hand-rolled API client. Do not re-open it.

Do not quote the number from a plain `go build` either. Unstripped it reads about 90 MB, which makes
the increase look far larger than it is; Wave 5's report did exactly that. Any before-and-after must
use the release flags, because that is the binary that ships.

---

## 3. Lane conventions

### Single-owner files - never two lanes, never concurrently

- `deploy/grafana/gen/build.py` and `deploy/alerts/gen/build_rules.py` - the generators. Serialize.
- `internal/app/collectors.go` and the rest of the composition root - wiring pass only.
- `internal/config/` and `config.example.yaml` - one owner; `docs/env-vars.md` is generated from the
  latter, so two lanes editing it produce a conflicting regeneration.
- `internal/catalog/` - descriptors. Note the one-way import rule: `internal/catalog` must not import
  `internal/app`, which is why app-layer descriptors live in the leaf `internal/appcatalog`.

### A new config shape has four seams, and a goal that names one commissions a lane that finds three

A lane told to add a config key touches `internal/config/` and `config.example.yaml`. A lane told to
add a **map or list** config shape also touches `config.schema.json`, the Helm chart's `values.yaml`
and `values.schema.json`, and the `TS2OTEL_*` environment loader - which has to *reject* a child-key
encoding for a shape the env convention cannot express, rather than silently ignoring it. TSO-0024's
`port_overrides` hit all four; only the first was in its ownership table, so the root inherited the
rest at wiring.

Assign the schema, Helm and env-loader seams explicitly whenever a lane introduces a structured
shape, or state that the root owns them. Leaving them unassigned does not protect them - it just
moves the work to whoever notices, after the lane has reported done.

### Generated files are never edited, and one of them is never blindly regenerated

Eight artifacts are committed but generated, each gated by a `fail-on-diff` check;
`scripts/regen-generated.sh` reproduces all of them byte-for-byte with CI. A lane that changes an
input regenerates in the same commit.

**`internal/catalog/signal_dispositions.json` is the exception.** Its dispositions are all *derived*
from the real dashboard and rule artifacts, so a new signal's disposition comes back empty and an
empty disposition always fails the gate. **There is no value a human may assign.** A signal on no
surface is settled by giving it a panel, not by editing the manifest - regenerating to turn a red
gate green does not work, and the three escape hatches that used to make it look like it did were
deliberately deleted (#526).

### Exclusive resources - one lane at a time, and only from the root agent

- **The m7kni Grafana stack.** Pushing rules and dashboards is pre-authorized and needs no asking.
  But `gcx resources push` is **additive** - it creates and updates, never deletes - so a rule
  dropped from the repo evaluates forever until removed by hand. Run
  `python3 scripts/verify_deployment.py` after any push (0 in sync, 1 drift, 2 unreachable).
- **Dashboards are delivered by GitSync, not by `gcx`.** Grafana writes UI saves back into the
  GitSync repo, so an API push is an out-of-band edit that leaves both sides disagreeing with no way
  to tell which is right. Rules go via `gcx`; dashboards go via `deploy/grafana/` and the workflow.
  Retire a dashboard by deleting the file, not through the API - the next sync recreates it.
- **The live lab tailnet is read-only.** `auto_configure` must never target a real tailnet. Lab
  names, addresses, identifiers, credentials and raw captures stay in ignored local paths.
- **The live deployment host.** Root agent only; it is named only in ignored local config.

---

## 4. Run-end against this tracker

The tracker *is* the report. There is no run-end file.

- Landed work: `backlog task edit <id> --check-ac N -s Done` **in one call**, with the commit SHA in
  the final summary. Splitting the criteria check from the status change lets an interrupted run
  leave finished work looking unfinished.
- Attempted and blocked: `-s Parked` with a concrete resume boundary - what was tried, what the next
  action is, what would unblock it. Parked is the status that exists to stop a long run's most
  valuable output from being flattened into "To Do".
- Untouched work needs no action; it is still `To Do` and self-evidently so.
- Discovered work: a new task labelled `needs-triage`. Never a note in a summary nobody queries.
- Notes and plans are appended (`--append-notes`, `--append-plan`), never set. The bare flags
  silently replace the whole section and destroy another lane's writes.

The run's closing terminal message carries only what no single task can: what this run learned as a
whole. Nothing durable may live only there.

Before any task goes to `Done`, the definition-of-done gate in `backlog/config.yml` must have
actually been run and its output seen - plus `just test-modules` when a tool module changed,
and a real `gcx resources push` when an alert rule changed.
