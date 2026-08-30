---
id: TSO-0029
title: Fix stale single-tailnet receiver claim in config.example.yaml
status: To Do
assignee: []
created_date: '2026-08-30 08:45'
updated_date: '2026-08-30 10:03'
labels: []
milestone: m-1
dependencies: []
priority: low
type: bug
ordinal: 32000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
config.example.yaml:98-99 states streaming/webhook receivers require single-tailnet mode, but streaming.routes and webhook.routes (config.example.yaml:535-537, 553-555) provide explicit multi-tailnet receiver routing, and docs/configuration.md:433 documents routes as the multi-tailnet path. Likely a leftover comment from before routes landed. Suspected during a product-surface review (2026-08-30), not yet proven — verify what the loader/validator actually enforces before editing, then fix whichever side is wrong. Note docs/env-vars.md is generated from config.example.yaml comments, so regenerate after the edit (just gen-envref).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The example config, docs/configuration.md and actual validation behaviour agree on multi-tailnet receiver support
- [ ] #2 Generated docs regenerated if the example config comments change
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Rewrite `config.example.yaml:98-99` to state the real rule. Suggested wording, kept inside the existing comment block so the env-ref generator picks it up unchanged in shape:
   `# Streaming/webhook receivers work here, but a list of MORE THAN ONE tailnet must route them explicitly: set streaming.routes / webhook.routes (also FILE-ONLY), each naming one tailnets[].name. A list of exactly one entry may still use the legacy single-listener fields.`
2. Do NOT edit docs/configuration.md:429 — it is already correct.
3. `just gen-envref` (docs/env-vars.md is generated from these comments) and then `just gen` to confirm no other artifact moved.
4. Add a validator test ONLY if the existing coverage does not already pin all three branches. `internal/config/multitailnet_test.go:159` already asserts the streaming-requires-routes error, and `internal/config/receiver_routes_test.go` covers the routes path; check whether the ACCEPTED single-entry-list case (`len(Tailnets) == 1`, `streaming.enabled`, no routes) is pinned anywhere. If it is not, add that one case — it is the branch the stale comment claimed was impossible and the one a future "simplification" would delete.
5. Gate: `just check`. TDD note — the substantive change is a comment, so validation (regenerate + the existing validator tests) replaces a new unit test, except for the one missing-branch test in step 4.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
## Research 2026-08-30 (Wave 1 planning, HEAD 1dd76a9) — CONFIRMED; the comment is the wrong side

`config.example.yaml:98` reads "Streaming/webhook receivers require single-tailnet mode (use the tailscale: block instead)." The validator says otherwise. From `internal/config/validate.go`, `validateReceiverRoutes` (line 140):

- `multi := len(c.Tailnets) > 0` (line 141).
- `streaming.routes` non-empty + `!multi` -> error "streaming.routes requires configured multi-tailnet mode (tailnets: list)" (line 150). Same for `webhook.routes` (line 203).
- `len(c.Tailnets) > 1 && c.Streaming.Enabled` with NO routes -> error "streaming.enabled in multi-tailnet mode requires streaming.routes" (line 198). Same for webhook (line 240).

So receivers are fully supported in multi-tailnet mode — via `routes:` — and `docs/configuration.md:429` already documents that correctly ("Multi-tailnet receivers use explicit routes"). The example config comment is a leftover from before routes landed.

## Precision the fix must keep (the >1 vs >0 asymmetry)

The two guards use DIFFERENT thresholds and this is load-bearing, not a typo to smooth over:

- A `tailnets:` list of exactly ONE entry with `streaming.enabled: true` and no routes is ACCEPTED — `len(c.Tailnets) > 1` is false — and keeps the legacy single-listener identity fields (`streaming.path/token/public_url/auto_configure`).
- Two or more entries with a receiver enabled and no routes is a hard error.
- Routes at all require `len(c.Tailnets) > 0`.

A replacement comment saying only "receivers work in multi-tailnet mode" loses that. Write the rule, not a vibe.

Also note the mutual-exclusion rules the comment could usefully carry: with routes set, `streaming.path` must stay at its default and `token`/`token_file`/`public_url`/`auto_configure` must be empty (validate.go:155-166); `webhook.path` must stay default and `secret`/`secret_file` empty (validate.go:207-213); webhook routes cannot mix tokenless and signed routes on one listener (line 236).
<!-- SECTION:NOTES:END -->
