---
id: TSO-0070
title: Warn on gRPC exporter with CA rotation but no reconnection period
status: Done
assignee: []
created_date: '2026-08-30 09:34'
updated_date: '2026-08-31 03:39'
labels: []
milestone: m-5
dependencies: []
priority: low
ordinal: 73000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
CA rotation propagates to gRPC OTLP only via forced reconnect (internal/telemetry/exporters.go:729-744); with tls CA file watching enabled and no grpc_reconnection_period set, a rotated CA silently never applies. Add a Warnings() advisory for that combination.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The combination produces a startup warning naming the fix
- [x] #2 Covered by a config warnings test
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Lane H adds the gRPC CA-rotation-without-reconnection advisory without changing existing valid behavior.
<!-- SECTION:PLAN:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added the actionable startup warning for gRPC export with CA rotation but no reconnection period, with focused config warning coverage. Implementation SHA f35b6ab. Final integrated just check passed at 5b55617; exact-head CI run 33354208183 completed success.
<!-- SECTION:FINAL_SUMMARY:END -->
