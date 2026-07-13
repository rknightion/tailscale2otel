// Package users is a snapshot collector that reports Tailscale user inventory:
// aggregate counts grouped by role/status/type, plus per-user device count,
// connection state, and last-seen time.
package users

import (
	"context"
	"fmt"
	"strconv"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

// Compile-time assertion that *Collector is a SnapshotCollector.
var _ collector.SnapshotCollector = (*Collector)(nil)

// Metric names emitted by this collector.
const (
	MetricUsersCount   = "tailscale.users.count"
	MetricUserDevices  = "tailscale.user.devices"
	MetricUserConn     = "tailscale.user.connected"
	MetricUserLastSeen = "tailscale.user.last_seen"
	MetricUserInvites  = "tailscale.user_invites.count"
)

// Attribute keys emitted by this collector.
const (
	attrRole   = "tailscale.user.role"
	attrStatus = "tailscale.user.status"
	attrType   = "tailscale.user.type"
	// User identity uses the stable OTel user.* registry (the deprecated
	// enduser.*/tailscale.user.login names are gone as of v2.0.0).
	attrID             = semconv.AttrUserID
	attrLogin          = semconv.AttrUserName
	attrInviteRole     = "tailscale.user_invite.role"
	attrInviteAccepted = "tailscale.user_invite.accepted"
)

const defaultInterval = 300 * time.Second

// lister is the narrow client surface this collector needs. It is satisfied by
// *tsapi.Client.
type lister interface {
	Users(ctx context.Context) ([]tsclient.User, error)
	UserInvites(ctx context.Context) ([]tsapi.UserInvite, error)
}

// Collector reports Tailscale user inventory on each tick.
type Collector struct {
	api          lister
	interval     time.Duration
	perEntity    bool
	activityData bool
}

// Option configures optional Collector behavior.
type Option func(*Collector)

// WithPerEntity controls whether the per-user gauges (devices, connected,
// last_seen) are emitted. The default is true; false (cardinality.per_entity.user)
// emits only the aggregate tailscale.users.count / user_invites.count rollups.
func WithPerEntity(enabled bool) Option {
	return func(c *Collector) { c.perEntity = enabled }
}

// WithActivityData controls whether the per-user device-count and
// currently-connected gauges (tailscale.user.devices, tailscale.user.connected)
// are emitted. The default is true (Tailscale populates both fields natively);
// pass false when the underlying control-plane API has no per-user
// device-count/connection-state concept at all (e.g. Headscale's user API), so
// the collector doesn't report a fabricated 0/not-connected as if it were real
// data. Mirrors how LastSeen is already gated on IsZero for a provider that
// omits it. Per-user gauges are additionally gated by WithPerEntity;
// tailscale.user.last_seen and the aggregate counts are unaffected.
func WithActivityData(enabled bool) Option {
	return func(c *Collector) { c.activityData = enabled }
}

// New returns a users Collector. A non-positive interval falls back to the
// default (300s) via DefaultInterval. Per-user gauges are emitted by default;
// pass WithPerEntity(false) to emit only the aggregate counts. Device-count and
// connected-state gauges are emitted by default; pass WithActivityData(false)
// when the control-plane doesn't report that data.
func New(api lister, interval time.Duration, opts ...Option) *Collector {
	c := &Collector{api: api, interval: interval, perEntity: true, activityData: true}
	for _, o := range opts {
		o(c)
	}
	return c
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

// inviteKey groups user invites for aggregate counting.
type inviteKey struct {
	role     string
	accepted bool
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

		// Per-user gauges (one series per user) are gated by
		// cardinality.per_entity.user; when off, only the aggregate
		// users.count / user_invites.count rollups are emitted.
		if !c.perEntity {
			continue
		}

		idAttrs := telemetry.Attrs{
			attrID:    u.ID,
			attrLogin: u.LoginName,
		}

		// Device-count/connected-state gauges are gated by activityData: some
		// control planes (Headscale) report neither field at all, so emitting
		// them unconditionally would report a fabricated 0/not-connected as if
		// it were real data (issue #64).
		if c.activityData {
			e.Gauge(docUserDevices.Name, docUserDevices.Unit, docUserDevices.Description,
				float64(u.DeviceCount), idAttrs)

			connected := 0.0
			if u.CurrentlyConnected {
				connected = 1.0
			}
			e.Gauge(docUserConnected.Name, docUserConnected.Unit, docUserConnected.Description,
				connected, idAttrs)
		}

		if !u.LastSeen.IsZero() {
			e.Gauge(docUserLastSeen.Name, docUserLastSeen.Unit, docUserLastSeen.Description,
				float64(u.LastSeen.Unix()), idAttrs)
		}
	}

	for k, n := range counts {
		e.Gauge(docUsersCount.Name, docUsersCount.Unit, docUsersCount.Description,
			float64(n), telemetry.Attrs{
				attrRole:   k.role,
				attrStatus: k.status,
				attrType:   k.typ,
			})
	}

	invites, err := c.api.UserInvites(ctx)
	if err != nil {
		return fmt.Errorf("users: invites: %w", err)
	}

	inviteCounts := make(map[inviteKey]int)
	for i := range invites {
		inviteCounts[inviteKey{role: invites[i].Role, accepted: invites[i].Accepted}]++
	}
	for k, n := range inviteCounts {
		e.Gauge(docUserInvites.Name, docUserInvites.Unit, docUserInvites.Description,
			float64(n), telemetry.Attrs{
				attrInviteRole:     k.role,
				attrInviteAccepted: strconv.FormatBool(k.accepted),
			})
	}

	return nil
}
