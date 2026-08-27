---
id: TSO-0021
title: >-
  Update check fails permanently: the 64 KiB body cap truncates the GitHub
  latest-release response
status: To Do
assignee: []
created_date: '2026-08-27 16:33'
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
- [ ] #1 internal/release resolves the current GitHub latest-release response (>=74 KiB) to tag_name v4.0.0 without error
- [ ] #2 A response body larger than the retained read limit still parses, or fails with a class that is distinguishable from a malformed body
- [ ] #3 The reader remains bounded: an unbounded or hostile response body cannot be read into memory in full
- [ ] #4 The Tailscale pkgs manifest parser is exercised against a body exceeding the same limit
- [ ] #5 A regression test pins the oversized-body case using a fixture, not a live network call
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 go build ./... && go vet ./... && go test -race ./...
- [ ] #2 golangci-lint run
- [ ] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->
