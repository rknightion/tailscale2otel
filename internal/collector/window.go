package collector

import "time"

// nextWindow computes the [from, to] range a window collector should poll on
// this tick, given the last consumed high-water mark.
//
//   - to is always now-lag, so we never query up to "now" where late records
//     may still be arriving (the Tailscale flow-log "tail" hazard).
//   - on a cold start (no checkpoint) we look back initialLookback.
//   - a long outage is capped to maxWindow so a single tick can't request a
//     huge range; the collector catches up over successive ticks.
//   - ok is false when there is nothing new to poll yet (from >= to), in which
//     case the caller should skip this tick without advancing the checkpoint.
func nextWindow(last time.Time, hasLast bool, now time.Time, lag, initialLookback, maxWindow time.Duration) (from, to time.Time, ok bool) {
	to = now.Add(-lag)
	if hasLast {
		from = last
	} else {
		from = to.Add(-initialLookback)
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, false
	}
	if maxWindow > 0 && to.Sub(from) > maxWindow {
		to = from.Add(maxWindow)
	}
	return from, to, true
}
