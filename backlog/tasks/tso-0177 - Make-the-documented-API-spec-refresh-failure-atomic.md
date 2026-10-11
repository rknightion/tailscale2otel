---
id: TSO-0177
title: Make the documented API spec refresh failure-atomic
status: To Do
assignee: []
created_date: '2026-10-11 12:05'
labels:
  - needs-triage
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/spec/README.md
priority: low
type: docs
ordinal: 177000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
CodeRabbit found that the new YAML-to-JSON refresh example redirects yq output directly into the vendored JSON baseline. A conversion failure can truncate a previously valid baseline. The completed vendor refresh itself is byte-identical to the successful upstream conversion; this is a future operator-command safety issue, not a schema defect.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A failed download or YAML conversion leaves the existing vendored JSON byte-identical.
- [ ] #2 A successful refresh replaces the baseline with valid JSON matching the conversion and cleans up temporary files; the example is exercised on local success/failure fixtures.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
