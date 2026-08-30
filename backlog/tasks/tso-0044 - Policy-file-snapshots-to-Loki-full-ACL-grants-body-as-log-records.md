---
id: TSO-0044
title: Policy file snapshots to Loki (full ACL/grants body as log records)
status: To Do
assignee: []
created_date: '2026-08-30 09:27'
labels: []
dependencies: []
priority: medium
ordinal: 47000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Emit the full HuJSON policy file as a log record so a Grafana dashboard panel can display the current ACL and its history from Loki. Design settled (owner, 2026-08-30): RAW body in the log record (not base64 - grep-able in Loki), emitted on ETag/revision change plus a daily heartbeat snapshot, OFF by default because the policy contains user emails and group members (pii_filter-style opt-in). The acl collector already polls getPolicyFile with ETag revision tracking - hook there. Attribute-mark the record (snapshot marker, etag, size) so dashboards can query latest-snapshot. Add a Policy tab panel rendering the latest snapshot.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 On policy revision change and on a daily heartbeat, an opt-in log record carries the full raw policy body with etag/size attributes
- [ ] #2 Off by default; enabling it is an explicit config opt-in documented with the PII implications
- [ ] #3 A generated dashboard panel displays the latest policy snapshot
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
