package app

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// emitProcess records process.uptime (gauge) and, when readCPU returns ok=true,
// the per-interval deltas for process.cpu.time (counter, split by cpu.mode).
// lastUser and lastSys carry the previous cumulative CPU seconds across calls so
// only the per-interval delta is added to the counter — mirroring how emitRuntime
// handles monotonic MemStats values. On the first call both are zero, so the full
// since-start cumulative is emitted as the seed delta (correct under cumulative
// temporality). A negative delta (impossible under normal getrusage but guarded
// defensively) is skipped and last* is still advanced to the new baseline.
func emitProcess(e telemetry.Emitter, start time.Time, readCPU func() (user, system float64, ok bool), lastUser, lastSys *float64) {
	// process.uptime — always emit.
	e.Gauge(
		appcatalog.DocProcessUptime.Name,
		appcatalog.DocProcessUptime.Unit,
		appcatalog.DocProcessUptime.Description,
		time.Since(start).Seconds(),
		nil,
	)

	// process.cpu.time — only on platforms that support getrusage.
	user, sys, ok := readCPU()
	if !ok {
		return
	}

	if user >= *lastUser {
		e.Counter(
			appcatalog.DocProcessCPUTime.Name,
			appcatalog.DocProcessCPUTime.Unit,
			appcatalog.DocProcessCPUTime.Description,
			user-*lastUser,
			telemetry.Attrs{semconv.AttrCPUMode: semconv.CPUModeUser},
		)
	}
	*lastUser = user

	if sys >= *lastSys {
		e.Counter(
			appcatalog.DocProcessCPUTime.Name,
			appcatalog.DocProcessCPUTime.Unit,
			appcatalog.DocProcessCPUTime.Description,
			sys-*lastSys,
			telemetry.Attrs{semconv.AttrCPUMode: semconv.CPUModeSystem},
		)
	}
	*lastSys = sys
}

// runProcessReporter emits process.uptime and process.cpu.time immediately, then
// on each interval until ctx is canceled. readCPU is injected so tests are
// deterministic and platform-independent; production passes readProcessCPU.
// A non-positive interval falls back to 60s (time.NewTicker(0) panics).
func runProcessReporter(ctx context.Context, e telemetry.Emitter, start time.Time, interval time.Duration, readCPU func() (user, system float64, ok bool)) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	var lastUser, lastSys float64
	emit := func() { emitProcess(e, start, readCPU, &lastUser, &lastSys) }
	emit()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			emit()
		}
	}
}
