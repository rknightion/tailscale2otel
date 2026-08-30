---
id: TSO-0058
title: 'Hot-reload receiver secrets (streaming token, webhook secret)'
status: To Do
assignee: []
created_date: '2026-08-30 09:30'
updated_date: '2026-08-30 09:47'
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
- [ ] #1 Rotating either secret file takes effect without a process restart
- [ ] #2 Rotation behaviour (cutover vs dual-accept window) is decided, tested and documented
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
