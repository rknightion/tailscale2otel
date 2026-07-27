package objectstore

import (
	"strings"
	"testing"
	"time"
)

// Every spelling of one configured prefix. A leading slash is meaningless in an
// S3 key prefix but nothing rejects it, so all four reach the engine.
var scanPrefixSpellings = []string{"flow", "/flow", "flow/", "/flow/", "", "/"}

// The prefix a cycle LISTS and the prefix a persisted scan row is recognized
// under must be the same string. When they disagree, loadScanState classifies the
// row stale and Collect DELETES it, so the position is not merely ignored — it is
// erased on the cycle that loads it. Partitioned enumeration then restarts at the
// beginning of every day partition forever, and a backlog wider than one page can
// never advance, with every health signal staying green (#498).
//
// This is a round-trip assertion rather than a comparison of two hand-written
// trimming expressions, so it fails again if the builder and the predicate ever
// diverge for any reason.
func TestScanPrefixRoundTrip_EverySpellingOfAConfiguredPrefix(t *testing.T) {
	from := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

	for _, base := range scanPrefixSpellings {
		t.Run("partitioned base="+base, func(t *testing.T) {
			prefixes := dayPrefixes(base, from, to, maxDayPrefixes)
			if len(prefixes) == 0 {
				t.Fatal("dayPrefixes returned nothing; the fixture window covers three days")
			}
			for _, p := range prefixes {
				if !isConfiguredScanPrefix(p, base, LayoutPartitioned) {
					t.Errorf("cycle lists %q but a scan row for it is disowned and DELETED "+
						"— durable listing progress can never be retained", p)
				}
			}
		})

		t.Run("flat base="+base, func(t *testing.T) {
			p := flatPrefix(base)
			if !isConfiguredScanPrefix(p, base, LayoutFlat) {
				t.Errorf("cycle lists %q but a scan row for it is disowned and DELETED", p)
			}
		})
	}
}

// A prefix that is NOT the configured one must still be disowned — that pruning is
// what makes switching layout safe in both directions, so the fix must not turn
// the predicate into an accept-all.
func TestScanPrefixRoundTrip_ForeignPrefixesAreStillStale(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		base   string
		layout Layout
	}{
		{"other base", "audit/2026/07/24/", "flow", LayoutPartitioned},
		{"flat row under partitioned", "flow/", "flow", LayoutPartitioned},
		{"day row under flat", "flow/2026/07/24/", "flow", LayoutFlat},
		{"not a date", "flow/2026/13/45/", "flow", LayoutPartitioned},
		{"truncated date", "flow/2026/07/", "flow", LayoutPartitioned},
		{"base is a path prefix but not a segment", "flowlogs/2026/07/24/", "flow", LayoutPartitioned},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if isConfiguredScanPrefix(tc.prefix, tc.base, tc.layout) {
				t.Errorf("prefix %q was accepted for base %q under %v; a foreign row must be pruned",
					tc.prefix, tc.base, tc.layout)
			}
		})
	}
}

// The durable checkpoint namespace is keyed by FeedID, which is a plain digest
// over its parts with NO normalization (#453). So the fix must not normalize the
// configured prefix: changing the digest for a deployment configured "/flow"
// would orphan its cursor and its entire seen set, cold-start from
// initial_lookback and re-emit everything it had already ingested. Distinct
// spellings therefore stay distinct feeds, and this test is what stops a later
// "tidy the prefix up" change from silently causing duplicate emission.
func TestFeedID_PrefixSpellingsStayDistinctFeeds(t *testing.T) {
	seen := map[string]string{}
	for _, base := range []string{"flow", "/flow", "flow/", "/flow/"} {
		id := FeedID("https://s3.example", "bucket", base)
		if prev, dup := seen[id]; dup {
			t.Errorf("FeedID(%q) == FeedID(%q): the prefix is being normalized, which moves the "+
				"checkpoint namespace of an existing deployment and re-emits its history", base, prev)
		}
		seen[id] = base
	}
	// Pinned so an accidental change to the digest inputs is loud rather than
	// merely "still distinct". Any change here orphans every deployment's state.
	const wantFlow = "95c95b4d63391724eb711bf2"
	if got := FeedID("https://s3.example", "bucket", "flow"); got != wantFlow {
		t.Errorf("FeedID(endpoint, bucket, \"flow\") = %q, want the frozen %q — changing the feed "+
			"digest orphans every deployment's cursor and seen set", got, wantFlow)
	}
}

// The three places that turn a configured base into a listing prefix must agree on
// how the base is trimmed. Guarding the shared helper directly keeps a future
// fourth caller from reintroducing its own trimming expression.
func TestScanBase_TrimsOnlyTheTrailingSlash(t *testing.T) {
	cases := map[string]string{
		"flow":   "flow",
		"flow/":  "flow",
		"/flow":  "/flow",
		"/flow/": "/flow",
		"":       "",
		"/":      "",
	}
	for in, want := range cases {
		if got := scanBase(in); got != want {
			t.Errorf("scanBase(%q) = %q, want %q — a LEADING slash is part of the configured "+
				"prefix and part of the feed digest, so it must not be trimmed", in, got, want)
		}
	}
	// Consistency with the day-prefix builder, which is the string the predicate
	// has to recognize.
	for _, base := range scanPrefixSpellings {
		want := scanBase(base)
		got := dayPrefixes(base, time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC), 1)
		if len(got) != 1 {
			t.Fatalf("dayPrefixes(%q) = %v, want one day", base, got)
		}
		if want != "" && !strings.HasPrefix(got[0], want+"/") {
			t.Errorf("dayPrefixes(%q) = %q, want it under scanBase %q", base, got[0], want)
		}
		if want == "" && got[0] != "2026/07/24/" {
			t.Errorf("dayPrefixes(%q) = %q, want a bare day partition", base, got[0])
		}
	}
}
