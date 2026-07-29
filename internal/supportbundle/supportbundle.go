// Package supportbundle assembles a privacy-safe support bundle: everything
// docs/troubleshooting.md's "Still stuck?" section previously asked an
// operator to gather and redact BY HAND (#321) — version, every configuration
// diagnostic, the full redacted effective config, component/API/export state,
// and the metric/log-event catalogs — as one deterministic, bounded archive.
//
// It is a leaf package (no dependency on internal/app) so it is testable
// without a running App: the caller (an admin HTTP handler, or in future a
// CLI flag) gathers an Input from whatever App-internal state it already has
// and calls Write. This mirrors why internal/configexport exists (see its
// package doc) — Build's output is exactly what feeds Input.Config, so this
// package does not restate configexport's redaction rules; it only arranges
// data configexport and the admin status snapshot already redacted.
//
// PII-heavy sections are opt-in and OFF by default (#321's acceptance
// criterion): the device inventory (names, hostnames, users, IPs) is included
// only when Options.IncludeDeviceInventory is true. Flow-log records and raw
// audit/webhook log content are never included by this package at all — no
// opt-in path exists for them here, so they are always excluded (see
// Manifest.ExcludedByDefault). Only the bounded, non-PII aggregate counters
// already surfaced on the admin status page (flow-store/event-store sizing)
// travel with this bundle, via Input.Collectors/Components/etc.
package supportbundle

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v3/internal/config"
)

// FormatVersion identifies the bundle's file layout, so a future consumer (a
// script, a support engineer's tooling) can tell an old bundle from a new one
// without guessing from which files happen to be present.
const FormatVersion = 1

// Bounds. Every section is capped so a single bundle can never grow
// unboundedly with tailnet size — a large tailnet's device list or collector
// fleet stays a bounded download, not an ever-growing one. Each cap is
// generous relative to any real deployment; hitting one is disclosed via
// Manifest.Truncated rather than silently dropping rows.
const (
	maxDevices          = 5000
	maxCollectors       = 2000
	maxComponents       = 200
	maxAPIEndpoints     = 500
	maxDeliverySignals  = 100
	maxAdvisories       = 500
	maxDiagnostics      = 1000
	maxMetricsCatalog   = 5000
	maxLogEventsCatalog = 5000
)

// Options controls the PII-heavy sections a bundle may include. The zero
// value is the privacy-safe default: nothing PII-heavy is included.
type Options struct {
	// IncludeDeviceInventory opts into the device enrichment table (device
	// names, hostnames, OS, user, IP addresses, tags) — the one section this
	// package can include that identifies specific people or machines on the
	// tailnet. Off by default.
	IncludeDeviceInventory bool
}

// Input is everything Build/Write needs to assemble a bundle, gathered by the
// caller from already-redacted, already-computed state (typically
// (*app.App).buildStatus() plus (*config.Config).Diagnostics()). Nothing in
// this package re-derives or re-redacts these values — see the package doc
// for why a second copy of any redaction rule is the failure mode to avoid.
type Input struct {
	// Version is the running binary's version string (cmd/tailscale2otel's
	// -version output).
	Version string
	// Diagnostics is config.Diagnostics()'s full validation+advisory pass
	// (#307) — every problem in one report, not just the first fatal one.
	Diagnostics []config.Diagnostic
	// Config is the complete redacted effective configuration
	// (internal/configexport.Build's output, as already carried by
	// statusdata.ConfigSummary.Full). Every config.Secret-typed field is
	// reduced to {secret, set, source}; nothing here can ever be a raw
	// credential value because Build itself never renders one.
	Config map[string]statusdata.ConfigFieldValue
	// Components is the long-running-subsystem state /readyz itself gates on
	// (admin, metrics, stream, webhook, ingress WAL).
	Components []statusdata.ComponentStatus
	// API is the Tailscale API call/rate-limit/auth-method summary. It never
	// carries a credential value (see statusdata.APIAuth's doc).
	API statusdata.APIInfo
	// Delivery is the OTLP export state per signal (#317) — what actually
	// reached the backend, distinct from what collectors merely produced.
	Delivery []statusdata.DeliverySignal
	// Collectors is the per-collector run/health summary (name, interval,
	// run counts, last error CLASS — never a raw error body that could carry
	// a backend response).
	Collectors []statusdata.CollectorStatus
	// Advisories is every active non-fatal configuration warning.
	Advisories []statusdata.ConfigAdvisory
	// Metrics and LogEvents are the code-as-docs telemetry catalogs
	// (internal/catalog, internal/metricdoc) — static per build, not
	// tailnet-derived, so they carry no PII by construction.
	Metrics   []statusdata.MetricRow
	LogEvents []statusdata.LogRow
	// FlowStore is the flow view's own state (#294): which backend is serving,
	// whether it is healthy, and its bounded counters. It is the one signal that
	// makes a stuck or unhealthy persistent store diagnosable from a bundle —
	// queue drops and a false Healthy are otherwise visible only on a live admin
	// page nobody can reach after the fact.
	//
	// It carries no records and no identities: kind, health, counts, row total
	// and file size only. The flow RECORDS themselves remain unconditionally
	// excluded, with no opt-in path, exactly as before.
	FlowStore statusdata.FlowStoreInfo
	// Devices is the device-enrichment table. The CALLER must pass nil unless
	// Options.IncludeDeviceInventory is true — Write does not itself gate on
	// Options for this field, so the caller (internal/app/admin_bundle.go) is
	// the single place that decision is made, matching the "fail closed"
	// requirement: a caller that forgets the opt-in check simply has nothing
	// to include here, rather than this package silently deciding for it.
	Devices []statusdata.DeviceRow
}

// Manifest is bundle/manifest.json: it states exactly what a given bundle
// contains and, just as importantly, what it deliberately excludes — so a
// bundle can never be mistaken for a complete capture of the tailnet's state.
type Manifest struct {
	FormatVersion int    `json:"format_version"`
	GeneratedAt   string `json:"generated_at"` // RFC3339, UTC
	Version       string `json:"version"`
	// Included lists every file this bundle actually contains, in the fixed
	// order Write adds them (see fileOrder).
	Included []string `json:"included"`
	// ExcludedByDefault lists every section this package can produce that is
	// NOT in this bundle. "device_inventory" moves out of this list (and into
	// Included, as "devices.json") only when Options.IncludeDeviceInventory
	// was set. "flow_records" and "raw_logs" are ALWAYS excluded — this
	// package has no opt-in path for either; only their bounded, non-PII
	// aggregate counts travel — flow-store sizing and backend health via
	// FlowStore (flow_store.json), collector run stats via Collectors — never
	// the records themselves.
	ExcludedByDefault []string `json:"excluded_by_default"`
	// Truncated lists, one line per section, any section whose entry count
	// exceeded its bound and was cut — so a shorter-than-expected section
	// reads as "disclosed truncation" rather than "silently incomplete".
	Truncated []string `json:"truncated,omitempty"`
}

// fileOrder is the fixed sequence Write adds files in — both the archive's
// entry order (for determinism: two calls with identical Input/Options/now
// must byte-for-byte match) and Manifest.Included's order.
var fileOrder = []string{
	"manifest.json",
	"version.json",
	"diagnostics.json",
	"config.json",
	"components.json",
	"api.json",
	"delivery.json",
	"collectors.json",
	"advisories.json",
	"flow_store.json",
	"catalog_metrics.json",
	"catalog_log_events.json",
}

const devicesFile = "devices.json"

// Write assembles the support bundle as a zip archive into w. now is passed
// in explicitly (rather than read via time.Now()) so a caller — and this
// package's own determinism test — can pin the only field allowed to vary
// between two otherwise-identical bundles: Manifest.GeneratedAt.
//
// Every archive entry is written with a zero Modified time (zip's default),
// so two calls with identical Input/Options/now produce byte-identical
// output: identical JSON bytes per entry (encoding/json sorts map keys and
// struct fields are already in fixed declaration order) through identical
// zip entry metadata.
func Write(w io.Writer, in Input, opts Options, now time.Time) error {
	var truncated []string
	diagnostics := boundSlice(in.Diagnostics, maxDiagnostics, "diagnostics", &truncated)
	components := boundSlice(in.Components, maxComponents, "components", &truncated)
	delivery := boundSlice(in.Delivery, maxDeliverySignals, "delivery", &truncated)
	collectors := boundSlice(in.Collectors, maxCollectors, "collectors", &truncated)
	advisories := boundSlice(in.Advisories, maxAdvisories, "advisories", &truncated)
	metrics := boundSlice(in.Metrics, maxMetricsCatalog, "catalog_metrics", &truncated)
	logEvents := boundSlice(in.LogEvents, maxLogEventsCatalog, "catalog_log_events", &truncated)
	api := in.API
	api.Endpoints = boundSlice(api.Endpoints, maxAPIEndpoints, "api.endpoints", &truncated)

	included := append([]string(nil), fileOrder...)
	excluded := []string{"flow_records", "raw_logs"}
	var devices []statusdata.DeviceRow
	if opts.IncludeDeviceInventory {
		devices = boundSlice(in.Devices, maxDevices, "devices", &truncated)
		included = append(included, devicesFile)
	} else {
		excluded = append(excluded, "device_inventory")
	}

	manifest := Manifest{
		FormatVersion:     FormatVersion,
		GeneratedAt:       now.UTC().Format(time.RFC3339),
		Version:           in.Version,
		Included:          included,
		ExcludedByDefault: excluded,
		Truncated:         truncated,
	}

	zw := zip.NewWriter(w)
	write := func(name string, v any) error {
		body, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal %s: %w", name, err)
		}
		f, err := zw.Create(name)
		if err != nil {
			return fmt.Errorf("create %s: %w", name, err)
		}
		if _, err := f.Write(body); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
		return nil
	}

	if err := write("manifest.json", manifest); err != nil {
		return err
	}
	if err := write("version.json", map[string]string{"version": in.Version}); err != nil {
		return err
	}
	if err := write("diagnostics.json", diagnostics); err != nil {
		return err
	}
	if err := write("config.json", in.Config); err != nil {
		return err
	}
	if err := write("components.json", components); err != nil {
		return err
	}
	if err := write("api.json", api); err != nil {
		return err
	}
	if err := write("delivery.json", delivery); err != nil {
		return err
	}
	if err := write("collectors.json", collectors); err != nil {
		return err
	}
	if err := write("advisories.json", advisories); err != nil {
		return err
	}
	if err := write("flow_store.json", in.FlowStore); err != nil {
		return err
	}
	if err := write("catalog_metrics.json", metrics); err != nil {
		return err
	}
	if err := write("catalog_log_events.json", logEvents); err != nil {
		return err
	}
	if opts.IncludeDeviceInventory {
		if err := write(devicesFile, devices); err != nil {
			return err
		}
	}
	return zw.Close()
}

// boundSlice caps s at max entries, recording a disclosure line in
// *truncated (keyed by label) when it had to cut. A nil input stays nil
// (never turned into an empty-but-non-nil slice), matching every other JSON
// surface in this codebase's "nil vs empty is a real distinction" convention.
func boundSlice[T any](s []T, max int, label string, truncated *[]string) []T {
	if len(s) <= max {
		return s
	}
	*truncated = append(*truncated, fmt.Sprintf("%s: truncated to %d of %d entries", label, max, len(s)))
	return s[:max]
}
