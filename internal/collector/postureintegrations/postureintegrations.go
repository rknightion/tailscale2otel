// Package postureintegrations is a snapshot collector for the tailnet's
// device-posture integrations (MDM/EDR providers such as Intune). It emits the
// integration count plus per-integration match counts and the last-sync
// timestamp (for staleness alerting — a stalled sync means posture data is
// going stale). The provider identifiers (clientId/tenantId/cloudId) are never
// fetched (see internal/tsapi) and so cannot be emitted.
//
// No entity-age distribution is emitted here (#426), deliberately: the upstream
// PostureIntegration schema carries only `configUpdated` (when the config was
// last EDITED) and `status.lastSync`. Neither is a creation timestamp, and an
// age derived from a mutable "last edited" field would answer a question nobody
// asked while looking exactly like the fleet ages the other collectors report.
// TestNoAgeHistogram pins the omission so it does not get "fixed" by inventing
// one.
package postureintegrations

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

// Compile-time assertions.
var (
	_ collector.SnapshotCollector = (*Collector)(nil)
	_ api                         = (*tsapi.Client)(nil)
)

const defaultInterval = 600 * time.Second

const (
	metricCount         = "tailscale.posture_integrations.count"
	metricMatched       = "tailscale.posture_integration.matched"
	metricPossible      = "tailscale.posture_integration.possible_matched"
	metricProviderHosts = "tailscale.posture_integration.provider_hosts"
	metricLastSync      = "tailscale.posture_integration.last_sync"
	metricError         = "tailscale.posture_integration.error"
)

const (
	attrProvider    = "tailscale.posture.provider"
	attrIntegration = "tailscale.posture.integration"
)

// opGetPostureIntegrations is the upstream operationId of the list call.
const opGetPostureIntegrations = "getPostureIntegrations"

// postureDisposition is the DEFAULT disposition (#420): 403 stays scope_denied.
// Device posture IS a Premium/Enterprise feature, but upstream signals its
// absence with a 404 on this path, not a 403 — and reading 403 as "feature off"
// here would silently swallow a missing devices:read scope, which is exactly
// the conflation the issue exists to prevent. Only flowlogs, whose 403 gate is
// documented upstream, opts out of this default.
var postureDisposition = apistate.Disposition{}

// api is the narrow slice of the Tailscale client this collector needs.
type api interface {
	PostureIntegrations(ctx context.Context) ([]tsapi.PostureIntegration, error)
}

// Collector implements collector.SnapshotCollector for posture integrations.
type Collector struct {
	api      api
	interval time.Duration
	// tracker records per-operation availability for the admin status page
	// (#420). A nil *apistate.Tracker is a no-op.
	tracker *apistate.Tracker
	// now is the clock, injectable from tests.
	now func() time.Time
}

// Option configures optional Collector behavior.
type Option func(*Collector)

// WithAPIState wires the shared per-operation availability tracker (#420).
// Availability METRICS are emitted regardless; the tracker is the in-process
// introspection copy the admin status page reads.
func WithAPIState(t *apistate.Tracker) Option {
	return func(c *Collector) { c.tracker = t }
}

// New returns a posture-integrations collector. A non-positive interval resolves
// to the default (600s).
func New(a api, interval time.Duration, opts ...Option) *Collector {
	c := &Collector{api: a, interval: interval, now: time.Now}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Name returns the stable collector identifier.
func (c *Collector) Name() string { return "posture_integrations" }

// DefaultInterval returns the configured interval, or 600s when unset.
func (c *Collector) DefaultInterval() time.Duration {
	if c.interval > 0 {
		return c.interval
	}
	return defaultInterval
}

// Collect lists posture integrations and emits the count plus per-integration
// match counts and last-sync timestamp (skipped when no sync has occurred).
//
// Failures are CLASSIFIED rather than blanket-propagated (#420). A 404 means
// the tailnet has no posture-integration surface at all, so it emits count=0
// and stays idle — genuinely zero integrations, not a scrape failure. Every
// other error (401, 403, 429, 5xx, transport) is returned AND emits its
// distinct availability state, and deliberately emits no count: being denied
// tells us nothing about how many integrations exist.
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	ints, err := c.api.PostureIntegrations(ctx)
	state := apistate.Observe(e, c.tracker, c.Name(), opGetPostureIntegrations, postureDisposition, err, c.now())
	if err != nil {
		if state == apistate.StateDisabled {
			e.Gauge(docCount.Name, docCount.Unit, docCount.Description, 0, nil)
			return nil
		}
		return err
	}

	e.Gauge(docCount.Name, docCount.Unit, docCount.Description, float64(len(ints)), nil)

	for i := range ints {
		in := &ints[i]
		attrs := telemetry.Attrs{attrProvider: in.Provider, attrIntegration: in.ID}
		e.Gauge(docMatched.Name, docMatched.Unit, docMatched.Description,
			float64(in.Status.MatchedCount), attrs)
		e.Gauge(docPossible.Name, docPossible.Unit, docPossible.Description,
			float64(in.Status.PossibleMatchedCount), attrs)
		e.Gauge(docProviderHosts.Name, docProviderHosts.Unit, docProviderHosts.Description,
			float64(in.Status.ProviderHostCount), attrs)
		// 0/1 error gauge: LastSync tracks the last sync ATTEMPT (not success), so a
		// failing integration keeps advancing it — this is the only failure signal.
		// The raw error text is NOT put on a label (unbounded / potentially sensitive).
		errVal := 0.0
		if in.Status.Error != "" {
			errVal = 1.0
		}
		e.Gauge(docError.Name, docError.Unit, docError.Description, errVal, attrs)
		if !in.Status.LastSync.IsZero() {
			e.Gauge(docLastSync.Name, docLastSync.Unit, docLastSync.Description,
				float64(in.Status.LastSync.Unix()), attrs)
		}
	}
	return nil
}
