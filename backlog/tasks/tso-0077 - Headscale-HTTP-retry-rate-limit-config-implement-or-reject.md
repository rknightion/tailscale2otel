---
id: TSO-0077
title: 'Headscale HTTP retry/rate-limit config: implement or reject'
status: Done
assignee: []
created_date: '2026-08-30 09:35'
updated_date: '2026-08-30 12:58'
labels: []
milestone: m-1
dependencies: []
priority: high
ordinal: 80000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
headscale.http.retry and rate_limit are accepted "for parity" but explicitly NOT applied (config.example.yaml:41-45) - accepted-but-inert config is the worst state. Either implement retry/rate-limit in internal/hsapi (mirroring the tsapi transport behaviour) or make setting them a validation error. Owner preference not yet stated between the two - decide on pickup with a bias to implementing.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The keys are either honoured by hsapi (with tests) or rejected loudly at validation
- [x] #2 config.example.yaml comment and generated docs updated to match reality
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
## Frozen seam (do not renegotiate)

New leaf package `internal/httpretry`, containing ONLY provider-neutral pieces:

- The whole of `internal/tsapi/ratelimit.go`, MOVED (not copied) and exported: `Limiter`, `Waiter` (the `Wait(ctx) error` interface), `RateLimitTransport`, `WrapRateLimit(base, ratePerSec)`, `NewWaiter(ratePerSec)`.
- `ComputeBackoff(delay, maxDelay time.Duration, rnd float64) (sleep, next time.Duration)` — moved verbatim from transport.go:627.
- `RetryAfter(header string) time.Duration` — moved verbatim from transport.go:643.
- `RetryableOutcome(resp *http.Response, err error) bool` — moved from transport.go:331. If its current body reaches into tsapi-specific error classification, keep the STATUS-code half here and leave the transport-error half in tsapi behind a `Classify` hook; do not drag OAuth knowledge into the leaf package.

`internal/httpretry` imports nothing from `internal/tsapi` or `internal/hsapi`. Both depend on it. This is the "one source of truth" seam and it is what makes this task a Wave 1 foundation rather than a Headscale detail.

`internal/tsapi` keeps `retryTransport` structurally intact and calls the moved functions. **No behaviour change in tsapi is permitted**, and the existing tsapi test suite passing unchanged is the acceptance evidence for that half. If a tsapi test needs editing, stop and return the question — a required edit means the move was not behaviour-preserving.

`internal/hsapi` gains its own small retry RoundTripper built on those primitives, wired in `NewClient` from three new `Options` fields:

```go
// Options (internal/hsapi/client.go:29)
MaxAttempts int           // 0 or 1 = no retry
BaseDelay   time.Duration
MaxDelay    time.Duration
RateLimit   float64       // requests/sec across all Headscale calls; <= 0 = unlimited
```

Composition-root wiring passes `cfg.Headscale.HTTP.Retry.MaxAttempts/BaseDelay/MaxDelay` and `cfg.Headscale.HTTP.RateLimit` into those fields.

Ordering must match tsapis and the reason must be carried in the comment: the rate-limit wait happens on the PARENT context BEFORE the per-attempt timeout is applied, so queueing for a token is not charged against the HTTP timeout. Getting this backwards is silently wrong and no test would obviously catch it — write the test that pins it.

## Self-obs must follow

`hsapi.RequestInfo` (client.go:60) gains `Attempts int` and `WaitDuration time.Duration`, mirroring `tsapi.RequestInfo` (transport.go:56). Correct the two stale comments that say hsapi never retries (client.go:3 package doc and client.go:57). Then check whether the `api.retries` / api-request self-obs metrics in `internal/appcatalog` are emitted for the Headscale path or only the Tailscale one, and wire them if not — an implemented retry that reports nothing is half the feature.

## Docs

Rewrite config.example.yaml:41-45. The comments currently read "accepted for parity with tailscale.http but NOT applied by the minimal v1 Headscale client" and "the ONLY http knob applied in v1" — both become false. Then `just gen-envref`. Grep docs/ for the same claim and fix every copy in the same commit.

## Work (test-first)

1. Move `ratelimit.go` and the three functions; run the full tsapi suite unchanged. This step is behaviour-preserving and its acceptance check is "no tsapi test was edited".
2. hsapi retry tests against an `httptest` server: 429 then 200 succeeds within `max_attempts`; `Retry-After` honoured and clamped to `max_delay`; 5xx retried, 4xx (other than 429) not; `max_attempts: 0/1` means exactly one attempt; a cancelled context aborts mid-backoff; `RequestInfo.Attempts` reports the true count; `WaitDuration` is non-zero under a rate limit and excluded from `Duration`.
3. Wiring + docs + `just gen-envref`. Gate: `just check` AND `just test-modules` is not needed here (no tool module changes), but `just lint` covers all four modules via `just check`.

## Explicitly OUT of scope

Rewriting `retryTransport`, changing tsapi behaviour, adding retry to any other client, and any new config key.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
## Research 2026-08-30 (Wave 1 planning, HEAD 1dd76a9) — DECIDED: implement, not reject

The description left the fork open with a bias to implementing. Implementing wins decisively on cost, because the config surface already exists.

`HeadscaleConfig.HTTP` is `TailscaleHTTPConfig` (internal/config/config.go:468, comment: "reuse the same timeout/retry/rate_limit shape"), so `headscale.http.retry.{max_attempts,base_delay,max_delay}` and `headscale.http.rate_limit` are ALREADY declared, defaulted, schemad, present in config.example.yaml:41-45 and present in the chart values. Implementing them costs ZERO config-shape seams. Rejecting them would need a new validation error, new tests, and a doc change — MORE work, for the strictly worse outcome of a Headscale deployment with no retry.

`internal/hsapi` today: `NewClient` (client.go:96) builds `httpguard.NoRedirectClient(&http.Client{Timeout: opts.Timeout})` and nothing else. `Options` (client.go:29) carries only URL, APIKey, Timeout, MaxResponseBytes, Tracer, OnRequest. The package doc (client.go:1-4) says outright "without retry (small tailnets; retry is a noted follow-up)". `hsapi.RequestInfo` (client.go:60) deliberately omits `Attempts` and `WaitDuration` with a comment saying hsapi never retries — that comment becomes false and must be corrected in the same change.

## What is reusable, and what is not

`internal/tsapi/ratelimit.go` is 113 lines and is **entirely provider-neutral**: `limiter` (token bucket), `Wait`, `rateWaiter`, `rateLimitTransport`, `wrapRateLimit`, `newRateWaiter`. Nothing in it mentions Tailscale. It is a clean lift.

`internal/tsapi/transport.go` is 659 lines and is mostly NOT neutral: `endpointLabel` with its `collectionsWithVarSegment` elision (transport.go:565-625), OAuth `RetrieveError` classification (`classifyOAuthRetrieveError`, transport.go:505; `rfc6749ErrorCodes`, transport.go:419), and tsapi-specific logging. Only three small pure functions are neutral: `computeBackoff` (transport.go:627), `retryAfter` (transport.go:643), `retryableOutcome` (transport.go:331).

**Do NOT attempt a full extraction of `retryTransport`.** It is the most security- and correctness-sensitive code in the repo (per-attempt timeout semantics, rate-limit wait excluded from that timeout, Retry-After clamped to maxDelay per #206, terminal-auth-costs-one-attempt per #489) and it is heavily tested. Refactoring it wholesale inside a bug-fix wave trades a large regression risk for no user-visible gain.

Wave 1 Lane A2 started by root at 268fc93 after Config Freeze f54548a; composition-root wiring remains root-owned W1.

Negative test evidence: temporarily removing HTTP 429 from RetryableOutcome made TestClientRetriesTooManyRequests fail with status 429; restored behavior passes. Rate-limit wait, Retry-After clamp, cancellation, attempts, and self-observability wiring passed just check and CI 33312668201.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Landed 7292f5a with root wiring in 1de673f: Headscale now honors retry and rate-limit config using provider-neutral primitives while preserving tsapi structure. Verified by focused race tests, full gate, and CI.
<!-- SECTION:FINAL_SUMMARY:END -->
