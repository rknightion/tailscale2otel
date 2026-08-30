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
	"encoding/json"
	"sort"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/snapshot"
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
	defaultSnapshotHeartbeat = 24 * time.Hour
	defaultSnapshotBodyBytes = 32 * 1024
	// EventPostureIntegrationsSnapshot is the opt-in JSON posture-integration
	// inventory snapshot event.
	EventPostureIntegrationsSnapshot = "tailscale.posture_integrations.snapshot"
)

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
	now               func() time.Time
	snapshotEnabled   bool
	snapshotHeartbeat time.Duration
	snapshotBodyBytes int
	snapshotEmitter   *snapshot.Emitter
}

// Option configures optional Collector behavior.
type Option func(*Collector)

// WithAPIState wires the shared per-operation availability tracker (#420).
// Availability METRICS are emitted regardless; the tracker is the in-process
// introspection copy the admin status page reads.
func WithAPIState(t *apistate.Tracker) Option {
	return func(c *Collector) { c.tracker = t }
}

// WithClock overrides the collector clock. It is primarily useful to make
// snapshot-heartbeat tests deterministic; the default is time.Now.
func WithClock(now func() time.Time) Option {
	return func(c *Collector) { c.now = now }
}

// WithSnapshot enables the safe JSON posture-integration snapshot. The
// optional body limit should match otlp.limits.log_body_bytes; when omitted or
// non-positive, the telemetry default (32 KiB) is used. The snapshot is emitted
// on the first observation, on content changes, and on a daily heartbeat.
func WithSnapshot(enabled bool, maxBodyBytes ...int) Option {
	return func(c *Collector) {
		c.snapshotEnabled = enabled
		if len(maxBodyBytes) > 0 && maxBodyBytes[0] > 0 {
			c.snapshotBodyBytes = maxBodyBytes[0]
		}
	}
}

// WithSnapshotHeartbeat overrides the default daily heartbeat. It exists for
// deterministic tests and leaves the public configuration surface unchanged.
func WithSnapshotHeartbeat(heartbeat time.Duration) Option {
	return func(c *Collector) {
		c.snapshotHeartbeat = heartbeat
	}
}

// New returns a posture-integrations collector. A non-positive interval resolves
// to the default (600s).
func New(a api, interval time.Duration, opts ...Option) *Collector {
	c := &Collector{
		api:               a,
		interval:          interval,
		now:               time.Now,
		snapshotHeartbeat: defaultSnapshotHeartbeat,
		snapshotBodyBytes: defaultSnapshotBodyBytes,
	}
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
	if c.snapshotEnabled {
		body, err := marshalSnapshot(ints)
		if err != nil {
			return err
		}
		c.emitSnapshot(e, body)
	}

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

// snapshotIntegration is the safe subset of the posture response. The
// provider credentials (clientId, tenantId, cloudId, and any secret) are not
// part of tsapi.PostureIntegration and can therefore never reach this body.
// The upstream error text is reduced to a boolean for the same reason as the
// metric: sync diagnostics may contain provider or tenant details.
type snapshotIntegration struct {
	ID       string                    `json:"id"`
	Provider string                    `json:"provider"`
	Status   snapshotIntegrationStatus `json:"status"`
}

type snapshotIntegrationStatus struct {
	LastSync             string `json:"lastSync,omitempty"`
	MatchedCount         int64  `json:"matchedCount"`
	PossibleMatchedCount int64  `json:"possibleMatchedCount"`
	ProviderHostCount    int64  `json:"providerHostCount"`
	Error                bool   `json:"error"`
}

type snapshotIntegrations struct {
	Integrations []snapshotIntegration `json:"integrations"`
}

func marshalSnapshot(ints []tsapi.PostureIntegration) (string, error) {
	ordered := append([]tsapi.PostureIntegration(nil), ints...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].ID != ordered[j].ID {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].Provider < ordered[j].Provider
	})

	out := make([]snapshotIntegration, len(ordered))
	for i, integration := range ordered {
		status := integration.Status
		out[i] = snapshotIntegration{
			ID:       integration.ID,
			Provider: integration.Provider,
			Status: snapshotIntegrationStatus{
				LastSync:             snapshotTimestamp(status.LastSync),
				MatchedCount:         status.MatchedCount,
				PossibleMatchedCount: status.PossibleMatchedCount,
				ProviderHostCount:    status.ProviderHostCount,
				Error:                status.Error != "",
			},
		}
	}

	body, err := json.Marshal(snapshotIntegrations{Integrations: out})
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func snapshotTimestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (c *Collector) emitSnapshot(e telemetry.Emitter, body string) {
	if c.snapshotEmitter == nil {
		emitter, err := snapshot.New(snapshot.Config{
			Emitter:      e,
			EventName:    EventPostureIntegrationsSnapshot,
			Kind:         snapshot.KindPostureIntegrations,
			Heartbeat:    c.snapshotHeartbeat,
			MaxBodyBytes: c.snapshotBodyBytes,
		})
		if err != nil {
			return
		}
		c.snapshotEmitter = emitter
	}
	c.snapshotEmitter.Observe(c.now(), "", body, nil)
}
