---
id: TSO-0136
title: Attribute PAM telemetry to an operator-named tailnet via pam.tailnet
status: To Do
assignee: []
created_date: '2026-09-05 17:16'
labels: []
dependencies: []
references:
  - internal/app/collectors.go
  - internal/config/config.go
  - codex/report-2026-09-04-wave12.md
priority: medium
type: feature
ordinal: 137000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The PAM collector registers on the primary (first-configured) tailnet runtime, so every tailscale.pam.* series and PAM log record carries the primary tailnet's tailscale.tailnet attribute. Border0 is one organization per process, not one per tailnet, so in a multi-tailnet deployment the attribution is an accident of list order: the org may belong to a tailnet that is not first. Wave 12 raised this as owner question 1; the owner decided on 2026-09-05 to add an explicit `pam.tailnet` key rather than keep the implicit primary or drop the attribute. Default empty keeps today's primary-runtime behaviour so no deployment migrates. The registration site is internal/app/collectors.go (the `d.primary` gate) and the config seam is `PAMConfig` in internal/config/config.go; a new config key touches about eleven non-test files (example, schema, docs, Helm values and schema, env-var reference), all generated or root-owned.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 `pam.tailnet` (YAML and TS2OTEL_PAM__TAILNET) selects which configured tailnet runtime hosts both PAM schedules; empty keeps the primary runtime and existing deployments are unchanged
- [ ] #2 A value naming no configured tailnet (tailscale.tailnet or any tailnets[].name) fails config.Validate with an error listing the configured names; a matching value is accepted in both single- and multi-tailnet mode
- [ ] #3 In a two-runtime test driven through telemetrytest.Recorder, every tailscale.pam.* metric and PAM log record carries the selected tailnet's tailscale.tailnet attribute and exactly one copy of each series is emitted
- [ ] #4 config.example.yaml, docs/configuration.md, the Helm values and the generated schema and env-var docs carry the key with the primary-default semantics stated
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
