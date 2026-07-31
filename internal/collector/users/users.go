// Package users is a snapshot collector that reports Tailscale user inventory:
// aggregate counts grouped by role/status/type, plus per-user device count,
// connection state, and last-seen time.
package users

import (
	"context"
	"fmt"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/v4/internal/aclpolicy"
	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/entityage"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

// Compile-time assertion that *Collector is a SnapshotCollector.
var _ collector.SnapshotCollector = (*Collector)(nil)

// Metric names emitted by this collector.
const (
	MetricUsersCount        = "tailscale.users.count"
	MetricUserDevices       = "tailscale.user.devices"
	MetricUserConn          = "tailscale.user.connected"
	MetricUserLastSeen      = "tailscale.user.last_seen"
	MetricUserInvites       = "tailscale.user_invites.count"
	MetricUserInvitePending = "tailscale.user_invites.pending_age"
	MetricUsersAge          = "tailscale.users.age"
)

// Attribute keys emitted by this collector.
const (
	attrRole   = "tailscale.user.role"
	attrStatus = "tailscale.user.status"
	attrType   = "tailscale.user.type"
	// User identity uses the stable OTel user.* registry (the deprecated
	// enduser.*/tailscale.user.login names are gone as of v2.0.0).
	attrID         = semconv.AttrUserID
	attrLogin      = semconv.AttrUserName
	attrInviteRole = "tailscale.user_invite.role"
	// attrInviteDelivery is the bounded HOW-was-this-invite-delivered signal
	// (#412), replacing the fabricated accepted-state label removed by #411.
	// It never carries the invite's email address or its bearer inviteUrl —
	// only one of the deliveryXxx enum values below.
	attrInviteDelivery = "tailscale.user_invite.delivery"
)

// Bounded values for attrInviteDelivery. deliveryUnknown is part of the
// registered enum (see internal/telemetry/pii/registry.go) for forward
// compatibility, but the current wire shape only ever yields emailed or
// manual_link: lastEmailSentAt is either populated (emailed) or absent
// (manual_link, i.e. a shareable link never dispatched by email).
const (
	deliveryEmailed    = "emailed"
	deliveryManualLink = "manual_link"
)

const defaultInterval = 300 * time.Second

// opListUsers is the upstream operationId of the primary user-listing call
// (verbatim from spec/tailscale-api.json).
const opListUsers = "listUsers"

// opUserInvites is the operation name this collector records for the
// UserInvites() subrequest. It is deliberately NOT the upstream operationId
// (which is listUserInvites) — it is the bounded subrequest name declared as
// app.SubrequestUserInvites ("user_invites") in internal/app/capability.go.
// That file's filterOperations joins a capability-matrix row to this
// collector's tracker entries by exact match on the operation string against
// the subrequest name, so recording under the operationId instead would leave
// that row permanently "unknown" (#524). This package cannot import
// internal/app (import cycle: internal/app already imports internal/catalog,
// which would need to import this package), so the literal is duplicated here
// rather than shared — TestUserInvitesRecordedUnderSubrequestNameNotOperationID
// in users_test.go guards against it drifting.
const opUserInvites = "user_invites"

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
	directory    DirectorySink
	now          func() time.Time
	// tracker records this collector's per-operation availability for the admin
	// status page and the capability matrix (#430/#524). A nil tracker is a no-op.
	tracker *apistate.Tracker
}

// DirectorySink receives the login-to-role directory each collect, so the ACL
// evaluator can resolve autogroup:owner and autogroup:admin. It is the narrow
// slice of aclpolicy.Store this collector needs.
type DirectorySink interface {
	SetDirectory(aclpolicy.Directory) error
}

// Option configures optional Collector behavior.
type Option func(*Collector)

// WithDirectorySink publishes the collected login-to-role directory to sink.
func WithDirectorySink(sink DirectorySink) Option {
	return func(c *Collector) { c.directory = sink }
}

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

// WithClock overrides the time source used for the user-age and invite
// pending-age histograms (#412/#426). Defaults to time.Now; intended for
// deterministic tests.
func WithClock(now func() time.Time) Option {
	return func(c *Collector) { c.now = now }
}

// WithAPIState wires the shared per-operation availability tracker (#420).
// Availability METRICS are emitted regardless; the tracker is the in-process
// introspection copy the admin status page reads. A nil tracker is a no-op.
func WithAPIState(t *apistate.Tracker) Option { return func(c *Collector) { c.tracker = t } }

// New returns a users Collector. A non-positive interval falls back to the
// default (300s) via DefaultInterval. Per-user gauges are emitted by default;
// pass WithPerEntity(false) to emit only the aggregate counts. Device-count and
// connected-state gauges are emitted by default; pass WithActivityData(false)
// when the control-plane doesn't report that data.
func New(api lister, interval time.Duration, opts ...Option) *Collector {
	c := &Collector{api: api, interval: interval, perEntity: true, activityData: true, now: time.Now}
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
	delivery string
}

// inviteDelivery classifies how an open invite was (or wasn't) delivered by
// email (#412). The wire has no explicit delivery-method field, so this is
// derived from lastEmailSentAt: populated means Tailscale sent an email,
// absent means the invite exists only as a shareable link nobody has emailed.
func inviteDelivery(i tsapi.UserInvite) string {
	if !i.LastEmailSentAt.IsZero() {
		return deliveryEmailed
	}
	return deliveryManualLink
}

// Collect fetches the current users and emits the inventory metrics. It also
// publishes the login-to-role directory when a sink is configured; a compile
// failure there is deliberately not returned, because the ACL evaluator is a
// side consumer and must never fail the users collect. The failure is retained
// by the sink and surfaced on the status page instead.
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	us, err := c.api.Users(ctx)
	apistate.Observe(e, c.tracker, c.Name(), opListUsers, apistate.Disposition{}, err, c.now())
	if err != nil {
		return fmt.Errorf("users: list: %w", err)
	}

	nowT := c.now()
	counts := make(map[comboKey]int)
	roles := make(map[string]string, len(us))
	for i := range us {
		u := us[i]
		roles[u.LoginName] = string(u.Role)

		k := comboKey{
			role:   string(u.Role),
			status: string(u.Status),
			typ:    string(u.Type),
		}
		counts[k]++

		// Age distribution (#426) — a bounded fleet-level histogram, so unlike
		// the per-user gauges below it is emitted regardless of perEntity.
		// entityage.Seconds reports ok=false for a zero Created (the provider
		// didn't supply it), which must be omitted rather than recorded as a
		// fabricated age of zero.
		if secs, ok := entityage.Seconds(u.Created, nowT); ok {
			e.Histogram(docUsersAge.Name, docUsersAge.Unit, docUsersAge.Description,
				secs, entityage.BucketsSeconds(), nil)
		}

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
	apistate.Observe(e, c.tracker, c.Name(), opUserInvites, apistate.Disposition{}, err, c.now())
	if err != nil {
		return fmt.Errorf("users: invites: %w", err)
	}

	inviteCounts := make(map[inviteKey]int)
	for i := range invites {
		inv := invites[i]
		inviteCounts[inviteKey{role: inv.Role, delivery: inviteDelivery(inv)}]++

		// Pending-age / delivery-staleness (#412): only invites Tailscale has
		// actually emailed carry a delivery timestamp to measure age from: a
		// manual-link invite (zero LastEmailSentAt) has no such timestamp, and
		// entityage.Seconds correctly omits it rather than reporting age zero.
		if secs, ok := entityage.Seconds(inv.LastEmailSentAt, nowT); ok {
			e.Histogram(docUserInvitePendingAge.Name, docUserInvitePendingAge.Unit, docUserInvitePendingAge.Description,
				secs, entityage.BucketsSeconds(), telemetry.Attrs{attrInviteRole: inv.Role})
		}
	}
	for k, n := range inviteCounts {
		e.Gauge(docUserInvites.Name, docUserInvites.Unit, docUserInvites.Description,
			float64(n), telemetry.Attrs{
				attrInviteRole:     k.role,
				attrInviteDelivery: k.delivery,
			})
	}

	if c.directory != nil {
		// A recompile failure is swallowed on purpose: the ACL evaluator is a
		// side consumer of this collector and must never turn a policy problem
		// into a users-collector failure. The sink retains the error for the
		// status page.
		_ = c.directory.SetDirectory(aclpolicy.Directory{Roles: roles})
	}

	return nil
}
