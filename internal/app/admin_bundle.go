package app

import (
	"fmt"
	"net/http"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/supportbundle"
)

// bundleIncludeDevicesParam is the query parameter that opts into the
// PII-heavy device inventory section (#321's acceptance criterion: "PII-heavy
// sections require explicit opt-in" — the bundle excludes it unless this is
// exactly "1", so a stray truthy-looking value like "true" or "yes" does NOT
// silently opt in; this fails CLOSED on anything but the one documented value).
const bundleIncludeDevicesParam = "include_devices"

// handleSupportBundle serves a privacy-safe support bundle (#321): version,
// every configuration diagnostic, the full redacted effective config,
// component/API/export state, and the metric/log-event catalogs, as one
// deterministic, bounded zip archive. It replaces the manual
// gather-and-redact procedure docs/troubleshooting.md's "Still stuck?"
// section used to ask an operator to do by hand.
//
// The device inventory (names, hostnames, users, IP addresses) is the one
// PII-heavy section this bundle can carry; it is included only when the
// request explicitly opts in via ?include_devices=1. Flow-log records and
// raw audit/webhook log content are never included by this bundle at all —
// internal/supportbundle has no opt-in path for either (see its Manifest
// doc) — so requesting them is not possible through this endpoint.
//
// Read-only, GET-only, behind the same admin gate as every other admin route
// (requireAdminAuth, wired in buildAdminServer).
func (a *App) handleSupportBundle(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	opts := supportbundle.Options{
		IncludeDeviceInventory: r.URL.Query().Get(bundleIncludeDevicesParam) == "1",
	}

	status := a.buildStatus()
	in := supportbundle.Input{
		Version:     a.version,
		Diagnostics: a.cfg.Diagnostics(),
		Config:      status.Config.Full,
		Components:  status.Components,
		API:         status.API,
		Delivery:    status.Delivery,
		Collectors:  status.Collectors,
		Advisories:  status.Advisories,
		Metrics:     status.Metrics,
		LogEvents:   status.LogEvents,
		FlowStore:   status.Flows,
	}
	// The CALLER decides whether the device inventory travels at all — see
	// supportbundle.Input.Devices' own doc for why this package does not
	// gate on Options itself. status.Devices is already the full combined
	// device-cache view (see (*App).deviceRows), so it is only read into In
	// when the request explicitly opted in.
	if opts.IncludeDeviceInventory {
		in.Devices = status.Devices
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+bundleFilename(a.version)+`"`)
	if err := supportbundle.Write(w, in, opts, time.Now()); err != nil {
		a.logger.Error("write support bundle", "error", err)
	}
}

// bundleFilename names the downloaded archive after the running version and
// the moment it was generated, so two bundles taken minutes apart never
// collide in a Downloads folder — the same convention exportFilename uses
// for the flow-export downloads.
func bundleFilename(version string) string {
	return fmt.Sprintf("tailscale2otel-support-%s-%s.zip",
		sanitizeFilenamePart(version), time.Now().UTC().Format("20060102T150405Z"))
}
