---
id: TSO-0072
title: Rate-limit admin authentication failures
status: Done
assignee: []
created_date: '2026-08-30 09:34'
updated_date: '2026-08-31 03:39'
labels: []
milestone: m-5
dependencies: []
priority: high
ordinal: 75000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
requireAdminAuth (internal/app/admin.go:226-256) counts rejections but never throttles; on a network-reachable bind the admin token is an unlimited online brute-force target. Add per-source backoff/limiting on the rejected path (cheap, memory-bounded), with self-obs for lockout events. Security-surface change: adversarial review tier.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Repeated failures from one source are throttled with bounded memory
- [x] #2 Legitimate auth after backoff expiry works; behaviour tested
- [x] #3 Lockout/throttle events observable
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Root F1 freezes bounded admin auth-failure throttling controls with conservative defaults; lane G later implements and security-tests the limiter and telemetry.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Lane G reported all four admin implementations present. Focused statushtml, supportbundle and apicontract checks are green, but the integrated internal/app race suite is temporarily blocked by Lane C's concurrent collector seams. Root directed Lane G to preserve scope and defer cross-package/negative guard evidence until the collector tree is stable; this is coordination, not a work blocker.

Lane G implemented bounded per-source admin auth throttling with a 1,024-source cap, fail-closed overflow bucket, Retry-After, expiry recovery and existing rejection telemetry reason=throttled. Focused race tests and deliberate negative mutations passed.

CodeRabbit review completed with three major findings. Verified and fixed the overflow-bucket finding: successful auth from an untracked source no longer clears a shared spray backoff. Added a regression guard, deliberately restored the bug to watch it fail, then restored the fix and passed the race test. The admin client-CA-without-cert finding was verified false because config.Validate already rejects that exact combination before App construction (validate.go admin.tls.client_ca_file guard).

Second CodeRabbit review completed with no critical or major findings. One valid minor found that the existing success-reset test first let allow() prune expired state, so it did not actually prove success() cleared live failure history. Fixed the sequence to add one post-expiry failure before success; targeted race test passes. No findings were left unresolved.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added bounded per-source admin authentication throttling with overflow protection, backoff recovery for legitimate clients, 429 Retry-After behavior, and existing rejection telemetry. Implementation SHA 6d9c23c. Final integrated just check passed at 5b55617; exact-head CI run 33354208183 completed success.
<!-- SECTION:FINAL_SUMMARY:END -->
