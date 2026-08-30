---
id: TSO-0074
title: Include a bounded recent-log tail in the support bundle
status: To Do
assignee: []
created_date: '2026-08-30 09:34'
labels: []
dependencies: []
priority: medium
ordinal: 77000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
internal/app/admin_bundle.go collects config/diagnostics/state but not the last N slog lines, so "what just happened" debugging needs separate log access. Add an in-memory bounded ring of recent log records (respecting existing redaction) and include its tail in the bundle.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The bundle contains a bounded, redaction-safe tail of recent process logs
- [ ] #2 Ring size bounded and configurable; no PII leakage beyond existing log policy
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
