---
id: TSO-0100
title: Keep the docker-compose deployment path validated by CI now camden is retired
status: In Progress
assignee: []
created_date: '2026-08-31 10:55'
updated_date: '2026-09-01 18:44'
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

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
- Inspect the current docker-compose self-test and CI topology after camden retirement.
- Add the smallest CI wiring that executes the supported Compose validation path.
- Negative-test the workflow guard, validate declarative changes, and return changed paths plus evidence without committing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
- Added a disposable Compose CI override that starts the CI-built image through the supported deployment path, uses stdout delivery and a loopback-only control-plane endpoint, waits for the real health check, scans logs for credential canaries, and removes its uniquely named project/volume.
- CI continues through the existing image-smoke leg; documentation now states Compose is rendered and started by CI while the lab deployment remains Kubernetes-only.
- Extended compose validation to render the CI composition. Negative-tested all five guards; CodeRabbit found the stdout mutation was forcing render status and duplicating the production assertion, so it now preserves pipeline status and runs the shared assertion helper.
- `just compose-check` passed 71 checks with 0 failures; image build/smoke passed and `docker compose ls` was empty afterward. Final CodeRabbit deploy shard completed with 0 findings.
<!-- SECTION:NOTES:END -->
