---
id: TSO-0096
title: Shard the CodeRabbit pre-commit review so a wave-sized diff can be reviewed
status: To Do
assignee: []
created_date: '2026-08-31 10:55'
labels:
  - needs-triage
milestone: m-9
dependencies: []
priority: high
ordinal: 97000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Wave 3 run reported that its required CodeRabbit reviews "repeatedly failed before analysis with WebSocket closed and no complete line". Reproduced 2026-08-31: `coderabbit review --agent --base <wave base>` over the 270-file Wave 3 diff fails at connecting_to_review_service every time with {"type":"error","errorType":"connection","message":"Connection failed: WebSocket closed"}. The SAME review scoped with --dir succeeds. It is a diff-size limit, not a service outage, and it silently cost seven of eight Wave 3 commits their review.

Sharding found two real defects that shipped unreviewed (a flow-store leak on four early returns in App.New, and a path traversal in organizationTailnetsURL), both fixed in 2167354.

Sharding also has a KNOWN FALSE-POSITIVE CLASS that must be documented alongside the recipe, or it will waste more time than it saves: --dir hides the rest of the repo, so any call site outside the reviewed directory reads as missing. Four of five majors in the post-Wave-3 pass were this artifact - two claimed flowstore.Backend lacked fields that store.go:74-80 defines, one claimed WithProbeIntervals was unwired when collectors.go:278 wires it, one claimed the checkpoint flush was missing when flushCheckpointStores covers both stores on the shutdown path. Every "symbol or wiring is missing" finding from a sharded run must be checked against the whole tree before it is actioned.

Deliver a just recipe that shards by directory, aggregates the NDJSON, and fails when any shard lacks a complete line - because a shard that dies at connect currently looks identical to a clean one.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A just recipe reviews a wave-sized diff by sharding, and its output distinguishes clean from failed per shard
- [ ] #2 A shard with no complete line fails the recipe rather than being counted as clean
- [ ] #3 The false-positive class from --dir scoping is documented where the next agent will read it before triaging findings
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
