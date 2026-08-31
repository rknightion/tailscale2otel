---
id: TSO-0100
title: Keep the docker-compose deployment path validated by CI now camden is retired
status: To Do
assignee: []
created_date: '2026-08-31 10:55'
labels:
  - needs-triage
milestone: m-9
dependencies: []
priority: medium
ordinal: 101000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
camden, the docker-compose instance, was deliberately retired by the owner (2026-08-31); its last telemetry was 2026-08-29 20:00 UTC and the lab Kubernetes deployment is now the only live one. deploy/docker-compose*.yaml and the compose secrets template are still shipped and documented, so they remain a supported path with nobody exercising them.

A live instance was implicitly validating those assets. Replace that with CI: render and start the compose stack against a stub or stdout exporter, assert it comes up healthy, and fail on drift between the compose assets and the config contract the app actually enforces. shutdownbudget_test.go already asserts against Compose assets, so there is an existing seam to extend rather than a new one to invent.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 CI exercises the docker-compose path on every relevant change, not just parses it
- [ ] #2 The check fails when a compose asset drifts from the config contract, proven by a negative test
- [ ] #3 Docs state that docker-compose is CI-validated rather than run in the lab
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
