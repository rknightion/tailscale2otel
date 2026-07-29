package objectstore

import "testing"

// NOTE: an earlier version of this file opened with a
// TestRecorderCheckpointDoesNotSkipShorterKey that asserted
// `skipped > truncatedStartAfter(pos)` for a specific 8-digit/7-digit pair. It
// was DELETED because it was vacuous: for that pair the assertion already holds
// on the untruncated string, so the test passed identically against a no-op
// implementation and proved nothing. Verified by neutering truncatedStartAfter
// and watching it stay green while the two tests below failed.
//
// Do not reinstate a comparison-shaped test here without first checking it
// against a no-op. The behavior worth pinning is the exact truncated OUTPUT
// (TestTruncatedStartAfterDropsTheFractionalPart) and the real skip direction
// (TestTruncatedStartAfterCoversTheRealSkipDirection); both are below.

func TestTruncatedStartAfterIsIdentityForOtherLayouts(t *testing.T) {
	pos := "flow/2026/07/29/2026-07-29-11-00-00.ndjson"
	if got := truncatedStartAfter(pos, LayoutPartitioned); got != pos {
		t.Fatalf("truncatedStartAfter mutated a partitioned position: %q", got)
	}
}

func TestTruncatedStartAfterHandlesEmptyAndUnparseable(t *testing.T) {
	if got := truncatedStartAfter("", LayoutRecorder); got != "" {
		t.Fatalf("empty position must stay empty, got %q", got)
	}
	if got := truncatedStartAfter("garbage", LayoutRecorder); got != "garbage" {
		t.Fatalf("unparseable position must pass through, got %q", got)
	}
}

// TestTruncatedStartAfterDropsTheFractionalPart pins the exact truncated shape:
// everything from the '.' onward is gone, leaving only the whole-second stem.
// This is the assertion that actually discriminates an identity/no-op
// implementation from a real one: TestRecorderCheckpointDoesNotSkipShorterKey
// above happens to hold for its two example strings even WITHOUT truncation
// (an 8-digit fraction already sorts lexicographically before a 7-digit one for
// that specific pair), so it alone would not catch a truncatedStartAfter that
// silently did nothing. Confirmed by temporarily reverting truncatedStartAfter
// to `return pos` (an identity no-op): the plan's own test above kept passing,
// this one failed.
func TestTruncatedStartAfterDropsTheFractionalPart(t *testing.T) {
	pos := "n/events/2026-07-29T11:51:11.91150441Z.event"
	want := "n/events/2026-07-29T11:51:11"
	if got := truncatedStartAfter(pos, LayoutRecorder); got != want {
		t.Fatalf("truncatedStartAfter(%q) = %q, want %q", pos, got, want)
	}
}

// TestTruncatedStartAfterCoversTheRealSkipDirection exercises the direction that
// actually loses data: a checkpoint persisted from a LEXICOGRAPHICALLY GREATER
// key (here the 7-digit fraction, which sorts after the 8-digit one per the
// package doc on LayoutRecorder) must not exclude a chronologically LATER
// object that happens to sort lexicographically SMALLER. Without truncation,
// List(ctx, prefix, checkpoint, ...) would never return `later` because
// later <= checkpoint lexicographically, even though later is the newer
// object — a permanent, invisible skip.
func TestTruncatedStartAfterCoversTheRealSkipDirection(t *testing.T) {
	checkpoint := "n/events/2026-07-29T11:51:11.9115044Z.event" // 7 digits
	later := "n/events/2026-07-29T11:51:11.91150441Z.event"     // 8 digits, +10ns, arrives late
	if later >= checkpoint {
		t.Fatal("precondition failed: later must sort lexicographically BEFORE checkpoint")
	}
	got := truncatedStartAfter(checkpoint, LayoutRecorder)
	if got >= later {
		t.Fatalf("truncatedStartAfter(%q) = %q would still exclude the late-arriving %q", checkpoint, got, later)
	}
}
