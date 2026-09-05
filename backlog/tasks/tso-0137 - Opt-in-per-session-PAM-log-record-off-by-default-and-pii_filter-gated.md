---
id: TSO-0137
title: 'Opt-in per-session PAM log record, off by default and pii_filter-gated'
status: To Do
assignee: []
created_date: '2026-09-05 17:16'
updated_date: '2026-09-05 17:22'
labels: []
dependencies: []
references:
  - internal/collector/pam
  - codex/report-2026-09-04-wave12.md
priority: low
type: feature
ordinal: 138000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Wave 12 cut the PII-carrying per-session log record first under its cut order; the shipped PAM surface is bounded session metrics plus the safe configuration snapshot. The owner decided on 2026-09-05 to build it as an opt-in log record. Who accessed which PAM service, from where, with what authorization result, is the question an operator asks after a session metric moves, and the identity fields that answer it are exactly the ones the metric allowlist forbids: that is why this is a log record and why it ships off. The session semantics and the PII fence are in the PAM API reference doc (sections 5b and 6): `result` is the authorization result and never connection health, a grant-layer denial produces no row at all, recordings populate asynchronously, and field presence varies by session_type. The sessions poller already computes the accepted-session delta against the durable evidence store; the record is emitted from that path, not from a second poll.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 `collectors.pam.session_log_enabled` (default false) emits one log record per newly accepted session from the existing delta path; replaying the same page or restarting against the same evidence store emits no duplicate record
- [ ] #2 Identity, address, device-name and command attributes are governed by the existing pii_filter categories (emails, user_display_names, user_ids, hostnames, tailscale_ips or external_ips by address range, command_text) rather than a PAM-only switch, and a test proves each category removes its field
- [ ] #3 The emitted attribute set is asserted against an allowlist; auth_info and events[].metadata are never emitted verbatim
- [ ] #4 The record carries a catalog descriptor, its docs/metrics.md row, a derived coverage disposition and a dashboard log panel, and the docs state that result is the authorization outcome and that sessions denied at the grant layer produce no record
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
