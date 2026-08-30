---
id: TSO-0053
title: Cardinality backstop for the posture attribute-namespace wildcard
status: To Do
assignee: []
created_date: '2026-08-30 09:30'
labels: []
dependencies: []
priority: medium
ordinal: 56000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
WithAttributeNamespaces("*") (internal/collector/devices/devices.go:540-562) promotes every posture namespace to metric labels with no cap, unlike every other cardinality lever in the repo (tag_rollup_limit, __other__ buckets). One MDM vendor emitting a per-scan-timestamp value under the wildcard blows up series with no built-in bound. Add a cap with overflow behaviour consistent with existing levers. Shares design with the posture compliance-gauge task TSO-0039 - do them together or sequence explicitly.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Wildcard namespace promotion is bounded with a documented overflow behaviour
- [ ] #2 The cap interacts sanely with cardinality.metric_limit accounting
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
