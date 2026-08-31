package app

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v4/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v4/internal/release"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
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
// release.Fetcher refreshed on its own (longer) TTL. A non-positive interval
// falls back to 60s (time.NewTicker(0) panics).
func runUpdateCheck(ctx context.Context, e telemetry.Emitter, latest func() (string, bool), selfVersion string, interval time.Duration) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
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

// updateStatusInfo reads a.selfRelease (nil unless version_checks.self.enabled)
// into the admin status page's update-availability view (#330). It is the
// admin-page counterpart to emitUpdateCheck: the gauge can just stay silent
// when the comparison isn't trustworthy, but the page has to say WHY.
func (a *App) updateStatusInfo() statusdata.UpdateInfo {
	if a.selfRelease == nil {
		return updateInfo(false, release.Snapshot{}, a.version)
	}
	return updateInfo(true, a.selfRelease.Snapshot(), a.version)
}

// deviceVersionCheckStatusInfo is the status-page counterpart to the
// release-backed device skew collectors. They deliberately emit nothing until
// a trustworthy stable version is available, so this row makes a blocked
// version source visible rather than indistinguishable from an up-to-date fleet.
func (a *App) deviceVersionCheckStatusInfo() statusdata.VersionCheckInfo {
	if a.tsRelease == nil {
		return deviceVersionCheckInfo(false, release.Snapshot{})
	}
	return deviceVersionCheckInfo(true, a.tsRelease.Snapshot())
}

func deviceVersionCheckInfo(enabled bool, snap release.Snapshot) statusdata.VersionCheckInfo {
	info := statusdata.VersionCheckInfo{Enabled: enabled}
	if !enabled {
		info.State = "disabled"
		return info
	}
	if !snap.CheckedAt.IsZero() {
		info.LastCheckedAt = snap.CheckedAt.UTC().Format(rfc3339)
	}
	info.LastErrorClass = snap.ErrClass
	if !snap.OK {
		if snap.ErrClass == "" {
			info.State = "checking"
		} else {
			info.State = "error"
		}
		return info
	}

	info.State = "ready"
	info.LatestVersion = release.Normalize(snap.Latest)
	return info
}

// updateInfo derives the statusdata.UpdateInfo view from a release fetcher
// snapshot and the running binary's version. Split out from
// (*App).updateStatusInfo so it can be unit-tested with a synthetic Snapshot
// rather than a live Fetcher goroutine.
func updateInfo(enabled bool, snap release.Snapshot, selfVersion string) statusdata.UpdateInfo {
	info := statusdata.UpdateInfo{Enabled: enabled, ReleaseURL: release.SelfReleaseURL}
	if !enabled {
		info.State = "disabled"
		return info
	}
	if !snap.CheckedAt.IsZero() {
		info.LastCheckedAt = snap.CheckedAt.UTC().Format(rfc3339)
	}
	// Surfaced regardless of what State ends up being: even a "current"
	// verdict from an earlier successful fetch (fail-open) should show that
	// the MOST RECENT check attempt failed.
	info.LastErrorClass = snap.ErrClass

	if !snap.OK {
		if snap.ErrClass != "" {
			info.State = "error"
		} else {
			info.State = "checking"
		}
		return info
	}

	cur, curOK := release.Parse(selfVersion)
	lat, latOK := release.Parse(snap.Latest)
	if !curOK || !latOK {
		// A "dev" build or an unparseable upstream value: we HAVE data, but
		// comparing it would be misleading, so this must never render as
		// "current" (issue #330's trustworthy-comparison requirement).
		info.State = "unknown"
		return info
	}

	info.CurrentVersion = release.Normalize(selfVersion)
	info.LatestVersion = release.Normalize(snap.Latest)
	if cur.Less(lat) {
		info.State = "available"
		info.MinorsBehind = release.MinorsBehind(cur, lat)
	} else {
		info.State = "current"
	}
	return info
}
