---
id: TSO-0006
title: >-
  Migrate to OpenTelemetry v1.46.0 / log v0.22.0 after the log attribute API
  removal
status: Done
assignee: []
created_date: '2026-08-26 09:42'
updated_date: '2026-08-26 10:56'
labels: []
dependencies: []
ordinal: 10000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The batched OpenTelemetry Renovate update is the only red pull request on the board and it blocks every downstream dependency update. The upstream 0.21.0 release removed the log package's own Kind, Value and KeyValue types together with their constructors and conversion helpers, and moved log bodies and attributes onto the shared attribute package types. The emitter, the in-memory telemetry recorder and four telemetry test files still build against the removed surface, so the root module and every nested tool module that depends on it fail to compile. Port the call sites onto the shared attribute types, move all module pins to the new release train, and confirm the two other breaking changes in the same train are inert here: the HTTP exporter endpoint option no longer appends a default signal path, which this project already does for itself, and the log test record factory lost its attribute limit fields.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Every use of the removed log package attribute surface is ported onto the shared attribute package types
- [x] #2 The root module and all four nested tool modules pin the new OpenTelemetry release train and are tidy
- [x] #3 The endpoint-path and record-factory breaking changes in the same release train are confirmed inert or handled
- [x] #4 The full local gate passes and hosted CI is green on the default branch
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 go build ./... && go vet ./... && go test -race ./...
- [x] #2 golangci-lint run
- [x] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Bump the root module and all four nested tool modules onto the new release train. 2. Port the emitter, the recorder and the four telemetry test files off the removed log attribute surface onto the shared attribute types, holding the emitted wire shape constant. 3. Confirm the endpoint-path and record-factory breaking changes are inert. 4. Run the full local gate plus the module verifier, review, commit to the default branch and confirm hosted CI.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Three upstream behaviour changes land in this train, not one. Only the first is a compile break; the other two are silent and each cost a ten-minute package timeout to find.

1. Removed log attribute surface. The log package's own Kind, Value and KeyValue types and constructors are gone; bodies and attributes now use the shared attribute package. Ported the emitter, the in-memory recorder and four telemetry test files. The provider-scoped const attrs are already shared-attribute values, so their per-record conversion helper became an identity function and was deleted rather than retyped. The emitted wire shape is unchanged: the string-slice join and every value kind mapping were held constant deliberately.

2. The batch log processor now calls the decorated exporter's force flush from its shutdown path as well as from force flush, and the logger provider's shutdown blocks until every in-flight processor operation drains. A test fake that parks until its context is cancelled therefore wedges an uncancellable cleanup shutdown forever. Two tests hung for ten minutes each instead of failing.

3. The batch log processor now owns every exporter call in one worker goroutine and serialises force-flush requests through it, and it returns a bare context error on cancellation instead of joining the exporter's own error. Two concurrent force flushes can no longer occupy the log exporter simultaneously, and the exporter's error no longer surfaces once the context is done.

Neither 2 nor 3 is a production regression. Every production force flush runs under a bounded context and only distinguishes nil from non-nil, and each tailnet runtime owns its own logger provider, so the serialisation does not couple runtimes. The concurrency contract this project actually owns, running the metric and log legs of one flush in parallel, is untouched.

The concurrency test now asserts what remains true: both concurrent callers complete, both reach the exporter, and neither shuts a pipeline down. The cancellation test asserts the log leg by exporter call count rather than by error text, which is stronger and does not depend on wording upstream no longer propagates.

Cleanup ordering is now explicit rather than dependent on registration sequence. The first attempt registered the barrier-opening cleanup inside the constructor helper, which runs before the provider exists and therefore last under LIFO, reproducing the hang. One cleanup now opens the barrier and then shuts down. Verified by injecting a failure ahead of the barrier: the package reports in under half a second instead of timing out.

The endpoint-path change is inert. This project has always appended the per-signal path itself because the exporter used the URL as-is, which is exactly the new behaviour. The record-factory change is inert because nothing here uses the log test factory.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Ported the emitter, the in-memory telemetry recorder and four telemetry test files off the log package's removed Kind/Value/KeyValue surface onto the shared attribute types, and moved the root module plus all four nested tool modules onto the new release train. The provider-scoped const attrs are already shared-attribute values, so their per-record conversion helper collapsed to an identity function and was deleted; constLabelAttrs only ever emits string values, so the emitted wire shape is unchanged.

Two further changes in the same train are silent and cost a ten-minute package timeout each to find. The batch log processor now calls the decorated exporter's force flush from its shutdown path and the logger provider's shutdown drains in-flight operations, so a test fake parked until cancellation wedged an uncancellable cleanup forever; cleanup now opens the barrier and only then shuts down, in one registration so ordering cannot be got wrong. The processor also serialises force flush through a single worker and returns a bare context error on cancellation instead of joining the exporter's, so the concurrency test now asserts that both callers complete and reach the exporter, and the cancellation test asserts the log leg by exporter call count rather than by error text. Neither is a production regression: every production force flush runs under a bounded context and only distinguishes nil from non-nil, and each tailnet runtime owns its own logger provider.

The endpoint-path change is inert because this project has always appended the per-signal path itself, which is exactly the new behaviour; the record-factory change is inert because nothing here uses the log test factory.

Verified: go build, go vet and go test -race clean across the repository; golangci-lint 0 issues; scripts/verify-modules.sh green on all five modules including tidy diffs and govulncheck; no generated-artifact drift; CodeRabbit 0 findings. Landed as 613d0a3 with every hosted CI workflow green, which also closed the batched OpenTelemetry Renovate pull request.
<!-- SECTION:FINAL_SUMMARY:END -->
