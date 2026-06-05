// Package catalog aggregates every emitting package's in-code telemetry catalog
// (the metricdoc.Metric / metricdoc.LogEvent descriptors declared next to each
// emit site) into the single, ordered source of truth that the docs generator
// renders into docs/metrics.md. It imports each emitting package and concatenates
// its Catalog()/LogCatalog(); Render() then fills the generated tables in
// docs/metrics.md between markers. Keeping this aggregation (and the marker
// engine) in the main module means it is unit-tested by `go test ./...`, while
// the thin tools/metricscatalog binary just wires it to the file.
package catalog

import (
	"github.com/rknightion/tailscale2otel/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/internal/audit"
	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/collector/acl"
	"github.com/rknightion/tailscale2otel/internal/collector/contacts"
	"github.com/rknightion/tailscale2otel/internal/collector/devices"
	"github.com/rknightion/tailscale2otel/internal/collector/dns"
	"github.com/rknightion/tailscale2otel/internal/collector/flowlogs"
	"github.com/rknightion/tailscale2otel/internal/collector/keys"
	"github.com/rknightion/tailscale2otel/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/internal/collector/settings"
	"github.com/rknightion/tailscale2otel/internal/collector/users"
	"github.com/rknightion/tailscale2otel/internal/collector/webhooks"
	"github.com/rknightion/tailscale2otel/internal/flowlog"
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/stream"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/webhook"
)

// metricSources is every package that declares emitted metrics. Order here only
// affects the pre-sort aggregate; Render() sorts each rendered table by name.
var metricSources = []func() []metricdoc.Metric{
	telemetry.Catalog,
	appcatalog.Catalog,
	collector.Catalog,
	devices.Catalog,
	users.Catalog,
	keys.Catalog,
	settings.Catalog,
	acl.Catalog,
	dns.Catalog,
	contacts.Catalog,
	webhooks.Catalog,
	flowlogs.Catalog,
	nodemetrics.Catalog,
	flowlog.Catalog,
	audit.Catalog,
	stream.Catalog,
	webhook.Catalog,
}

// logSources is every package that declares emitted log events.
var logSources = []func() []metricdoc.LogEvent{
	telemetry.LogCatalog,
	appcatalog.LogCatalog,
	collector.LogCatalog,
	devices.LogCatalog,
	users.LogCatalog,
	keys.LogCatalog,
	settings.LogCatalog,
	acl.LogCatalog,
	dns.LogCatalog,
	contacts.LogCatalog,
	webhooks.LogCatalog,
	flowlogs.LogCatalog,
	nodemetrics.LogCatalog,
	flowlog.LogCatalog,
	audit.LogCatalog,
	stream.LogCatalog,
	webhook.LogCatalog,
}

// Metrics returns every emitted metric declared across the codebase.
func Metrics() []metricdoc.Metric {
	var out []metricdoc.Metric
	for _, src := range metricSources {
		out = append(out, src()...)
	}
	return out
}

// LogEvents returns every emitted log event declared across the codebase.
func LogEvents() []metricdoc.LogEvent {
	var out []metricdoc.LogEvent
	for _, src := range logSources {
		out = append(out, src()...)
	}
	return out
}
