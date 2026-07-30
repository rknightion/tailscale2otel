package annotations

import (
	"strings"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/collector"
)

// dedupeStore is the persisted set of published annotation dedupe keys,
// key -> the event time it was claimed at.
//
// # Why its own file, not the collectors' checkpoint file
//
// It reuses collector's file store — already atomic, already sweeping staging
// files left by a hard kill — but points it at a SEPARATE path. The window
// pollers rewrite their checkpoint file on every tick and internal/app walks
// its keys at startup to migrate namespacing; adding thousands of annotation
// hashes to it would inflate both for no reason and put this package's keys in
// front of a migration that knows nothing about them.
//
// # Why it is persisted at all
//
// A set that lived only in memory would republish, on every restart,
// everything still inside the source collectors' overlap windows — which is
// precisely the duplicate this feature must not produce. Losing the file
// degrades to "may republish once", never to "publishes nothing", which is why
// a read failure here is not an error.
type dedupeStore struct {
	mu        sync.Mutex
	store     collector.CheckpointStore
	retention time.Duration
	// seen is the authoritative in-memory view. The backing store is written
	// in batches from Persist rather than per claim: a busy audit window would
	// otherwise rewrite the whole file once per record.
	seen  map[string]time.Time
	dirty map[string]time.Time
}

// dedupeKeyFor renders the persisted, rule-scoped form of a dedupe key.
// Prefixing rather than nesting keeps the on-disk shape a flat
// map[string]time.Time, which is what the file store already persists.
func dedupeKeyFor(ruleID, key string) string { return ruleID + "|" + key }

// newDedupeStore loads the persisted set. A nil store makes every claim succeed
// once per process and persist nothing, which is the correct degraded mode when
// no writable state path exists.
func newDedupeStore(store collector.CheckpointStore, retention time.Duration) *dedupeStore {
	d := &dedupeStore{
		store:     store,
		retention: retention,
		seen:      map[string]time.Time{},
		dirty:     map[string]time.Time{},
	}
	if store != nil {
		for _, key := range store.Keys() {
			if at, ok := store.Get(key); ok {
				d.seen[key] = at
			}
		}
	}
	return d
}

// Claim records (ruleID, key) as published and reports whether this caller won
// the claim. A second call with the same key returns false, which is the whole
// dedupe: the caller publishes only on true.
//
// eventTime is what the entry is evicted against, so it must be the SOURCE
// event time. Using arrival time would keep a re-delivered record's entry alive
// forever and expire a real event's too early.
func (d *dedupeStore) Claim(ruleID, key string, eventTime time.Time) bool {
	stored := dedupeKeyFor(ruleID, key)
	d.mu.Lock()
	defer d.mu.Unlock()
	if previous, ok := d.seen[stored]; ok {
		// Refresh the entry's clock on a re-delivery so a still-current
		// identity — a key that stays inside its expiry window for a fortnight
		// — cannot age out and be republished while it is still being observed
		// every tick.
		if eventTime.After(previous) {
			d.seen[stored] = eventTime
			d.dirty[stored] = eventTime
		}
		return false
	}
	d.seen[stored] = eventTime
	d.dirty[stored] = eventTime
	return true
}

// Persist flushes newly claimed keys and evicts everything older than the
// retention window, in ONE batch write. Eviction rides on the same write
// because the alternative is a second full rewrite of the file for no benefit.
//
// It is safe to call when nothing changed: an empty batch is not written.
func (d *dedupeStore) Persist(now time.Time) error {
	if d.store == nil {
		return nil
	}
	d.mu.Lock()
	updates := d.dirty
	d.dirty = map[string]time.Time{}
	cutoff := now.Add(-d.retention)
	var deletes []string
	for key, at := range d.seen {
		if at.Before(cutoff) {
			delete(d.seen, key)
			delete(updates, key)
			deletes = append(deletes, key)
		}
	}
	d.mu.Unlock()

	if len(updates) == 0 && len(deletes) == 0 {
		return nil
	}
	return collector.UpdateCheckpointBatch(d.store, updates, deletes)
}

// Len reports how many keys are remembered. Used by the status surface and by
// tests; not part of the dedupe contract.
func (d *dedupeStore) Len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.seen)
}

// HasRule reports whether any key for ruleID is remembered. Kept for
// diagnostics: "the token works but this rule has never fired" and "this rule
// fires constantly" are otherwise indistinguishable from outside.
func (d *dedupeStore) HasRule(ruleID string) bool {
	prefix := ruleID + "|"
	d.mu.Lock()
	defer d.mu.Unlock()
	for key := range d.seen {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}
