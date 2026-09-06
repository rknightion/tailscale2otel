---
id: TSO-0145
title: Audit v5 documentation against shipped features and remove AI prose
status: Done
assignee:
  - '@codex'
created_date: '2026-09-06 17:59'
updated_date: '2026-09-06 18:29'
labels: []
dependencies: []
priority: high
type: docs
ordinal: 146000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Before the owner merges the v5 release PR, verify public documentation against the implementation and cover every user-facing feature, including missing guides. Parallel evidence and technical drafts feed a root-owned factual and de-AI edit.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Public documentation claims and examples are reconciled against source with a feature-to-document coverage inventory
- [x] #2 Missing user-facing features have usable documentation linked from the published site
- [x] #3 All maintained public prose receives the write-as-rob de-AI pass while generated regions and historical release records remain protected
- [x] #4 Documentation validation and just check pass; findings and exact revision evidence are recorded without merging the release PR
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Run four independent source-to-doc mapping lanes for configuration, deployment, signals and operations; each writes only an ignored evidence/draft file. Root integrates factual corrections and missing guides, applies the complete write-as-rob de-AI catalog to maintained public prose, checks feature coverage and site navigation, runs just check and documentation validation, reviews the final diff, then commits/pushes main and records exact revision evidence. Release PR 585 remains unmerged. Run contract and report paths: codex/goal-2026-09-06-v5-docs.md and codex/report-2026-09-06-v5-docs.md.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Four source audits and a second factual review completed. Added feature index, Kubernetes HA, PAM and event-explorer guides. Corrected PII defaults, receiver startup auth, file-credential scope, local/persistent flow exports, cursor shards, standby routing, and 40-second drain/55-second grace budget. Root applied de-AI edits; generated metric and coverage blocks preserved. Initial just check passed fmt/lint/vet/race tests/tool modules/Python/tidy/vulnerability checks, then stopped because generated-diff checks compare the working tree to the index, including changed documentation. Stage reviewed paths and rerun the full gate. No runtime change, release merge or live provider action.

Final local verification passed: just check exit 0, just gen with no unstaged diff, just --fmt --check, 28 docs.toml navigation targets, and local file/heading links across 32 maintained Markdown pages. All 514 Helm render assertions and 71 Compose assertions passed. Parsed config.example.yaml and Helm values are unchanged; only comments changed. Generated metric/coverage blocks remain byte-identical. One earlier Docker hygiene probe failed without a retained engine diagnostic; the diagnostic retry and final standard gate both passed. LogQL/TraceQL syntax remains unparsed by the existing query checker (variables checked). No live collection/HA exercise or published-site visual render claimed. CodeRabbit and new tests skipped for docs/comments only.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Audited configuration, deployment, signals and operations against the v5 source. Added linked feature, HA, PAM and event-explorer guides; corrected privacy, authentication, persistence/export, coordination and shutdown guidance; applied a root-owned de-AI prose pass. Full local gate and regeneration passed. Release PR 585 remains open. Exact commit and CI evidence are recorded in codex/report-2026-09-06-v5-docs.md after publication.
<!-- SECTION:FINAL_SUMMARY:END -->
