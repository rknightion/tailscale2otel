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

	"github.com/rknightion/tailscale2otel/v5/internal/apistate"
	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/entityage"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
	"github.com/rknightion/tailscale2otel/v5/internal/tsscope"
)

// Compile-time assertion that *Collector is a SnapshotCollector.
var _ collector.SnapshotCollector = (*Collector)(nil)

// Metric and event names emitted by this collector. Frozen by the #167 seam
// (MetricAppsCount/MetricAppScopes/MetricAppNodeAttributes/EventAppInfo) and
// the EPIC-03/#479 seam (MetricAppRedirectURIs/MetricAppScopeClass/MetricAppsAge).
const (
	MetricAppsCount         = "tailscale.oauth_apps.count"
	MetricAppScopes         = "tailscale.oauth_app.scopes"
	MetricAppNodeAttributes = "tailscale.oauth_app.node_attributes"
	EventAppInfo            = "tailscale.oauth_app.info"

	// MetricAppRedirectURIs is the #419 COUNT of an app's configured redirect
	// URIs — the values themselves are never emitted.
	MetricAppRedirectURIs = "tailscale.oauth_app.redirect_uris"
	// MetricAppScopeClass is #419's app-side analog of the keys collector's
	// #415 tailscale.key.scope_class: tsscope.Classify(a.Scopes), zero-seeded.
	MetricAppScopeClass = "tailscale.oauth_app.scope_class"
	// MetricAppsAge is the oauth_apps slice of the #426 fleet age distribution:
	// a single bounded histogram, not a per-entity series.
	MetricAppsAge = "tailscale.oauth_apps.age"
)

// Attribute keys emitted by this collector.
const (
	attrID            = "tailscale.oauth_app.id"
	attrName          = "tailscale.oauth_app.name"
	attrScopeValues   = "tailscale.oauth_app.scope_values"
	attrNodeAttrCount = "tailscale.oauth_app.node_attribute_count"
	// attrScopeClass is a pre-registered, frozen non-identifier attribute key
	// (internal/telemetry/pii/registry.go, EPIC-03/#479) — it doubles as both
	// the metric name and the attribute carrying the classification value, the
	// same "info gauge" idiom as tailscale2otel.build_info.
	attrScopeClass = "tailscale.oauth_app.scope_class"
)

// DefaultInterval is the poll cadence used when none is configured, per the
// #167 seam (matches the other inventory collectors' default of 300s).
const DefaultInterval = 300 * time.Second

// opListOAuthApps is the upstream operationId of the list call.
const opListOAuthApps = "listOAuthApps"

// lister is the narrow client surface this collector needs. It is satisfied by
// *tsapi.Client.
type lister interface {
	OAuthApps(ctx context.Context) ([]tsapi.OAuthApp, error)
}

// Collector reports Tailscale OAuth-application inventory on each tick.
type Collector struct {
	api      lister
	interval time.Duration
	now      func() time.Time
	// tracker records this collector's per-operation availability for the admin
	// status page and the capability matrix (#430/#524). A nil tracker is a no-op.
	tracker *apistate.Tracker
}

// Option configures optional Collector behavior.
type Option func(*Collector)

// WithClock overrides the collector's clock (for deterministic age-histogram
// tests); the default is time.Now.
func WithClock(now func() time.Time) Option {
	return func(c *Collector) { c.now = now }
}

// WithAPIState wires the shared per-operation availability tracker (#420).
// Availability METRICS are emitted regardless; the tracker is the in-process
// introspection copy the admin status page reads. A nil tracker is a no-op.
func WithAPIState(t *apistate.Tracker) Option { return func(c *Collector) { c.tracker = t } }

// New returns an oauth_apps Collector. A non-positive interval falls back to
// the package DefaultInterval (300s) via (*Collector).DefaultInterval,
// mirroring webhooks.New/logstream.New.
func New(api lister, interval time.Duration, opts ...Option) *Collector {
	c := &Collector{api: api, interval: interval, now: time.Now}
	for _, o := range opts {
		o(c)
	}
	return c
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
	// Observed regardless of outcome and INDEPENDENTLY of isFeatureOff below
	// (#420/#524). With Disposition{} a 403 here records as scope_denied even
	// though isFeatureOff (below) treats 403 OR 404 as "feature off" and
	// swallows the error. That divergence is intentional: isFeatureOff's
	// control flow (silently going idle) is unchanged, but the availability
	// signal now makes a real scope regression visible on the admin status
	// page and to the alert rules instead of looking identical to the alpha
	// feature simply being unavailable. Do NOT "align" the two by adding
	// DisabledOn: []int{403} — see the apistate package doc: an ambiguous 403
	// must default to scope_denied, and this endpoint's 403 is genuinely
	// ambiguous (alpha-feature-off vs. missing scope look identical on the
	// wire; only isFeatureOff's existing, deliberate choice is to treat both
	// as idle here).
	apistate.Observe(e, c.tracker, c.Name(), opListOAuthApps, apistate.Disposition{}, err, c.now())
	if err != nil {
		if isFeatureOff(err) {
			return nil
		}
		return fmt.Errorf("oauth_apps: list: %w", err)
	}

	e.Gauge(docAppsCount.Name, docAppsCount.Unit, docAppsCount.Description, float64(len(apps)), nil)

	now := c.now()
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
		// Redirect-URI COUNT only (#419) — the URI values themselves never
		// reach telemetry, on this metric or anywhere else.
		if len(a.RedirectURIs) > 0 {
			e.Gauge(docAppRedirectURIs.Name, docAppRedirectURIs.Unit, docAppRedirectURIs.Description,
				float64(len(a.RedirectURIs)), attrs)
		}

		// Privilege classification (#419, mirrors the keys collector's #415
		// treatment): zero-seeded across every tsscope.Class for EVERY app,
		// including one with no scopes at all (ClassNone is itself a
		// meaningful posture value here, unlike the count-based
		// tailscale.oauth_app.scopes gauge above).
		emitScopeClass(e, a)

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

	// tailscale.oauth_apps.age (#426): a single bounded fleet age-distribution
	// histogram, not a per-entity series. Apps with no Created timestamp are
	// skipped (entityage.Seconds ok=false) rather than reported as age 0.
	for i := range apps {
		if secs, ok := entityage.Seconds(apps[i].Created, now); ok {
			e.Histogram(docAppsAge.Name, docAppsAge.Unit, docAppsAge.Description,
				secs, entityage.BucketsSeconds(), nil)
		}
	}

	return nil
}

// emitScopeClass zero-seeds a's privilege class across every tsscope.Class
// (mirrors the keys collector's emitScopeClass / apistate.EmitAvailability):
// one series holds value 1 (the current class), the rest hold 0.
func emitScopeClass(e telemetry.Emitter, a *tsapi.OAuthApp) {
	class := tsscope.Classify(a.Scopes)
	for _, want := range tsscope.Classes() {
		v := 0.0
		if want == class {
			v = 1
		}
		e.Gauge(docAppScopeClass.Name, docAppScopeClass.Unit, docAppScopeClass.Description, v, telemetry.Attrs{
			attrID:         a.ID,
			attrName:       a.Name,
			attrScopeClass: string(want),
		})
	}
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
