---
id: TSO-0021
title: >-
  Update check fails permanently: the 64 KiB body cap truncates the GitHub
  latest-release response
status: Done
assignee:
  - '@claude'
created_date: '2026-08-27 16:33'
updated_date: '2026-08-27 16:47'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 24000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The self update-availability check (#330) has been broken for every v4 user since the v4.0.0 release was published, and will stay broken.

internal/release/release.go:317 reads the response through io.LimitReader(resp.Body, 1<<16) — a 65536-byte cap. The GitHub /repos/rknightion/tailscale2otel/releases/latest response is 74006 bytes, because the release body carries the whole 4.0.0 changelog. The read stops mid-string, json.Unmarshal in ParseGitHubLatest fails, and fetch() wraps it as errParse.

The observable result is Snapshot.ErrClass=parse_error, update state=error, and no latest_version at all, so the status page (#330) can never report that an upgrade exists. It fails open, so nothing else degrades — which is exactly why it went unnoticed.

Live-verified 2026-08-27 on the lab deployment: before the v4.0.0 release was published the same binary returned state=current with latest_version=3.0.0 (the v3.0.0 release body was under the cap); after publication the same process reports state=error / parse_error. Reproduced independently against the public API: full 74006 bytes parses and yields tag_name=v4.0.0, the first 65536 bytes fail with 'Unterminated string starting at line 546'.

This is not a one-off. Any release whose notes push the JSON past the cap reproduces it, and release-please generates large bodies by design, so raising the constant alone only moves the cliff.

The cap itself is a deliberate defence against an unbounded body and must not simply be removed. The fix needs to decide how to get a bounded read that still parses — candidates include a streaming json.Decoder that stops once tag_name is decoded, or asking GitHub for a smaller representation. That choice is the work; do not assume one.

Note the same Fetcher also serves TailscalePkgsURL (pkgs.tailscale.com/stable/?mode=json) with a different Parser. Any change has to hold for both, and that manifest is the larger long-term truncation risk of the two.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 internal/release resolves the current GitHub latest-release response (>=74 KiB) to tag_name v4.0.0 without error
- [x] #2 A response body larger than the retained read limit still parses, or fails with a class that is distinguishable from a malformed body
- [x] #3 The reader remains bounded: an unbounded or hostile response body cannot be read into memory in full
- [x] #4 The Tailscale pkgs manifest parser is exercised against a body exceeding the same limit
- [x] #5 A regression test pins the oversized-body case using a fixture, not a live network call
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 go build ./... && go vet ./... && go test -race ./...
- [ ] #2 golangci-lint run
- [ ] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Add scanTopLevelString(r io.Reader, key string): a streaming json.Decoder token loop that tracks depth and returns as soon as the top-level key's string value is decoded, so the read stops within the first few hundred bytes rather than consuming the whole body.
2. Change Parser from func([]byte) to func(io.Reader), and rewrite ParseGitHubLatest and ParseTailscalePkgs over the shared helper. Both extract a single top-level string field, so one helper serves both.
3. In fetch(), stop buffering with io.ReadAll. Pass a bounded reader straight to f.parse and keep a hard ceiling as a backstop against an unbounded body; raise it well above any plausible release payload since it no longer decides success.
4. Distinguish truncation from malformed JSON: track whether the bound was actually exhausted and classify that as its own error class rather than parse_error.
5. Regression tests from fixtures, no network: an oversized GitHub body that still resolves tag_name; an oversized Tailscale pkgs body; a body whose key never appears within the bound, asserting the truncation class; keep the existing empty-value cases.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Root cause confirmed: release.go read the response through io.LimitReader(resp.Body, 1<<16) and then json.Unmarshal'd the whole document. The v4.0.0 GitHub response is 74006 bytes, so the buffer ended mid-string and every check failed as parse_error.

Fix: Parser changed from func([]byte) to func(io.Reader), and both parsers now go through scanTopLevelString — a json.Decoder token loop over the top-level object that returns the moment the wanted key's value is decoded. Unwanted values are consumed whole by a single Decode, so a nested occurrence of the key (a GitHub release embeds author/assets/reactions objects) can never match.

The read stays bounded, by limitedReader rather than io.LimitReader: the ceiling moved to 4 MiB, and the reader records whether the bound was actually exhausted. That distinction is the whole point of the new class — io.LimitReader reports a truncated stream as a plain EOF, which is indistinguishable from a body that simply ended, and the two call for opposite operator responses. classify() gained "truncated".

Measured: the parser reads 127 bytes of a 6291588-byte fixture to resolve tag_name.

Negative-tested, which took two attempts. The first revert kept the streaming parser and only one test regressed — not a faithful check. Reverting BOTH the buffered 64 KiB read and the whole-document Unmarshal made the two fetch-level tests fail with exactly the production symptom (ErrClass=parse_error), and both pass with the fix.

No doc or schema change needed, checked rather than assumed: docs/api/schemas/status.schema.json types update.last_error_class as a plain string with no enum, and the enumerated last_error_class list in docs/architecture.md:306 belongs to delivery[], a different set. Snapshot.ErrClass's own doc comment did need the new value and got it.

CodeRabbit: 2 minor findings, both applied. The ErrClass doc comment omitted "truncated"; and the oversized fixtures only exceeded the OLD 64 KiB cap, so they were raised past the CURRENT 4 MiB ceiling — which is what AC 2 actually asks for.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
The self update-check could never report an available upgrade on v4: fetch() buffered the response behind a 64 KiB io.LimitReader and json.Unmarshal'd it whole, and the v4.0.0 GitHub release is 74006 bytes because release-please puts the changelog in "body". The read stopped mid-string, so every check failed as parse_error, permanently and for every user, failing open so nothing else showed it.

Parser now takes an io.Reader, and both parsers share scanTopLevelString: a json.Decoder token loop over the top-level object that stops at the wanted key and consumes unwanted values whole, so a nested occurrence cannot match. The read is still bounded — a limitedReader with a 4 MiB ceiling that also records whether the bound was exhausted, which is what lets a too-big body be classified "truncated" instead of parse_error. Raising the constant alone was rejected: release bodies keep growing, so that only moves the cliff.

Verified: the real 74006-byte GitHub response resolves v4.0.0 (scratch check against a live fetch, not committed); the committed tests are fixture-only. Fetch-level tests serve a 6 MiB GitHub-shaped body and a 6 MiB pkgs body over httptest and resolve the version, so a body larger than the retained limit parses; a body whose key never appears yields ErrClass=truncated while a malformed one yields parse_error; nested and missing keys are pinned. The parser reads 127 of 6291588 bytes. Negative-tested against a faithful restoration of the old read AND the old whole-document parse: both fetch-level tests fail there with the production symptom. Full gate green — go build, go vet, go test -race ./..., golangci-lint 0 issues. CodeRabbit: 2 minor findings, both applied.
<!-- SECTION:FINAL_SUMMARY:END -->
