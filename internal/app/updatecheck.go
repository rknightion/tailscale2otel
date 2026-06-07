package app

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/internal/release"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// emitUpdateCheck emits tailscale2otel.update_available once: 1 if a newer
// release than selfVersion is available, else 0. It emits nothing when the
// upstream value is unknown or either version is unparseable (e.g. a "dev"
// build), so the gauge is never misleadingly 0.
func emitUpdateCheck(e telemetry.Emitter, latest func() (string, bool), selfVersion string) {
	lv, ok := latest()
	if !ok {
		return
	}
	cur, ok1 := release.Parse(selfVersion)
	up, ok2 := release.Parse(lv)
	if !ok1 || !ok2 {
		return
	}
	val := 0.0
	if cur.Less(up) {
		val = 1
	}
	e.Gauge(appcatalog.DocUpdateAvailable.Name, appcatalog.DocUpdateAvailable.Unit,
		appcatalog.DocUpdateAvailable.Description, val, nil)
}

// runUpdateCheck emits the self update-available gauge immediately, then every
// interval until ctx is canceled. The latest() provider is backed by a
// release.Fetcher refreshed on its own (longer) TTL.
func runUpdateCheck(ctx context.Context, e telemetry.Emitter, latest func() (string, bool), selfVersion string, interval time.Duration) {
	emitUpdateCheck(e, latest, selfVersion)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			emitUpdateCheck(e, latest, selfVersion)
		}
	}
}
