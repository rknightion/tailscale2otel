package objectstore

import (
	"slices"
	"testing"
	"time"
)

func TestParseKey(t *testing.T) {
	want := time.Date(2026, 7, 24, 9, 5, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		key      string
		wantAt   time.Time
		wantComp compression
		wantOK   bool
	}{
		{
			name:   "official uncompressed layout",
			key:    "flow/2026/07/24/09:05:00.json",
			wantAt: want,
			wantOK: true,
		},
		{name: "official zstd layout", key: "flow/2026/07/24/09:05:00.json.zst", wantAt: want, wantComp: compZstd, wantOK: true},
		{name: "official gzip layout", key: "flow/2026/07/24/09:05:00.json.gz", wantAt: want, wantComp: compGzip, wantOK: true},
		{
			name:     "legacy date-bearing basename",
			key:      "flow/2026/07/24/2026-07-24-09-05-00.ndjson.zst",
			wantAt:   want,
			wantComp: compZstd,
			wantOK:   true,
		},
		{name: "long zstd extension", key: "flow/2026/07/24/2026-07-24-09-05-00.ndjson.zstd", wantAt: want, wantComp: compZstd, wantOK: true},
		{name: "gzip", key: "flow/2026/07/24/2026-07-24-09-05-00.ndjson.gz", wantAt: want, wantComp: compGzip, wantOK: true},
		{name: "long gzip extension", key: "flow/2026/07/24/2026-07-24-09-05-00.ndjson.gzip", wantAt: want, wantComp: compGzip, wantOK: true},
		{name: "uncompressed", key: "flow/2026/07/24/2026-07-24-09-05-00.ndjson", wantAt: want, wantOK: true},
		{
			// A copy that landed as .json is still NDJSON, and refusing it would
			// silently skip real data.
			name: "json extension", key: "flow/2026/07/24/2026-07-24-09-05-00.json", wantAt: want, wantOK: true,
		},
		{
			// The retained legacy basename is self-contained. This only pins
			// parser compatibility; bounded discovery of a flat bucket is the
			// separate end-to-end contract in #293.
			name: "legacy basename is self-contained", key: "2026-07-24-09-05-00.ndjson", wantAt: want, wantOK: true,
		},
		{
			// Deliberately NOT taken from the directories: a mismatch means
			// something has rewritten the layout, and the basename is the one the
			// exporter chose.
			name: "day directories disagree with the stem", key: "flow/2020/01/01/2026-07-24-09-05-00.ndjson", wantAt: want, wantOK: true,
		},
		{name: "no timestamp", key: "flow/2026/07/24/manifest.ndjson", wantOK: false},
		{name: "official basename copied flat loses its date", key: "09:05:00.json", wantOK: false},
		{name: "unknown extension", key: "flow/2026/07/24/2026-07-24-09-05-00.parquet", wantOK: false},
		{name: "no extension", key: "flow/2026/07/24/2026-07-24-09-05-00", wantOK: false},
		{name: "empty", key: "", wantOK: false},
		{name: "directory marker", key: "flow/2026/07/24/", wantOK: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at, comp, ok := parseKey(tc.key)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if !at.Equal(tc.wantAt) {
				t.Errorf("at = %v, want %v", at, tc.wantAt)
			}
			if comp != tc.wantComp {
				t.Errorf("compression = %v, want %v", comp, tc.wantComp)
			}
		})
	}
}

// The stem is parsed as UTC. Reading it as local time would shift every object
// by the operator's offset and put half of them outside the cursor window.
func TestParseKey_IsUTC(t *testing.T) {
	at, _, ok := parseKey("2026-07-24-09-05-00.ndjson")
	if !ok {
		t.Fatal("not parsed")
	}
	if _, offset := at.Zone(); offset != 0 {
		t.Errorf("zone offset = %d, want UTC", offset)
	}
}

func TestDayPrefixes(t *testing.T) {
	from := time.Date(2026, 7, 22, 23, 30, 0, 0, time.UTC)
	to := time.Date(2026, 7, 24, 0, 30, 0, 0, time.UTC)

	got := dayPrefixes("flow", from, to, 14)
	want := []string{"flow/2026/07/22/", "flow/2026/07/23/", "flow/2026/07/24/"}
	if !slices.Equal(got, want) {
		t.Errorf("prefixes = %v, want %v", got, want)
	}
}

// Objects must be ingested oldest first, so the enumeration that feeds it has to
// be chronological.
func TestDayPrefixes_AreChronological(t *testing.T) {
	got := dayPrefixes("flow", time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC), 14)
	if !slices.IsSorted(got) {
		t.Errorf("prefixes = %v, want ascending", got)
	}
}

// A capped span must keep the RECENT days. Keeping the oldest would leave a
// process that has fallen behind stuck re-listing the same stale window forever.
func TestDayPrefixes_CapKeepsTheNewestDays(t *testing.T) {
	got := dayPrefixes("flow", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC), 3)
	want := []string{"flow/2026/07/22/", "flow/2026/07/23/", "flow/2026/07/24/"}
	if !slices.Equal(got, want) {
		t.Errorf("prefixes = %v, want the three most recent days %v", got, want)
	}
}

func TestDayPrefixes_EdgeCases(t *testing.T) {
	day := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

	if got := dayPrefixes("flow", day, day.Add(-time.Hour), 14); got != nil {
		t.Errorf("reversed range = %v, want nothing", got)
	}
	if got := dayPrefixes("", day, day, 14); !slices.Equal(got, []string{"2026/07/24/"}) {
		t.Errorf("no prefix = %v, want an unrooted day path", got)
	}
	if got := dayPrefixes("flow/", day, day, 14); !slices.Equal(got, []string{"flow/2026/07/24/"}) {
		t.Errorf("trailing slash = %v, want it not doubled", got)
	}
	// Month and year rollovers are where a naive day-adding loop breaks.
	got := dayPrefixes("f", time.Date(2025, 12, 31, 23, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC), 14)
	if !slices.Equal(got, []string{"f/2025/12/31/", "f/2026/01/01/"}) {
		t.Errorf("year rollover = %v", got)
	}
}
