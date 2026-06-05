// Package settings is a snapshot collector for tailnet feature settings. It
// fetches TailnetSettings each tick and emits one gauge per boolean feature
// (tailscale.setting.enabled, 0/1, keyed by tailscale.setting.name) plus the
// device key-expiry duration in days.
package settings

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

const defaultInterval = 600 * time.Second

// Metric names emitted by this collector.
const (
	metricEnabled     = "tailscale.setting.enabled"
	metricKeyDuration = "tailscale.setting.devices_key_duration"
	metricSettingRole = "tailscale.setting.users_external_tailnets_role"
)

// attrSettingName labels a setting.enabled point with its stable feature name.
const attrSettingName = "tailscale.setting.name"

// attrSettingRole carries the external-tailnets role enum on the role info gauge.
const attrSettingRole = "tailscale.setting.role"

// api is the narrow slice of the Tailscale client this collector needs. It is
// satisfied by *tsapi.Client.
type api interface {
	TailnetSettings(ctx context.Context) (*tsapi.TailnetSettings, error)
}

// Collector implements collector.SnapshotCollector for tailnet settings.
type Collector struct {
	api      api
	interval time.Duration
}

// New returns a settings collector. A non-positive interval resolves to the
// default (600s) via DefaultInterval.
func New(a api, interval time.Duration) *Collector {
	return &Collector{api: a, interval: interval}
}

// Name returns the stable collector identifier.
func (c *Collector) Name() string { return "settings" }

// DefaultInterval returns the configured interval, or 600s when unset.
func (c *Collector) DefaultInterval() time.Duration {
	if c.interval > 0 {
		return c.interval
	}
	return defaultInterval
}

// Collect fetches the current tailnet settings and emits a gauge per boolean
// feature plus the device key-duration gauge.
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	s, err := c.api.TailnetSettings(ctx)
	if err != nil {
		return err
	}

	// Each stable feature name mapped to its current boolean value. Names are
	// snake_case and stable so the time series is comparable across versions.
	bools := []struct {
		name string
		on   bool
	}{
		{"devices_approval", s.DevicesApprovalOn},
		{"devices_auto_updates", s.DevicesAutoUpdatesOn},
		{"users_approval", s.UsersApprovalOn},
		{"network_flow_logging", s.NetworkFlowLoggingOn},
		{"regional_routing", s.RegionalRoutingOn},
		{"posture_identity_collection", s.PostureIdentityCollectionOn},
		{"https_enabled", s.HTTPSEnabled},
		{"acls_externally_managed", s.ACLsExternallyManagedOn},
	}
	for _, b := range bools {
		e.Gauge(docSettingEnabled.Name, docSettingEnabled.Unit, docSettingEnabled.Description,
			boolValue(b.on), telemetry.Attrs{attrSettingName: b.name})
	}

	e.Gauge(docSettingKeyDuration.Name, docSettingKeyDuration.Unit, docSettingKeyDuration.Description,
		float64(s.DevicesKeyDurationDays), nil)

	// Info gauge (constant 1) carrying the external-tailnets role enum as a label.
	e.Gauge(docSettingRole.Name, docSettingRole.Unit, docSettingRole.Description,
		1, telemetry.Attrs{attrSettingRole: s.UsersRoleAllowedToJoinExternalTailnets})

	return nil
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
