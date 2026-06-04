package collector

import (
	"sync"
	"time"
)

// CollectorRun is the most recent run record for a single collector, suitable
// for surfacing on an admin status page. Times are wall-clock (the scheduler's
// configured clock); LastDuration is measured monotonically.
type CollectorRun struct {
	Runs         int64         // total runs (success + failure)
	LastStarted  time.Time     // wall-clock start of the most recent run
	LastFinished time.Time     // wall-clock finish of the most recent run
	LastDuration time.Duration // duration of the most recent run
	LastSuccess  bool          // whether the most recent run succeeded
	Failures     int64         // total failed runs over the process lifetime
	LastError    string        // most recent run's error ("" when the last run succeeded)
}

// StatusTracker records the latest run outcome per collector. The scheduler
// writes one record per tick and the admin status handler reads snapshots, so it
// is safe for concurrent use. A nil *StatusTracker is a no-op: record does
// nothing and Snapshot returns an empty map.
type StatusTracker struct {
	mu   sync.RWMutex
	runs map[string]*CollectorRun
}

// NewStatusTracker returns an empty tracker.
func NewStatusTracker() *StatusTracker {
	return &StatusTracker{runs: make(map[string]*CollectorRun)}
}

// record updates the run record for the named collector. errStr is "" on success
// and the failure message otherwise.
func (t *StatusTracker) record(name string, started, finished time.Time, dur time.Duration, errStr string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.runs[name]
	if r == nil {
		r = &CollectorRun{}
		t.runs[name] = r
	}
	r.Runs++
	r.LastStarted = started
	r.LastFinished = finished
	r.LastDuration = dur
	r.LastSuccess = errStr == ""
	if errStr != "" {
		r.Failures++
	}
	r.LastError = errStr
}

// Snapshot returns a copy of the per-collector run records keyed by collector
// name. The returned map and its values are independent of the tracker's
// internal state. On a nil receiver it returns an empty, non-nil map.
func (t *StatusTracker) Snapshot() map[string]CollectorRun {
	if t == nil {
		return map[string]CollectorRun{}
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]CollectorRun, len(t.runs))
	for k, v := range t.runs {
		out[k] = *v
	}
	return out
}
