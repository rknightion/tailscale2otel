---
id: TSO-0132
title: >-
  Prove the coordinated Kubernetes surface live: standby scrapes, demotion,
  Lease loss, clock skew
status: To Do
assignee: []
created_date: '2026-09-04 07:31'
labels:
  - needs-triage
dependencies: []
priority: high
type: chore
ordinal: 133000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Wave 10 changed what a standby replica serves on its Prometheus endpoint and proved it with unit tests alone. No lab cycle ran. Wave 8 is the precedent that makes this worth doing: every offline test there passed against a defect the first live rollback cycle found in one pass, because each of those tests modelled the older release as a reader when the real one was a writer.

What is unexercised against a real cluster, all of it introduced or changed since the last live cycle:

- A standby's /metrics endpoint. The gatherer is selected per scrape rather than at listener start, so a demoted leader is supposed to drop its collector series on the very next scrape. Nothing has scraped a real standby.
- Demotion itself. A former leader must retain process telemetry and lose the full gatherer, with no window where stale tailnet series are still scrapeable.
- A scrape landing concurrently with a leadership transition. The implementation notes that a gather already in progress may return the prior surface; that boundary has never been hit for real.
- Lease deletion and conflicting replacement beneath a live leader, which is TSO-0130's subject.
- API-server loss during renewal, and clock skew against the renew deadline. Wave 10's audit reasoned about both and found no defect, but reasoning is not observation.
- The three Wave 10 alert rules have never been watched through a complete evaluation window.

Also verify one shipped behaviour that looks like a defect on paper: CoordinationNoStandby fires below one standby, while the chart defaults replicaCount to 1 and coordination.mode is set independently. A single-replica coordinated deployment therefore alerts permanently. Decide whether that is correct advisory behaviour, a chart validation gap, or a rule that should exclude single-replica deployments.

Lab constraints. Isolated sibling deployment only, never the managed workload, its Deployment or its PVC. Every temporary object deleted and read back absent by exact name before the run ends. RBAC proofs go through the direct cluster endpoint: the tailnet API proxy answers auth can-i --as as the operator, so a negative test through it returns a false yes.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A real standby is scraped and serves process telemetry including its coordination state, with no collector or per-tailnet series present
- [ ] #2 A leader is demoted and the next scrape after demotion carries the standby surface, with the observed lag recorded
- [ ] #3 Lease deletion and conflicting replacement are exercised against a live leader and the observed duplicate-active window is measured, feeding TSO-0130
- [ ] #4 API-server unavailability during renewal and a configured clock skew are exercised, and the behaviour is recorded whether or not it is a defect
- [ ] #5 The three coordination rules are observed through at least one full evaluation window and their firing history is recorded
- [ ] #6 The single-replica CoordinationNoStandby behaviour is adjudicated and the verdict recorded
- [ ] #7 The lab is returned to its prior configuration and image, and every temporary object is confirmed absent by exact name
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
