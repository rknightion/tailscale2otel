---
id: TSO-0188
title: Document and prove optional single-tailnet profile selector behavior
status: To Do
assignee: []
created_date: '2026-10-11 12:14'
labels:
  - needs-triage
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/internal/app/profiling.go
priority: medium
type: task
ordinal: 188000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Guarded dashboard review found that profiles are process-global by default and in multi-tailnet mode, but optional single-tailnet name and hashed label modes emit different ownership labels. Four profile panels select service_name only. A blanket no-tailnet-label exemption is unsupported; a universal raw-name matcher would also erase default or hashed profiles. This is a contract gap, not proven live disclosure. Found during TSO-0172 (comprehensive audit of both Grafana dashboards against the live stack); not admitted into loop26.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Local fixture evidence records emitted ownership labels for off, name, hashed and multi-tailnet modes, without exposing private names.
- [ ] #2 Dashboard descriptions and supported selector behavior accurately distinguish process-global profiles from optional single-tailnet ownership; default and hashed profiles are not erased by a fabricated raw-name matcher.
- [ ] #3 Any newly supported selector behavior has independent failing-before/passing-after and real query/render proof.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
