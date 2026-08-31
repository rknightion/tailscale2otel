---
id: TSO-0095
title: Validate Wave 3 live on the lab deployment
status: To Do
assignee: []
created_date: '2026-08-31 10:54'
labels:
  - needs-triage
milestone: m-9
dependencies: []
priority: high
ordinal: 96000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Waves 1-3 shipped 100+ tasks that are CI-green and have NEVER run against the real tailnet. The lab Kubernetes deployment reports version 4.1.0-rc.29 (= 7d48fd5, the Wave 2 close), so every one of Wave 3s 32 tasks is unvalidated in production. camden, the docker-compose instance, was deliberately retired and last reported 2026-08-29 20:00 UTC; the lab cluster is the only live instance.

TRAP, CHECK THIS FIRST: the chart default is image.pullPolicy: IfNotPresent (deploy/helm/tailscale2otel/values.yaml:20), NOT Always. Deleting the pod will therefore re-use the cached image and validate nothing while looking like it worked. Confirm the LIVE values before touching anything; if the deployed values do not set Always, the rollout must pin or force the new image explicitly.

Scope: prove the Wave 3 signals, dashboards and alerts do what they claim on real data. Every new metric and log family, every regrouped dashboard tab, every new or changed alert rule. Read back through gcx and through the rendered dashboards, not from code.

Deliberately in scope because they can only be answered live: TSO-0093 (stream flow ingest-event-age p95 measured at ~20,200s) and whether the Wave 3 config keys behave as documented when actually set.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The lab deployment is confirmed running a Wave 3 build, with the image reference and pull policy recorded as evidence rather than assumed
- [ ] #2 Every metric and log family added in Wave 3 is confirmed present on the wire with the expected attributes, or its absence is explained
- [ ] #3 Both regrouped dashboards render with no empty panels other than those whose feature is genuinely disabled, verified visually
- [ ] #4 Every Wave 3 alert rule is confirmed to evaluate, with its current state recorded; any rule that cannot fire is explained
- [ ] #5 TSO-0093's ingest-age question is answered with fresh live evidence
- [ ] #6 Findings are filed as new tasks; this task records the evidence, not the fixes
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
