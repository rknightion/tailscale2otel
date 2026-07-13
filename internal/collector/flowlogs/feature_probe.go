package flowlogs

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/collector"
	"github.com/rknightion/tailscale2otel/v2/internal/semconv"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
)

// defaultFeatureProbeInterval is the cadence used when the probe is constructed
// with a non-positive interval. The feature flag changes rarely, so a slow poll
// keeps the health gauge fresh without meaningful API or cardinality cost.
const defaultFeatureProbeInterval = 300 * time.Second

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
// It mirrors the poller's feature-check semantics exactly: a check error fails
// open (emit nothing, no error); on success it emits feature.enabled=1 when
// enabled, else 0.
type FeatureProbe struct {
	check    FeatureCheck
	interval time.Duration
}

// Compile-time guarantee: *FeatureProbe is a SnapshotCollector.
var _ collector.SnapshotCollector = (*FeatureProbe)(nil)

// NewFeatureProbe returns a FeatureProbe that runs check on each tick and emits
// tailscale.feature.enabled accordingly. interval sets the poll cadence; a
// non-positive value falls back to 300s.
func NewFeatureProbe(check FeatureCheck, interval time.Duration) *FeatureProbe {
	return &FeatureProbe{check: check, interval: interval}
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
// Semantics mirror the poller's (flowlogs.go CollectWindow): on a check error it
// fails open — emits nothing and returns nil (no error). On success it emits the
// gauge =1 when the feature is enabled, else =0, using the same descriptor and
// tailscale.feature attribute the poller uses. A nil check is treated as
// "always enabled".
func (p *FeatureProbe) Collect(ctx context.Context, e telemetry.Emitter) error {
	enabled := true
	if p.check != nil {
		ok, err := p.check(ctx)
		if err != nil {
			// Fail open: emit nothing, not a failure.
			return nil
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
