---
id: TSO-0185
title: >-
  Bind the anti-forensics log-read investigation to recorder identity, not a
  fixed namespace
status: To Do
assignee: []
created_date: '2026-10-11 12:09'
labels:
  - needs-triage
dependencies: []
references:
  - /Users/rob/repos/tailscale2otel/deploy/grafana/gen
priority: medium
type: bug
ordinal: 185000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The current query selects any log subresource read in a fixed namespace and never restricts the recorder object identity, despite claiming recorder-specific anti-forensics. Found during TSO-0172 (comprehensive audit of both Grafana dashboards against the live stack); not admitted into loop26.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A recorder deployed in a nondefault namespace is discovered by the investigation, or the panel is explicitly relabeled as a generic namespace-log-read view.
- [ ] #2 An unrelated pod log read in the same namespace is not represented as a recorder-log read.
- [ ] #3 The fixture covers recorder and unrelated object identities, namespace changes, and relevant PII redaction behavior.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
