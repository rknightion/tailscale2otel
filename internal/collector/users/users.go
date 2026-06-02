// Package users is a snapshot collector that reports Tailscale user inventory:
// aggregate counts grouped by role/status/type, plus per-user device count,
// connection state, and last-seen time.
package users

import (
	"context"
	"fmt"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// Compile-time assertion that *Collector is a SnapshotCollector.
var _ collector.SnapshotCollector = (*Collector)(nil)

// Metric names emitted by this collector.
const (
	MetricUsersCount   = "tailscale.users.count"
	MetricUserDevices  = "tailscale.user.devices"
	MetricUserConn     = "tailscale.user.connected"
	MetricUserLastSeen = "tailscale.user.last_seen"
)

// Attribute keys emitted by this collector.
const (
	attrRole   = "tailscale.user.role"
	attrStatus = "tailscale.user.status"
	attrType   = "tailscale.user.type"
	attrID     = "enduser.id"
	attrLogin  = "tailscale.user.login"
)

const defaultInterval = 300 * time.Second

// lister is the narrow client surface this collector needs. It is satisfied by
// *tsapi.Client.
type lister interface {
	Users(ctx context.Context) ([]tsclient.User, error)
}

// Collector reports Tailscale user inventory on each tick.
type Collector struct {
	api      lister
	interval time.Duration
}

// New returns a users Collector. A non-positive interval falls back to the
// default (300s) via DefaultInterval.
func New(api lister, interval time.Duration) *Collector {
	return &Collector{api: api, interval: interval}
}

// Name returns the stable collector identifier.
func (c *Collector) Name() string { return "users" }

// DefaultInterval returns the configured interval, or 300s if non-positive.
func (c *Collector) DefaultInterval() time.Duration {
	if c.interval > 0 {
		return c.interval
	}
	return defaultInterval
}

// comboKey groups users for aggregate counting.
type comboKey struct {
	role, status, typ string
}

// Collect fetches the current users and emits the inventory metrics.
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	us, err := c.api.Users(ctx)
	if err != nil {
		return fmt.Errorf("users: list: %w", err)
	}

	counts := make(map[comboKey]int)
	for i := range us {
		u := us[i]

		k := comboKey{
			role:   string(u.Role),
			status: string(u.Status),
			typ:    string(u.Type),
		}
		counts[k]++

		idAttrs := telemetry.Attrs{
			attrID:    u.ID,
			attrLogin: u.LoginName,
		}

		e.Gauge(MetricUserDevices, semconv.UnitDimensionless, "User device count.",
			float64(u.DeviceCount), idAttrs)

		connected := 0.0
		if u.CurrentlyConnected {
			connected = 1.0
		}
		e.Gauge(MetricUserConn, semconv.UnitDimensionless, "Whether the user is currently connected (1) or not (0).",
			connected, idAttrs)

		if !u.LastSeen.IsZero() {
			e.Gauge(MetricUserLastSeen, semconv.UnitSeconds, "User last-seen time as a Unix timestamp.",
				float64(u.LastSeen.Unix()), idAttrs)
		}
	}

	for k, n := range counts {
		e.Gauge(MetricUsersCount, semconv.UnitDimensionless, "Number of users grouped by role, status, and type.",
			float64(n), telemetry.Attrs{
				attrRole:   k.role,
				attrStatus: k.status,
				attrType:   k.typ,
			})
	}

	return nil
}
