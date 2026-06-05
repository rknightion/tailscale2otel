// Package postureintegrations is a snapshot collector for the tailnet's
// device-posture integrations (MDM/EDR providers such as Intune). It emits the
// integration count plus per-integration match counts and the last-sync
// timestamp (for staleness alerting — a stalled sync means posture data is
// going stale). The provider identifiers (clientId/tenantId/cloudId) are never
// fetched (see internal/tsapi) and so cannot be emitted.
package postureintegrations

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
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
)

const (
	attrProvider    = "tailscale.posture.provider"
	attrIntegration = "tailscale.posture.integration"
)

// api is the narrow slice of the Tailscale client this collector needs.
type api interface {
	PostureIntegrations(ctx context.Context) ([]tsapi.PostureIntegration, error)
}

// Collector implements collector.SnapshotCollector for posture integrations.
type Collector struct {
	api      api
	interval time.Duration
}

// New returns a posture-integrations collector. A non-positive interval resolves
// to the default (600s).
func New(a api, interval time.Duration) *Collector {
	return &Collector{api: a, interval: interval}
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
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	ints, err := c.api.PostureIntegrations(ctx)
	if err != nil {
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
		if !in.Status.LastSync.IsZero() {
			e.Gauge(docLastSync.Name, docLastSync.Unit, docLastSync.Description,
				float64(in.Status.LastSync.Unix()), attrs)
		}
	}
	return nil
}
