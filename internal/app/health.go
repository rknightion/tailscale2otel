package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/app/statusdata"
)

// Health states surfaced on the admin status page.
const (
	healthHealthy  = "healthy"
	healthDegraded = "degraded"
	healthStarting = "starting"
)

// consecutiveFailureThreshold is the number of back-to-back failures at which a
// collector drags overall health to "degraded".
const consecutiveFailureThreshold = 3

// successRatePct reports the lifetime success rate as a percentage. It returns 0
// when no run has happened yet (rate is undefined), which pairs with HasRun=false
// so the page shows "—" rather than a misleading 0%.
func successRatePct(runs, failures int64) float64 {
	if runs <= 0 {
		return 0
	}
	return float64(runs-failures) / float64(runs) * 100
}

// nextRunIn reports how long until the next scheduled tick, clamped to zero once
// the collector is due or overdue.
func nextRunIn(lastFinished time.Time, interval time.Duration, now time.Time) time.Duration {
	d := lastFinished.Add(interval).Sub(now)
	if d < 0 {
		return 0
	}
	return d
}

// isOverdue reports whether a collector has not run in over twice its interval —
// a heuristic that a tick is wedged rather than merely a little late.
func isOverdue(lastFinished time.Time, interval time.Duration, now time.Time) bool {
	return now.After(lastFinished.Add(2 * interval))
}

// deriveHealth summarizes overall service health from the per-collector status
// rows. Precedence: any failing, overdue or stuck collector makes the service
// "degraded"; otherwise a collector that has not yet run makes it "starting";
// otherwise "healthy". The returned reasons explain a non-healthy verdict.
func deriveHealth(collectors []statusdata.CollectorStatus) (string, []string) {
	var reasons, pending []string
	for _, c := range collectors {
		if !c.HasRun {
			pending = append(pending, c.Name)
		}
		switch {
		case c.ConsecutiveFailures >= consecutiveFailureThreshold:
			reasons = append(reasons, fmt.Sprintf("collector %q: %d consecutive failures", c.Name, c.ConsecutiveFailures))
		case c.HasRun && !c.LastSuccess:
			reasons = append(reasons, fmt.Sprintf("collector %q: last run failed", c.Name))
		}
		if c.Overdue {
			reasons = append(reasons, fmt.Sprintf("collector %q: overdue (no run in over 2 intervals)", c.Name))
		}
		if c.Checkpoint != nil && c.Checkpoint.Stuck {
			reasons = append(reasons, fmt.Sprintf("collector %q: checkpoint stuck (lag %s)", c.Name, c.Checkpoint.Lag))
		}
	}
	switch {
	case len(reasons) > 0:
		return healthDegraded, reasons
	case len(pending) > 0:
		return healthStarting, []string{"waiting for first run: " + strings.Join(pending, ", ")}
	default:
		return healthHealthy, nil
	}
}
