package objectstore

import (
	"strings"
	"time"
)

const (
	// selfDatedKeyLayout is what Tailscale's publisher ACTUALLY writes, verified
	// against a live export on 2026-07-27 for both log types (#288):
	//
	//	<prefix>/YYYY/MM/DD/YYYY-MM-DD-HH-MM-SS.ndjson[.zst|.gz]
	//
	// The date appears twice — once in the partition directories and again in a
	// self-contained basename — which is why a flattened copy of an export is
	// still parseable. This is tried FIRST because it is the common case.
	//
	// It was previously called legacyKeyLayout, and timeOnlyKeyLayout below was
	// called officialKeyLayout, which had it exactly backwards.
	selfDatedKeyLayout = "2006-01-02-15-04-05"
	// timeOnlyKeyLayout is the form Tailscale's DOCUMENTATION describes, where the
	// basename carries only the UTC time and its date comes from the three
	// partition directories above it:
	//
	//	<prefix>/YYYY/MM/DD/HH:MM:SS.json[.zst|.gz]
	//
	// The live export has never been observed writing this, but accepting it costs
	// nothing and refusing it would silently skip real data if the publisher ever
	// switches to it.
	timeOnlyKeyLayout = "2006/01/02/15:04:05"
	// recorderKeyLayout is the basename format tsrecorder writes. time.RFC3339Nano
	// parses all trailing-zero-trimmed widths, so one layout covers 1-9 digits.
	recorderKeyLayout = time.RFC3339Nano
)

// recorderSuffixes are the object kinds the recorder layout accepts. tsrecorder
// writes no compressed objects, so there is no codec grid here; a compressed
// copy would need its own entries and a matching decode path.
var recorderSuffixes = []struct {
	ext  string
	comp compression
}{
	{".event", compNone},
	{".cast", compNone},
}

// maxClockSkew is how far ahead of this process's clock an object's basename
// timestamp may be and still be treated as real data. It is a fixed constant
// rather than a config knob: it exists to absorb ordinary NTP drift between the
// exporter and this process, not to be tuned, and an operator raising it would
// only widen the window in which the failure below can happen.
//
// The cursor doubles as the listing lower bound, so a timestamp accepted from
// the future moves the window past the wall clock and the next cycle derives no
// day prefixes at all — ingestion stops until the clock catches up, which for a
// key named hours or years ahead means indefinitely. Anything beyond the
// allowance is therefore skipped BEFORE it can reach the cursor, the seen set,
// or the lower bound.
//
// Skipping is not permanent. Nothing about the object is recorded, so once the
// clock reaches its timestamp a later cycle ingests it like any other object.
const maxClockSkew = 5 * time.Minute

// isFutureKey reports whether at is far enough ahead of now to be rejected.
// Equality with the boundary is accepted: the allowance is inclusive so that a
// host exactly maxClockSkew fast is still ordinary data.
//
// This is applied during enumeration, above every backend, so the policy is the
// same whether the bucket is S3, GCS via its S3 interop endpoint, or Azure
// behind an S3-compatible gateway.
func isFutureKey(at, now time.Time) bool { return at.After(now.Add(maxClockSkew)) }

// compression is how an object's bytes are encoded, decided by its extension.
type compression int

const (
	compNone compression = iota
	compZstd
	compGzip
)

// suffixes maps each recognized extension to its codec. Tailscale writes zstd;
// gzip is here because operators routinely re-compress an export while copying
// it between buckets. All five codec spellings (bare, .zst, .zstd, .gz, .gzip)
// are listed for BOTH stems, because #496 was this list going out of sync: the
// long .zstd/.gzip forms existed for .ndjson but not .json, so such an object
// parsed as unrecognized and was silently skipped instead of fetched.
// TestParseKey_StemCodecCrossProduct enumerates the whole stem x codec grid, so
// adding a stem or a spelling to one family and not the other fails loudly.
//
// Ordering rule: parseKey returns on the FIRST entry whose extension matches and
// never falls through, so if one extension is ever a literal byte-suffix of
// another, the longer must be listed first. Nothing here is in that relationship
// today (.zst/.zstd and .gz/.gzip differ in their final byte, and the stem keeps
// the families apart), but check it before adding an entry.
var suffixes = []struct {
	ext  string
	comp compression
}{
	{".ndjson.zst", compZstd},
	{".ndjson.zstd", compZstd},
	{".ndjson.gz", compGzip},
	{".ndjson.gzip", compGzip},
	{".ndjson", compNone},
	// The export is NDJSON regardless of extension, and some copies land as
	// .json or .log. Accepting them costs nothing and refusing them silently
	// skips real data.
	{".json.zst", compZstd},
	{".json.zstd", compZstd},
	{".json.gz", compGzip},
	{".json.gzip", compGzip},
	{".json", compNone},
}

// parseKey reads the object's own timestamp and codec out of its name.
//
// What the live export writes (both log types, verified 2026-07-27):
//
//	<prefix>/YYYY/MM/DD/<YYYY-MM-DD-HH-MM-SS>.ndjson[.zst|.zstd|.gz|.gzip]
//
// Also accepted — the documented time-only basename, and flattened copies:
//
//	<prefix>/YYYY/MM/DD/HH:MM:SS.json[.zst|.gz]
//
// The timestamp is what the cursor is compared against, so a key that does not
// carry one is reported as unparseable rather than defaulted to a time — an
// object assumed to be "now" would be ingested and then never re-examined, and
// one assumed to be zero would be skipped forever.
//
// layout selects which basename grammar and suffix set applies. There is no
// implicit default: every caller has a Layout in hand (c.opts.Layout), and an
// implicit fallback here is exactly how the wrong suffix set would get applied
// silently to a layout it was never verified against.
func parseKey(key string, layout Layout) (at time.Time, comp compression, ok bool) {
	if layout == LayoutRecorder {
		return parseRecorderKey(key)
	}
	base := key
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	for _, s := range suffixes {
		stem, found := strings.CutSuffix(base, s.ext)
		if !found {
			continue
		}
		// The self-dated basename is what the live export writes, and it is
		// self-contained, so try it first: it also keeps a flattened copy of an
		// export parseable.
		if t, err := time.Parse(selfDatedKeyLayout, stem); err == nil {
			return t.UTC(), s.comp, true
		}
		// A time-only basename takes its date from the final three directory
		// components immediately above it; any earlier components are the
		// operator-selected key prefix.
		parts := strings.Split(strings.TrimSuffix(key, "/"), "/")
		if len(parts) < 4 {
			return time.Time{}, compNone, false
		}
		stamp := strings.Join(parts[len(parts)-4:len(parts)-1], "/") + "/" + stem
		t, err := time.Parse(timeOnlyKeyLayout, stamp)
		if err != nil {
			return time.Time{}, compNone, false
		}
		return t.UTC(), s.comp, true
	}
	return time.Time{}, compNone, false
}

// parseRecorderKey reads a tsrecorder object's timestamp out of its basename.
//
// Verified against a live bucket on 2026-07-29, the layout is:
//
//	<stableID>/events/<RFC3339Nano>.event   -- one Kubernetes API request
//	<stableID>/<RFC3339Nano>.cast           -- one terminal session
//
// There is no date partition to fall back on and no alternate basename shape,
// unlike the export layouts above: a key that fails to parse is unrecognized,
// not defaulted.
func parseRecorderKey(key string) (at time.Time, comp compression, ok bool) {
	base := key
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	for _, s := range recorderSuffixes {
		stem, found := strings.CutSuffix(base, s.ext)
		if !found {
			continue
		}
		t, err := time.Parse(recorderKeyLayout, stem)
		if err != nil {
			return time.Time{}, compNone, false
		}
		return t.UTC(), s.comp, true
	}
	return time.Time{}, compNone, false
}

// dayPrefixes lists the day-partition prefixes covering [from, to] inclusive, in
// chronological order.
//
// Enumerating days rather than listing the whole prefix is what keeps a first
// run against a bucket holding months of history from listing all of it: S3
// charges and paginates per 1,000 keys, and a year of exports is six figures of
// keys to walk before reaching the recent ones that matter.
//
// The span is capped so a corrupt or absurd cursor cannot turn one cycle into
// thousands of list calls.
func dayPrefixes(base string, from, to time.Time, maxDays int) []string {
	if to.Before(from) {
		return nil
	}
	base = scanBase(base)
	from, to = from.UTC(), to.UTC()

	// Walk backwards from the newest day so that a capped span keeps the RECENT
	// days, not the oldest ones — falling behind must not mean falling behind
	// forever on the same stale window.
	var out []string
	day := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)
	first := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	for !day.Before(first) && len(out) < maxDays {
		p := day.Format("2006/01/02") + "/"
		if base != "" {
			p = base + "/" + p
		}
		out = append(out, p)
		day = day.AddDate(0, 0, -1)
	}
	// Reverse into chronological order: objects must be ingested oldest first so
	// the cursor only ever moves forward over ground actually covered.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// scanBase turns a configured object-store prefix into the base every listing
// prefix is built from, by trimming exactly one trailing slash.
//
// A LEADING slash is deliberately preserved. It is meaningless in an S3 key
// prefix, but it is part of the value that goes into the feed digest keying the
// frozen #453 checkpoint namespace, and that digest applies no normalization. So
// trimming it here would move an existing "/flow" deployment's cursor and seen
// set, cold-start it from initial_lookback and re-emit everything it had already
// ingested — a wasted-LIST-calls bug turned into a data-duplication bug (#498).
//
// This is the ONE definition. dayPrefixes builds day partitions from it,
// flatPrefix builds the single flat prefix from it, and isConfiguredDayPrefix
// recognizes a persisted row against it. When those disagreed, a persisted scan
// position was classified stale and DELETED on the cycle that loaded it, so
// enumeration restarted at the beginning of every day partition forever.
func scanBase(prefix string) string { return strings.TrimSuffix(prefix, "/") }

// MaxDayPrefixes is how many day partitions the partitioned layout enumerates,
// and therefore how far back it can EVER reach: today plus the previous
// MaxDayPrefixes-1 days.
//
// It is exported because it is an operator-facing bound that has to be stated
// somewhere an operator will see it, and internal/config is the only place that
// can: a configured initial_lookback beyond this ceiling silently ingests only the
// most recent partitions, producing no gap, no error and no metric because the
// older objects are never listed at all (#463). config cannot import this package
// (the dependency runs the other way), so it holds its own copy of the bound and a
// test asserts the two still agree.
const MaxDayPrefixes = maxDayPrefixes
