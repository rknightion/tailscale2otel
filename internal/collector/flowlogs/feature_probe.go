package flowlogs

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
)

// defaultFeatureProbeInterval is the cadence used when the probe is constructed
// with a non-positive interval. The feature flag changes rarely, so a slow poll
// keeps the health gauge fresh without meaningful API or cardinality cost.
const defaultFeatureProbeInterval = 300 * time.Second

// opGetTailnetSettings is the upstream operationId of the call the FeatureCheck
// makes. The probe is handed an opaque func and cannot see the endpoint itself,
// but flowFeatureCheck (internal/app/collectors.go) is its only implementation
// and it reads GET /tailnet/{tailnet}/settings. Naming the operation honestly
// matters more than naming it defensively: the whole point of the availability
// signal is to tell an operator WHICH endpoint is refusing them.
//
// The settings collector emits this same operation name, which is correct and
// not a collision — the tracker and the metric are both keyed on
// (collector, operation), and these are two genuinely independent probes of one
// endpoint that can fail independently of each other.
const opGetTailnetSettings = "getTailnetSettings"

// FeatureProbe is a standalone SnapshotCollector that emits only the
// tailscale.feature.enabled health gauge for network-flow-logging, by running a
// FeatureCheck independent of the windowed flow-log poller.
//
// In a stream-only deployment (source: stream) the flow-log Collector is not
// registered, so its poll-side feature gauge would never be emitted and the
// health signal would be lost. The probe restores that signal: it can be
// registered when flowlogs is enabled but ingestion is stream-only, emitting the
// same gauge (same descriptor and tailscale.feature attribute) the poller emits.
//
// On a successful check it emits feature.enabled=1 when enabled, else 0. A
// check error emits no gauge and is returned so scheduler status records the
// failed probe; this does not affect the poll collector's fail-open behavior.
type FeatureProbe struct {
	check    FeatureCheck
	interval time.Duration
	now      func() time.Time
	// tracker records this probe's per-operation availability for the admin
	// status page and the capability matrix (#430/#524). A nil tracker is a no-op.
	tracker *apistate.Tracker
}

// Compile-time guarantee: *FeatureProbe is a SnapshotCollector.
var _ collector.SnapshotCollector = (*FeatureProbe)(nil)

// FeatureProbeOption configures optional FeatureProbe behavior.
type FeatureProbeOption func(*FeatureProbe)

// WithFeatureProbeAPIState wires the shared per-operation availability tracker
// (#420). Availability METRICS are emitted regardless; the tracker is the
// in-process introspection copy the admin status page reads. A nil tracker is a
// no-op.
//
// This is named apart from the Collector's WithAPIState because the two live in
// one package and configure different types.
func WithFeatureProbeAPIState(t *apistate.Tracker) FeatureProbeOption {
	return func(p *FeatureProbe) { p.tracker = t }
}

// WithFeatureProbeClock overrides the probe's clock, for deterministic
// last-probe assertions; the default is time.Now.
func WithFeatureProbeClock(now func() time.Time) FeatureProbeOption {
	return func(p *FeatureProbe) {
		if now != nil {
			p.now = now
		}
	}
}

// NewFeatureProbe returns a FeatureProbe that runs check on each tick and emits
// tailscale.feature.enabled accordingly. interval sets the poll cadence; a
// non-positive value falls back to 300s.
func NewFeatureProbe(check FeatureCheck, interval time.Duration, opts ...FeatureProbeOption) *FeatureProbe {
	p := &FeatureProbe{check: check, interval: interval, now: time.Now}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Name returns the stable collector identifier.
func (p *FeatureProbe) Name() string { return "flowlogs-feature" }

// DefaultInterval returns the configured interval, or 300s when non-positive.
func (p *FeatureProbe) DefaultInterval() time.Duration {
	if p.interval > 0 {
		return p.interval
	}
	return defaultFeatureProbeInterval
}

// Collect runs the feature check and emits tailscale.feature.enabled.
//
// On a check error it emits nothing and returns the original error, allowing
// scheduler status to distinguish a failed probe from a disabled feature. On
// success it emits the gauge =1 when the feature is enabled, else =0, using the
// same descriptor and tailscale.feature attribute the poller uses. A nil check
// is treated as "always enabled".
//
// Every check outcome, success included, is also recorded as a bounded
// availability state (#524). Before that the probe was the ONLY thing standing
// in for the poller in a stream-only deployment, and it recorded nothing — so
// the capability row read `unknown` forever and a credential the settings
// endpoint rejects could fire neither api-credential-rejected nor
// api-scope-denied. A nil check makes no API call, so it deliberately records
// nothing rather than claiming a probe that never happened.
//
// Disposition is the default: an ambiguous 403 on the settings endpoint is a
// scope denial. The poller's flowlogsDisposition reads 403 as `disabled`
// because upstream documents 403 as the flow-log feature gate on
// listNetworkFlowLogs specifically — that reading does NOT extend to the
// settings read this probe makes.
func (p *FeatureProbe) Collect(ctx context.Context, e telemetry.Emitter) error {
	enabled := true
	if p.check != nil {
		ok, err := p.check(ctx)
		apistate.Observe(e, p.tracker, p.Name(), opGetTailnetSettings, apistate.Disposition{}, err, p.now())
		if err != nil {
			return err
		}
		enabled = ok
	}
	p.emitFeature(e, enabled)
	return nil
}

// emitFeature records the feature.enabled gauge for network-flow-logging, using
// the same descriptor (docFeatureEnabled) and attribute (featureName) the poller
// emits so the poll and stream-mode signals are identical.
func (p *FeatureProbe) emitFeature(e telemetry.Emitter, enabled bool) {
	var v float64
	if enabled {
		v = 1
	}
	e.Gauge(docFeatureEnabled.Name, docFeatureEnabled.Unit,
		docFeatureEnabled.Description,
		v, telemetry.Attrs{semconv.AttrFeature: featureName})
}
