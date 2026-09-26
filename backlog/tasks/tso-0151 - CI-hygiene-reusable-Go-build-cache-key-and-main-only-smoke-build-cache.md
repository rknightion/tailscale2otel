---
id: TSO-0151
title: 'CI hygiene: reusable Go build cache key and main-only smoke-build cache'
status: To Do
assignee: []
created_date: '2026-09-26 15:50'
labels: []
dependencies: []
priority: high
type: chore
ordinal: 152000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
1. `ci.yml` step 'Restore Go build cache for Docker' keys on `${{ github.sha }}`, so the key is unique per commit, never hit exactly, and re-saved every run (about 4.5 GB of dead entries). Key on `hashFiles('**/go.sum')` (plus Go version) and keep the restore-keys prefix.
2. The no-push smoke image build writes `cache-to: type=gha,mode=max,scope=ci-smoke` from every run including PRs. Write the cache only on push to main; PRs use cache-from only.

Context: fleet CI hygiene, tracked centrally as GHC-0006 in rknightion/.github. The container-publish.yml buildx cache move to a GHCR registry cache happens there and arrives here through the normal Renovate bump; orphaned PR and tag caches are deleted by the n8n repo-settings aligner. Neither needs work in this repo.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 docker-go-build cache key is content-hashed, and a second CI run on the same go.sum restores it exactly
- [ ] #2 ci-smoke build writes cache only on push to main
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
