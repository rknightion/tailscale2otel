package devices

import "time"

// SetClock overrides the collector's clock for deterministic online-window
// tests. Test-only seam (compiled only into the test binary).
func SetClock(c *Collector, now func() time.Time) { c.now = now }
