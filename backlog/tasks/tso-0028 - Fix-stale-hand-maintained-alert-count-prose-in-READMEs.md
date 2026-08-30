---
id: TSO-0028
title: Fix stale hand-maintained alert-count prose in READMEs
status: To Do
assignee: []
created_date: '2026-08-30 08:44'
labels: []
dependencies: []
priority: medium
type: bug
ordinal: 31000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
README.md:192 claims "77 of 78" alert rules link a canonical dashboard panel and deploy/alerts/README.md:138 claims "92 of the 100"; a direct count of deploy/alerts/grafana-managed/*.json finds 96 rules carrying __panelId__. Both sentences are hand-maintained in a repo that drift-gates every other generated fact, and they disagree with each other and with the artifacts. Found during a product-surface review (2026-08-30); counts verified against the generated manifests, root cause not yet investigated. Fix should make the numbers generated (from deploy/alerts/gen/build_rules.py) or CI-asserted rather than hand-edited.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Both prose counts match the generated rule manifests
- [ ] #2 The counts are produced by the generator or asserted by a drift/CI check so they cannot silently rot again
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
