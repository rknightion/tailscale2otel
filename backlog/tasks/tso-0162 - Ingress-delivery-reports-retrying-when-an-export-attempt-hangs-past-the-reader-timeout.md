---
id: TSO-0162
title: >-
  Ingress delivery reports retrying when an export attempt hangs past the reader
  timeout
status: To Do
assignee: []
created_date: '2026-10-09 22:26'
labels: []
dependencies: []
references:
  - internal/telemetry/scheduled_metrics.go
  - internal/telemetry/ingress_delivery.go
priority: medium
type: bug
ordinal: 162000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
After TSO-0148, readiness reaches delivery health only through the ingress WAL coordinator's deliveryRetrying(), which reads Provider.IngressDeliveryRetrying() (internal/telemetry/scheduled_metrics.go). That signal counts only failed attempts. An exporter that ignores its context and blocks (seen with a hung stdout writer) never fails an attempt, so readiness stays green forever. Before TSO-0148 the 1-minute flush timeout surfaced it. Found in loop19 review (Low). Decided 2026-10-09: an in-flight metrics or logs export attempt older than the resolved reader timeout counts as retrying; scope is the ingress WAL path only (no new readiness gate when ingress_wal is disabled); the state clears with no manual reset when the attempt returns.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 With ingress_wal enabled, a fake exporter whose Export blocks without honouring its context makes IngressDeliveryRetrying report true once the attempt is older than the reader timeout, for metrics and for logs
- [ ] #2 When the hung attempt returns, the retrying state clears without a restart
- [ ] #3 A cooperative slow export younger than the timeout does not report retrying
- [ ] #4 The new tests use testing/synctest, fail before the change and pass after: go test -run 'IngressDeliveryRetrying|HungExport' ./internal/telemetry/
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
