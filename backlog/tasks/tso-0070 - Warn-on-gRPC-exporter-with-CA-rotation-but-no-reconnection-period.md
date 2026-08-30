---
id: TSO-0070
title: Warn on gRPC exporter with CA rotation but no reconnection period
status: To Do
assignee: []
created_date: '2026-08-30 09:34'
labels: []
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
- [ ] #1 The combination produces a startup warning naming the fix
- [ ] #2 Covered by a config warnings test
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
