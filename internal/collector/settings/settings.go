// Package settings is a snapshot collector for tailnet feature settings. It
// fetches TailnetSettings each tick and emits one gauge per boolean feature
// (tailscale.setting.enabled, 0/1, keyed by tailscale.setting.name) plus the
// device key-expiry duration in days.
package settings

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/snapshot"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

const defaultInterval = 600 * time.Second

const (
	defaultSnapshotHeartbeat = 24 * time.Hour
	defaultSnapshotBodyBytes = 32 * 1024
	// EventSettingsSnapshot is the opt-in JSON tailnet-settings snapshot event.
	EventSettingsSnapshot = "tailscale.settings.snapshot"
)

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

// opGetTailnetSettings is the upstream operationId of the settings fetch call.
const opGetTailnetSettings = "getTailnetSettings"

// settingsDisposition is the DEFAULT disposition (#420/#524): 403 stays
// scope_denied. Tailnet settings are available on every plan, so upstream has
// no reason to answer 403 as a feature gate here — a 403 on this path means
// the credential is missing the settings read scope, and reading it as
// "disabled" would hide exactly that.
var settingsDisposition = apistate.Disposition{}

// api is the narrow slice of the Tailscale client this collector needs. It is
// satisfied by *tsapi.Client.
type api interface {
	TailnetSettings(ctx context.Context) (*tsapi.TailnetSettings, error)
}

// Collector implements collector.SnapshotCollector for tailnet settings.
type Collector struct {
	api      api
	interval time.Duration
	// tracker records this collector's per-operation availability for the admin
	// status page and the capability matrix (#430/#524). A nil tracker is a no-op.
	tracker *apistate.Tracker
	// now is the clock, injectable from tests.
	now func() time.Time

	// snapshot is opt-in because the settings response contains the complete
	// tailnet configuration. The external ACL link is reduced to a presence
	// boolean before serialization; the link itself is never telemetry.
	snapshotEnabled   bool
	snapshotHeartbeat time.Duration
	snapshotBodyBytes int
	snapshotEmitter   *snapshot.Emitter
}

// Option configures optional Collector behavior.
type Option func(*Collector)

// WithAPIState wires the shared per-operation availability tracker (#420).
// Availability METRICS are emitted regardless; the tracker is the in-process
// introspection copy the admin status page reads. A nil tracker is a no-op.
func WithAPIState(t *apistate.Tracker) Option { return func(c *Collector) { c.tracker = t } }

// WithClock overrides the collector's clock (for deterministic last-probe
// timestamp tests); the default is time.Now.
func WithClock(now func() time.Time) Option {
	return func(c *Collector) { c.now = now }
}

// WithSnapshot enables the JSON tailnet-settings snapshot. The optional body
// limit should match otlp.limits.log_body_bytes; when omitted or non-positive,
// the telemetry default (32 KiB) is used. The snapshot is emitted on the first
// observation, on content changes, and on a daily heartbeat when unchanged.
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

// New returns a settings collector. A non-positive interval resolves to the
// default (600s) via DefaultInterval.
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
	apistate.Observe(e, c.tracker, c.Name(), opGetTailnetSettings, settingsDisposition, err, c.now())
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

	// aclsExternalLink is gated behind the policy_file:read scope, separately
	// from the rest of tailnet settings, so it can be absent from the wire
	// response even when the rest of TailnetSettings decoded fine (#418). A nil
	// pointer means "key absent" (unsupported for this credential/plan, or the
	// scope isn't granted) and must be treated as ABSENCE — no data point at
	// all — never a healthy-looking false. Only the derived presence boolean is
	// ever emitted; the URI value itself (which can leak an internal repo path)
	// never is.
	if s.ACLsExternalLink != nil {
		e.Gauge(docSettingEnabled.Name, docSettingEnabled.Unit, docSettingEnabled.Description,
			boolValue(*s.ACLsExternalLink != ""), telemetry.Attrs{attrSettingName: "acls_external_link_set"})
	}

	e.Gauge(docSettingKeyDuration.Name, docSettingKeyDuration.Unit, docSettingKeyDuration.Description,
		float64(s.DevicesKeyDurationDays), nil)

	// Info gauge (constant 1) carrying the external-tailnets role enum as a label.
	e.Gauge(docSettingRole.Name, docSettingRole.Unit, docSettingRole.Description,
		1, telemetry.Attrs{attrSettingRole: s.UsersRoleAllowedToJoinExternalTailnets})

	if c.snapshotEnabled {
		body, err := marshalSnapshot(s)
		if err != nil {
			return err
		}
		if err := c.emitSnapshot(e, body); err != nil {
			return err
		}
	}

	return nil
}

// snapshotSettings mirrors the JSON names of the settings endpoint while
// preserving the existing privacy fence around ACLsExternalLink. The pointer
// boolean keeps the API's absent, present-but-empty, and present-and-set states
// distinguishable without exporting the URI itself.
type snapshotSettings struct {
	DevicesApprovalOn                      bool   `json:"devicesApprovalOn"`
	DevicesAutoUpdatesOn                   bool   `json:"devicesAutoUpdatesOn"`
	DevicesKeyDurationDays                 int    `json:"devicesKeyDurationDays"`
	UsersApprovalOn                        bool   `json:"usersApprovalOn"`
	UsersRoleAllowedToJoinExternalTailnets string `json:"usersRoleAllowedToJoinExternalTailnets"`
	NetworkFlowLoggingOn                   bool   `json:"networkFlowLoggingOn"`
	RegionalRoutingOn                      bool   `json:"regionalRoutingOn"`
	PostureIdentityCollectionOn            bool   `json:"postureIdentityCollectionOn"`
	HTTPSEnabled                           bool   `json:"httpsEnabled"`
	ACLsExternallyManagedOn                bool   `json:"aclsExternallyManagedOn"`
	ACLsExternalLinkSet                    *bool  `json:"aclsExternalLinkSet,omitempty"`
}

func marshalSnapshot(settings *tsapi.TailnetSettings) (string, error) {
	var linkSet *bool
	if settings.ACLsExternalLink != nil {
		value := *settings.ACLsExternalLink != ""
		linkSet = &value
	}
	body, err := json.Marshal(snapshotSettings{
		DevicesApprovalOn:                      settings.DevicesApprovalOn,
		DevicesAutoUpdatesOn:                   settings.DevicesAutoUpdatesOn,
		DevicesKeyDurationDays:                 settings.DevicesKeyDurationDays,
		UsersApprovalOn:                        settings.UsersApprovalOn,
		UsersRoleAllowedToJoinExternalTailnets: settings.UsersRoleAllowedToJoinExternalTailnets,
		NetworkFlowLoggingOn:                   settings.NetworkFlowLoggingOn,
		RegionalRoutingOn:                      settings.RegionalRoutingOn,
		PostureIdentityCollectionOn:            settings.PostureIdentityCollectionOn,
		HTTPSEnabled:                           settings.HTTPSEnabled,
		ACLsExternallyManagedOn:                settings.ACLsExternallyManagedOn,
		ACLsExternalLinkSet:                    linkSet,
	})
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (c *Collector) emitSnapshot(e telemetry.Emitter, body string) error {
	if c.snapshotEmitter == nil {
		emitter, err := snapshot.New(snapshot.Config{
			Emitter:      e,
			EventName:    EventSettingsSnapshot,
			Kind:         snapshot.KindSettings,
			Heartbeat:    c.snapshotHeartbeat,
			MaxBodyBytes: c.snapshotBodyBytes,
		})
		if err != nil {
			return err
		}
		c.snapshotEmitter = emitter
	}
	c.snapshotEmitter.Observe(c.now(), "", body, nil)
	return nil
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
