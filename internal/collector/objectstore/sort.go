package objectstore

import (
	"sort"
	"time"
)

// sortCandidates orders objects chronologically, then by key so the order is
// total and reproducible. Ingesting oldest-first is what lets the cursor advance
// to the newest ingested object without ever skipping ground in between.
func sortCandidates(in []candidate) {
	sort.SliceStable(in, func(i, j int) bool {
		if !in[i].at.Equal(in[j].at) {
			return in[i].at.Before(in[j].at)
		}
		return in[i].obj.Key < in[j].obj.Key
	})
}

// seenEntry is one row of the durable seen set.
type seenEntry struct {
	key string
	at  time.Time
}

// sortEntriesByTime orders oldest first, so the hard cap on the seen set evicts
// the entries least likely to still be re-listable.
func sortEntriesByTime(in []seenEntry) {
	sort.SliceStable(in, func(i, j int) bool { return in[i].at.Before(in[j].at) })
}
