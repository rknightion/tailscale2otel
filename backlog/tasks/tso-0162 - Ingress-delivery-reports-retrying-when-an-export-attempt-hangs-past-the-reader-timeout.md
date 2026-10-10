---
id: TSO-0162
title: >-
  Ingress delivery reports retrying when an export attempt hangs past the reader
  timeout
status: Done
assignee:
  - '@loop20'
created_date: '2026-10-09 22:26'
updated_date: '2026-10-10 00:23'
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
- [x] #1 With ingress_wal enabled, a fake exporter whose Export blocks without honouring its context makes IngressDeliveryRetrying report true once the attempt is older than the reader timeout, for metrics and for logs
- [x] #2 When the hung attempt returns, the retrying state clears without a restart
- [x] #3 A cooperative slow export younger than the timeout does not report retrying
- [x] #4 The new tests use testing/synctest, fail before the change and pass after: go test -run 'IngressDeliveryRetrying|HungExport' ./internal/telemetry/
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Implement frozen scope using synctest; regression red then green; exact candidate gate and security review before landing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop20: 1 implementation attempts; Done after acceptance, landed code and green composed gate. Earlier superseded/cancelled CI runs are not passes.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
loop20 landed evidence df6cd7225e175c9e739e3d9fdce2879c18f67939. Hung metric/log ingress exports report retrying strictly after the resolved reader timeout and clear on successful return, WAL-only readiness scope. Synctest regression red then green and independent base overlay reproduction; security review PASS; completed CodeRabbit zero findings. Final composed just check on actual shipped SHA129945fb3230f1c5d8a1127ad3e3c7faa2c514f4 passed, unchanged tree. Exact CI e96e74e31e2df4c697435dd4b7a32315e0e02f41 run38003933710 ci-success succeeded; no production diff outside backlog from that tested code. Helm absent-not-required (no landed chart change). Just formatting passed. Generated-input DoD is not applicable or covered by gate; no generator drift or weakened test claimed.
<!-- SECTION:FINAL_SUMMARY:END -->
