---
id: TSO-0001
title: Upgrade Go toolchain to 1.27
status: Done
assignee: []
created_date: '2026-08-23 19:06'
updated_date: '2026-08-26 10:56'
labels: []
dependencies: []
ordinal: 1000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Adopt Go 1.27 consistently across the application, nested modules, build images, CI configuration, setup automation, and version-specific contributor documentation.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 All active Go module and toolchain pins require Go 1.27
- [x] #2 Build images, CI jobs, setup automation, and current documentation agree with the Go 1.27 requirement
- [x] #3 The repository green-bar validation passes under Go 1.27
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 go build ./... && go vet ./... && go test -race ./...
- [x] #2 golangci-lint run
- [x] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Inventory every active Go version pin, including nested modules and container or CI toolchains. 2. Update the pins and version-specific documentation to Go 1.27.0 without changing historical records or fixtures. 3. Run the repository-defined validation gate, review the diff, commit to main, push, and confirm hosted CI.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Local Go 1.27.0 evidence: root build, vet, and golangci-lint passed with 0 issues; all four nested modules passed build, vet, and tests. The root race suite remains red only in workflow-contract tests for missing reusable-call exemptions and missing concurrency in arm-automerge.yml and ghcr-cleanup.yml. The same failures are present on prior main CI run 32145428038, so AC 3 and DoD 1 remain unchecked pending exact-head hosted CI. No generated input changed. CodeRabbit was skipped because the change is declarative toolchain, container, and documentation configuration only.

Exact-head CI run 32662556980 retained two before-fix failures: the v2.12.2 Linux analyzer could not handle Go 1.27 in tools/configcheck, and deploy/Dockerfile still set the deleted goroutineleakprofile experiment. The lint and cloud setup pin is now current v2.13.1; the obsolete experiment was removed from Docker and GoReleaser, and current profiling comments/docs were updated for Go 1.27 general availability. Linux-target lint passed for the root and all four nested modules, focused profiling/cert/credential tests and root build passed, actionlint passed, and GoReleaser validated the configuration.

Hosted CI is now green on Go 1.27 at 613d0a3: every workflow on the default branch completed successfully, including the full CI lane, CodeQL, Docker Security, Helm, actionlint, zizmor and Scorecard. The workflow-contract failures that held AC 3 open (TestExemptionsAreOnlyForReusableCallJobs and TestSupersedableLanesCancelOrSerialize) were fixed by d8a56ad and pass locally and in CI. A dispatched Client-lib tracking run on the same commit also passed both matrix legs, which retired the stale drift issue that had been misreporting those same two failures as a client-library break.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Go 1.27 is adopted across the root module, all four nested tool modules, the build images, CI, setup automation and the version-specific documentation, and the repository green-bar validation now passes under it. Verified by hosted CI at 613d0a3, where every workflow on the default branch completed successfully, plus a clean local go build, go vet, go test -race and golangci-lint run on the same toolchain.
<!-- SECTION:FINAL_SUMMARY:END -->
