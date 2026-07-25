package objectstore

import (
	"strings"
	"time"
)

// keyLayout is the timestamp format Tailscale names each exported object with.
// It is zero-padded throughout, which is what makes S3's lexicographic listing
// order chronological order too — the whole cursor scheme rests on that.
const keyLayout = "2006-01-02-15-04-05"

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
// it between buckets.
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
	{".json.gz", compGzip},
	{".json", compNone},
}

// parseKey reads the object's own timestamp and codec out of its name.
//
//	<prefix>/YYYY/MM/DD/<YYYY-MM-DD-HH-MM-SS>.ndjson[.zst|.zstd|.gz|.gzip]
//
// The timestamp is what the cursor is compared against, so a key that does not
// carry one is reported as unparseable rather than defaulted to a time — an
// object assumed to be "now" would be ingested and then never re-examined, and
// one assumed to be zero would be skipped forever.
//
// It reads the BASENAME only. The day-partition directories are redundant with
// the timestamp, and trusting them would break on a bucket whose objects were
// copied into a flat prefix.
func parseKey(key string) (at time.Time, comp compression, ok bool) {
	base := key
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	for _, s := range suffixes {
		stem, found := strings.CutSuffix(base, s.ext)
		if !found {
			continue
		}
		t, err := time.Parse(keyLayout, stem)
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
	base = strings.TrimSuffix(base, "/")
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
