package collector

import (
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/ringbuf"
)

// historyLen is the number of recent runs retained per collector for the admin
// status page's sparklines (duration trend) and run-outcome strip.
const historyLen = 60

// CollectorRun is the most recent run record for a single collector, suitable
// for surfacing on an admin status page. Times are wall-clock (the scheduler's
// configured clock); LastDuration is measured monotonically.
type CollectorRun struct {
	Runs         int64         // total runs (success + failure)
	LastStarted  time.Time     // wall-clock start of the most recent run
	LastFinished time.Time     // wall-clock finish of the most recent run
	LastDuration time.Duration // duration of the most recent run
	LastSuccess  bool          // whether the most recent run succeeded
	// LastSuccessAt is the finish time of the most recent SUCCESSFUL run (zero
	// before the first success). Unlike LastFinished it is not overwritten by a
	// later failure, so the status page can show data-freshness (how stale the
	// last good data is) independently of the last attempt.
	LastSuccessAt time.Time
	Failures      int64  // total failed runs over the process lifetime
	LastError     string // most recent run's error ("" when the last run succeeded)
	// ConsecutiveFailures is the length of the current unbroken run of failures
	// ending at the most recent run. It resets to 0 on any success.
	ConsecutiveFailures int64
}

// history holds the bounded per-collector ring buffers behind the status page's
// sparklines. It is kept separate from CollectorRun, which Snapshot copies by
// value (a Ring is a pointer and must not be shared out of the tracker).
type history struct {
	durMs    *ringbuf.Ring[int64] // last historyLen run durations, in milliseconds
	outcomes *ringbuf.Ring[bool]  // last historyLen run outcomes (true = success)
}

// CollectorHistory is a snapshot of one collector's recent-run history, oldest
// first. The two slices are aligned and the same length.
type CollectorHistory struct {
	DurationMs []int64
	Outcomes   []bool
}

// StatusTracker records the latest run outcome per collector. The scheduler
// writes one record per tick and the admin status handler reads snapshots, so it
// is safe for concurrent use. A nil *StatusTracker is a no-op: record does
// nothing and Snapshot returns an empty map.
type StatusTracker struct {
	mu   sync.RWMutex
	runs map[string]*CollectorRun
	hist map[string]*history
}

// NewStatusTracker returns an empty tracker.
func NewStatusTracker() *StatusTracker {
	return &StatusTracker{
		runs: make(map[string]*CollectorRun),
		hist: make(map[string]*history),
	}
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
	if errStr == "" {
		r.ConsecutiveFailures = 0
		r.LastSuccessAt = finished
	} else {
		r.Failures++
		r.ConsecutiveFailures++
	}
	r.LastError = errStr

	h := t.hist[name]
	if h == nil {
		h = &history{
			durMs:    ringbuf.New[int64](historyLen),
			outcomes: ringbuf.New[bool](historyLen),
		}
		t.hist[name] = h
	}
	h.durMs.Add(dur.Milliseconds())
	h.outcomes.Add(errStr == "")
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

// HistorySnapshot returns a copy of each collector's recent-run history (oldest
// first), keyed by collector name. The returned slices are independent of the
// tracker's internal ring buffers. On a nil receiver it returns an empty,
// non-nil map.
func (t *StatusTracker) HistorySnapshot() map[string]CollectorHistory {
	if t == nil {
		return map[string]CollectorHistory{}
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]CollectorHistory, len(t.hist))
	for name, h := range t.hist {
		out[name] = CollectorHistory{
			DurationMs: h.durMs.Values(),
			Outcomes:   h.outcomes.Values(),
		}
	}
	return out
}
