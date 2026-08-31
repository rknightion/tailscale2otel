---
id: TSO-0058
title: 'Hot-reload receiver secrets (streaming token, webhook secret)'
status: Done
assignee: []
created_date: '2026-08-30 09:30'
updated_date: '2026-08-31 03:39'
labels: []
milestone: m-4
dependencies: []
priority: high
ordinal: 61000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
internal/app/credreload.go watches only outbound OTLP/Pyroscope credential files; streaming.token_file and webhook.secret_file are read once and baked into the servers at construction (internal/stream/stream.go:591, internal/webhook/webhook.go:609). Rotating a leaked receiver secret today requires a restart, losing in-memory dedup windows - precisely when an operator least wants a restart. Design decided (owner, 2026-08-30): bring both files into the credreload watcher so new inbound requests validate against the rotated value without restart. Consider a brief dual-accept window (old+new) during rotation, and decide/document it explicitly.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Rotating either secret file takes effect without a process restart
- [x] #2 Rotation behaviour (cutover vs dual-accept window) is decided, tested and documented
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Lane E implements receiver secret hot reload without restarting listeners, covering streaming and webhook paths with TDD.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
CodeRabbit found that reusing the outbound opt-in pollInterval made receiver rotation inert under defaults. Root chose the narrower fix: receiver files always use the configured 30s cadence while outbound OTLP polling keeps its existing opt-in default.

Second CodeRabbit review raised the static Secret.Reveal construction path. Verified non-issue: app collectors pass file-backed streamTokenProvider/webhookSecretProvider closures into the servers; each handler samples the provider once per request, and the existing stream and webhook rotation tests prove the same handler rejects the old credential and accepts the new one. The static string is only the initial/fallback value.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Made streaming and webhook secret files reload without restart using tested atomic cutover behavior, while preserving bounded and redaction-safe diagnostics. Implementation SHA f35b6ab. Final integrated just check passed at 5b55617; exact-head CI run 33354208183 completed success.
<!-- SECTION:FINAL_SUMMARY:END -->
