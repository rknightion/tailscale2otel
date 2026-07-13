// Package oauthapps is a snapshot collector reporting Tailscale OAuth
// application inventory: an aggregate count plus per-app scope and
// allowed-node-attribute cardinality (scope-sprawl signals, mirroring the keys
// collector's tailscale.key.scopes precedent) and an info log per app.
//
// GET /tailnet/{tailnet}/oauth-apps is an alpha API endpoint: a tailnet
// without it enabled, or an API credential lacking the required scope,
// responds 403 or 404 rather than a body. Per the #167 seam freeze, that is
// treated as the feature being idle/off — not a collector failure — so the
// collector stays default-on and silently emits nothing for tailnets that
// don't have it, mirroring the flowlogs/logstream 403-idle precedent.
package oauthapps

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

// Compile-time assertion that *Collector is a SnapshotCollector.
var _ collector.SnapshotCollector = (*Collector)(nil)

// Metric and event names emitted by this collector. Frozen by the #167 seam.
const (
	MetricAppsCount         = "tailscale.oauth_apps.count"
	MetricAppScopes         = "tailscale.oauth_app.scopes"
	MetricAppNodeAttributes = "tailscale.oauth_app.node_attributes"
	EventAppInfo            = "tailscale.oauth_app.info"
)

// Attribute keys emitted by this collector.
const (
	attrID            = "tailscale.oauth_app.id"
	attrName          = "tailscale.oauth_app.name"
	attrScopeValues   = "tailscale.oauth_app.scope_values"
	attrNodeAttrCount = "tailscale.oauth_app.node_attribute_count"
)

// DefaultInterval is the poll cadence used when none is configured, per the
// #167 seam (matches the other inventory collectors' default of 300s).
const DefaultInterval = 300 * time.Second

// lister is the narrow client surface this collector needs. It is satisfied by
// *tsapi.Client.
type lister interface {
	OAuthApps(ctx context.Context) ([]tsapi.OAuthApp, error)
}

// Collector reports Tailscale OAuth-application inventory on each tick.
type Collector struct {
	api      lister
	interval time.Duration
}

// New returns an oauth_apps Collector. A non-positive interval falls back to
// the package DefaultInterval (300s) via (*Collector).DefaultInterval,
// mirroring webhooks.New/logstream.New.
func New(api lister, interval time.Duration) *Collector {
	return &Collector{api: api, interval: interval}
}

// Name returns the stable collector identifier.
func (c *Collector) Name() string { return "oauth_apps" }

// DefaultInterval returns the configured interval, or the package DefaultInterval
// constant if non-positive.
func (c *Collector) DefaultInterval() time.Duration {
	if c.interval > 0 {
		return c.interval
	}
	return DefaultInterval
}

// Collect fetches the tailnet's OAuth applications and emits the inventory
// metrics and one info log event per app.
//
// A 403/404 (the alpha endpoint disabled, or the credential lacking scope) is
// treated as the feature being off: Collect returns nil and emits nothing, so
// the scheduler never reports a scrape failure for a tailnet that simply
// doesn't have the feature. Any other error (including 5xx/transport errors)
// is returned so the scheduler can classify and retry it normally.
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	apps, err := c.api.OAuthApps(ctx)
	if err != nil {
		if isFeatureOff(err) {
			return nil
		}
		return fmt.Errorf("oauth_apps: list: %w", err)
	}

	e.Gauge(docAppsCount.Name, docAppsCount.Unit, docAppsCount.Description, float64(len(apps)), nil)

	for i := range apps {
		a := &apps[i]
		attrs := telemetry.Attrs{attrID: a.ID, attrName: a.Name}

		if len(a.Scopes) > 0 {
			e.Gauge(docAppScopes.Name, docAppScopes.Unit, docAppScopes.Description, float64(len(a.Scopes)), attrs)
		}
		if len(a.AllowedNodeAttributes) > 0 {
			e.Gauge(docAppNodeAttributes.Name, docAppNodeAttributes.Unit, docAppNodeAttributes.Description,
				float64(len(a.AllowedNodeAttributes)), attrs)
		}

		e.LogEvent(telemetry.Event{
			Name:     docAppInfo.Name,
			Severity: telemetry.SeverityInfo,
			Body:     fmt.Sprintf("Tailscale OAuth app %q has %d scope(s) and %d allowed node attribute(s)", a.Name, len(a.Scopes), len(a.AllowedNodeAttributes)),
			Attrs: telemetry.Attrs{
				attrID:            a.ID,
				attrName:          a.Name,
				attrScopeValues:   strings.Join(a.Scopes, ","),
				attrNodeAttrCount: strconv.Itoa(len(a.AllowedNodeAttributes)),
			},
		})
	}

	return nil
}

// isFeatureOff reports whether err is (or wraps) a *tsapi.StatusError with an
// HTTP 403 or 404 status, indicating the alpha OAuth-apps endpoint is
// unavailable (feature not enabled, or the credential lacks scope) rather than
// a transient failure. This mirrors the logstream/flowlogs precedent of
// classifying by the typed status code rather than matching text in
// err.Error().
func isFeatureOff(err error) bool {
	var se *tsapi.StatusError
	return errors.As(err, &se) && (se.Code == 403 || se.Code == 404)
}
