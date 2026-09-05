// Package pam collects Border0-only Tailscale PAM inventory and configuration
// shape. It deliberately does not restate Tailscale Service ports or PAM audit
// changes, which are owned by the services and auditlogs collectors.
package pam

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/apistate"
	"github.com/rknightion/tailscale2otel/v5/internal/b0api"
	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/snapshot"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
)

const defaultInterval = 10 * time.Minute

const (
	defaultSnapshotHeartbeat = 24 * time.Hour
	defaultSnapshotBodyBytes = 32 * 1024
	// EventSnapshot is the opt-in safe PAM inventory/configuration snapshot.
	EventSnapshot = "tailscale.pam.snapshot"
	// EventSession is the opt-in, PII-filtered record of an accepted PAM session.
	EventSession = "tailscale.pam.session"
)

const (
	metricConnectors            = "tailscale.pam.connectors"
	metricConnectorConnected    = "tailscale.pam.connector.connected"
	metricConnectorLastSeenAge  = "tailscale.pam.connector.last_seen_age"
	metricConnectorSockets      = "tailscale.pam.connector.sockets"
	metricConnectorArtifacts    = "tailscale.pam.connector.tokens"
	metricConnectorPlugins      = "tailscale.pam.connector.plugins"
	metricConnectorInfo         = "tailscale.pam.connector.info"
	metricServices              = "tailscale.pam.services"
	metricServiceAlive          = "tailscale.pam.service.alive"
	metricServiceSettingEnabled = "tailscale.pam.service.setting.enabled"
	metricPolicies              = "tailscale.pam.policies"
	metricPolicySettingEnabled  = "tailscale.pam.policy.setting.enabled"
	metricIdentities            = "tailscale.pam.identities"
	metricOrgSettingEnabled     = "tailscale.pam.org.setting.enabled"
	metricOrgPlanInfo           = "tailscale.pam.org.plan.info"
	metricSubscriptionLimit     = "tailscale.pam.subscription.limit"
)

const (
	attrConnectorName = "tailscale.pam.connector.name"
	attrVersion       = "tailscale.pam.version"
	attrBuiltDate     = "tailscale.pam.built_date"
	attrServiceName   = "tailscale.pam.service.name"
	attrServiceType   = "tailscale.pam.service.type"
	attrSettingName   = "tailscale.pam.setting.name"
	attrPolicyName    = "tailscale.pam.policy.name"
	attrIdentityKind  = "tailscale.pam.identity.kind"
	attrIdentityRole  = "tailscale.pam.identity.role"
	attrPlan          = "tailscale.pam.plan"
	attrLimitName     = "tailscale.pam.limit.name"
)

const (
	opConnectors             = "border0GetConnectors"
	opSockets                = "border0GetSockets"
	opPolicies               = "border0GetPolicies"
	opIAMUsers               = "border0GetIAMUsers"
	opIAMGroups              = "border0GetIAMGroups"
	opIAMServiceAccounts     = "border0GetIAMServiceAccounts"
	opOrganization           = "border0GetOrganization"
	opUpstreamConfigurations = "border0GetSocketUpstreamConfigurations"
)

var pamDisposition = apistate.Disposition{}

type api interface {
	Connectors(context.Context) ([]b0api.Connector, error)
	Sockets(context.Context) ([]b0api.Socket, error)
	Policies(context.Context) ([]b0api.Policy, error)
	IAMUsers(context.Context) ([]b0api.IAMUser, error)
	IAMGroups(context.Context) ([]b0api.IAMGroup, error)
	IAMServiceAccounts(context.Context) ([]b0api.ServiceAccount, error)
	Organization(context.Context) (*b0api.Organization, error)
	SocketUpstreamConfigurations(context.Context, string) ([]b0api.UpstreamConfiguration, error)
}

var (
	_ collector.SnapshotCollector = (*Collector)(nil)
	_ api                         = (*b0api.Client)(nil)
)

// Collector collects the current PAM inventory and configuration shape.
type Collector struct {
	api      api
	interval time.Duration
	tracker  *apistate.Tracker
	now      func() time.Time

	snapshotEnabled   bool
	snapshotHeartbeat time.Duration
	snapshotBodyBytes int
	snapshotEmitter   *snapshot.Emitter
}

// Option configures optional collector behavior.
type Option func(*Collector)

// WithAPIState wires the shared per-operation availability tracker.
func WithAPIState(tracker *apistate.Tracker) Option {
	return func(c *Collector) { c.tracker = tracker }
}

// WithClock overrides the wall clock for deterministic age and heartbeat tests.
func WithClock(now func() time.Time) Option { return func(c *Collector) { c.now = now } }

// WithSnapshot enables the safe PAM inventory/configuration snapshot.
func WithSnapshot(enabled bool, maxBodyBytes ...int) Option {
	return func(c *Collector) {
		c.snapshotEnabled = enabled
		if len(maxBodyBytes) > 0 && maxBodyBytes[0] > 0 {
			c.snapshotBodyBytes = maxBodyBytes[0]
		}
	}
}

// WithSnapshotHeartbeat overrides the default daily unchanged-state heartbeat.
func WithSnapshotHeartbeat(heartbeat time.Duration) Option {
	return func(c *Collector) { c.snapshotHeartbeat = heartbeat }
}

// New returns a PAM inventory/configuration collector.
func New(a api, interval time.Duration, opts ...Option) *Collector {
	c := &Collector{
		api:               a,
		interval:          interval,
		now:               time.Now,
		snapshotHeartbeat: defaultSnapshotHeartbeat,
		snapshotBodyBytes: defaultSnapshotBodyBytes,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Name returns the stable collector identifier.
func (c *Collector) Name() string { return "pam" }

// DefaultInterval returns the configured interval, or ten minutes when unset.
func (c *Collector) DefaultInterval() time.Duration {
	if c.interval > 0 {
		return c.interval
	}
	return defaultInterval
}

// Collect fetches all inventory/configuration families and emits their current
// state. Every Border0 403 retains the default scope_denied disposition.
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	connectors, err := c.api.Connectors(ctx)
	if c.observe(e, opConnectors, err) != nil {
		return err
	}
	sockets, err := c.api.Sockets(ctx)
	if c.observe(e, opSockets, err) != nil {
		return err
	}
	policies, err := c.api.Policies(ctx)
	if c.observe(e, opPolicies, err) != nil {
		return err
	}
	users, err := c.api.IAMUsers(ctx)
	if c.observe(e, opIAMUsers, err) != nil {
		return err
	}
	groups, err := c.api.IAMGroups(ctx)
	if c.observe(e, opIAMGroups, err) != nil {
		return err
	}
	serviceAccounts, err := c.api.IAMServiceAccounts(ctx)
	if c.observe(e, opIAMServiceAccounts, err) != nil {
		return err
	}
	organization, err := c.api.Organization(ctx)
	if c.observe(e, opOrganization, err) != nil {
		return err
	}
	if organization == nil {
		return fmt.Errorf("pam: %s returned no organization", opOrganization)
	}

	c.emitConnectors(e, connectors)
	c.emitServices(e, sockets)
	c.emitPolicies(e, policies)
	c.emitIdentities(e, users, groups, serviceAccounts)
	c.emitOrganization(e, organization)

	if !c.snapshotEnabled {
		return nil
	}
	upstream := make(map[string][]b0api.UpstreamConfiguration, len(sockets))
	for i := range sockets {
		configs, fetchErr := c.api.SocketUpstreamConfigurations(ctx, sockets[i].SocketID)
		if c.observe(e, opUpstreamConfigurations, fetchErr) != nil {
			return fetchErr
		}
		upstream[sockets[i].SocketID] = configs
	}
	body, err := marshalSnapshot(connectors, sockets, policies, users, groups, serviceAccounts, organization, upstream)
	if err != nil {
		return err
	}
	return c.emitSnapshot(e, body)
}

func (c *Collector) observe(e telemetry.Emitter, operation string, err error) error {
	apistate.Observe(e, c.tracker, c.Name(), operation, pamDisposition, err, c.now())
	return err
}

func (c *Collector) emitConnectors(e telemetry.Emitter, connectors []b0api.Connector) {
	e.Gauge(docConnectors.Name, docConnectors.Unit, docConnectors.Description, float64(len(connectors)), nil)
	connected := make([]telemetry.GaugePoint, 0, len(connectors))
	lastSeen := make([]telemetry.GaugePoint, 0, len(connectors))
	sockets := make([]telemetry.GaugePoint, 0, len(connectors))
	tokens := make([]telemetry.GaugePoint, 0, len(connectors))
	plugins := make([]telemetry.GaugePoint, 0, len(connectors))
	info := make([]telemetry.GaugePoint, 0, len(connectors))
	now := c.now()
	for i := range connectors {
		connector := &connectors[i]
		attrs := telemetry.Attrs{attrConnectorName: connector.Name}
		connected = append(connected, telemetry.GaugePoint{Value: boolValue(connector.IsConnected), Attrs: attrs})
		if !connector.LastSeenAt.IsZero() {
			age := now.Sub(connector.LastSeenAt).Seconds()
			if age < 0 {
				age = 0
			}
			lastSeen = append(lastSeen, telemetry.GaugePoint{Value: age, Attrs: attrs})
		}
		sockets = append(sockets, telemetry.GaugePoint{Value: float64(connector.Sockets), Attrs: attrs})
		tokens = append(tokens, telemetry.GaugePoint{Value: float64(connector.ActiveTokens), Attrs: attrs})
		plugins = append(plugins, telemetry.GaugePoint{Value: float64(connector.ActivePlugins), Attrs: attrs})
		meta := connector.Metadata.ConnectorInternalMetadata
		info = append(info, telemetry.GaugePoint{Value: 1, Attrs: telemetry.Attrs{
			attrConnectorName: connector.Name,
			attrVersion:       meta.Version,
			attrBuiltDate:     meta.BuiltDate,
		}})
	}
	emitGaugeSnapshot(e, docConnectorConnected.Name, docConnectorConnected.Unit, docConnectorConnected.Description, connected)
	emitGaugeSnapshot(e, docConnectorLastSeenAge.Name, docConnectorLastSeenAge.Unit, docConnectorLastSeenAge.Description, lastSeen)
	emitGaugeSnapshot(e, docConnectorSockets.Name, docConnectorSockets.Unit, docConnectorSockets.Description, sockets)
	emitGaugeSnapshot(e, docConnectorTokens.Name, docConnectorTokens.Unit, docConnectorTokens.Description, tokens)
	emitGaugeSnapshot(e, docConnectorPlugins.Name, docConnectorPlugins.Unit, docConnectorPlugins.Description, plugins)
	emitGaugeSnapshot(e, docConnectorInfo.Name, docConnectorInfo.Unit, docConnectorInfo.Description, info)
}

func (c *Collector) emitServices(e telemetry.Emitter, sockets []b0api.Socket) {
	counts := map[string]int{}
	alive := make([]telemetry.GaugePoint, 0, len(sockets))
	settings := make([]telemetry.GaugePoint, 0, len(sockets)*8)
	for i := range sockets {
		service := &sockets[i]
		counts[service.SocketType]++
		base := telemetry.Attrs{attrServiceName: service.Name, attrServiceType: service.SocketType}
		alive = append(alive, telemetry.GaugePoint{Value: boolValue(service.Alive), Attrs: base})
		bools := []struct {
			name string
			on   bool
		}{
			{"recording_enabled", service.RecordingEnabled},
			{"end_to_end_encryption_enabled", service.EndToEndEncryptionEnabled},
			{"cloud_authentication_enabled", service.CloudAuthenticationEnabled},
			{"connector_authentication_enabled", service.ConnectorAuthenticationEnabled},
			{"private_socket", service.PrivateSocket},
			{"protected_socket", service.ProtectedSocket},
			{"connector_managed", service.ConnectorManaged},
			{"private_network_enabled", service.PrivateNetworkEnabled},
		}
		for _, setting := range bools {
			attrs := cloneAttrs(base)
			attrs[attrSettingName] = setting.name
			settings = append(settings, telemetry.GaugePoint{Value: boolValue(setting.on), Attrs: attrs})
		}
	}
	serviceCounts := make([]telemetry.GaugePoint, 0, len(counts))
	for serviceType, count := range counts {
		serviceCounts = append(serviceCounts, telemetry.GaugePoint{
			Value: float64(count), Attrs: telemetry.Attrs{attrServiceType: serviceType},
		})
	}
	sortGaugePoints(serviceCounts, attrServiceType)
	emitGaugeSnapshot(e, docServices.Name, docServices.Unit, docServices.Description, serviceCounts)
	emitGaugeSnapshot(e, docServiceAlive.Name, docServiceAlive.Unit, docServiceAlive.Description, alive)
	emitGaugeSnapshot(e, docServiceSettingEnabled.Name, docServiceSettingEnabled.Unit, docServiceSettingEnabled.Description, settings)
}

func (c *Collector) emitPolicies(e telemetry.Emitter, policies []b0api.Policy) {
	e.Gauge(docPolicies.Name, docPolicies.Unit, docPolicies.Description, float64(len(policies)), nil)
	settings := make([]telemetry.GaugePoint, 0, len(policies)*3)
	for i := range policies {
		policy := &policies[i]
		for _, setting := range []struct {
			name string
			on   bool
		}{{"org_wide", policy.OrgWide}, {"read_only", policy.ReadOnly}, {"expires", policy.Expires}} {
			settings = append(settings, telemetry.GaugePoint{Value: boolValue(setting.on), Attrs: telemetry.Attrs{
				attrPolicyName:  policy.Name,
				attrVersion:     policy.Version,
				attrSettingName: setting.name,
			}})
		}
	}
	emitGaugeSnapshot(e, docPolicySettingEnabled.Name, docPolicySettingEnabled.Unit, docPolicySettingEnabled.Description, settings)
}

func (c *Collector) emitIdentities(e telemetry.Emitter, users []b0api.IAMUser, groups []b0api.IAMGroup, accounts []b0api.ServiceAccount) {
	type key struct{ kind, role string }
	counts := map[key]int{}
	for i := range users {
		counts[key{"user", users[i].Role}]++
	}
	for range groups {
		counts[key{kind: "group"}]++
	}
	for i := range accounts {
		counts[key{"service_account", accounts[i].Role}]++
	}
	points := make([]telemetry.GaugePoint, 0, len(counts))
	for bucket, count := range counts {
		points = append(points, telemetry.GaugePoint{Value: float64(count), Attrs: telemetry.Attrs{
			attrIdentityKind: bucket.kind,
			attrIdentityRole: bucket.role,
		}})
	}
	sort.Slice(points, func(i, j int) bool {
		leftKind := fmt.Sprint(points[i].Attrs[attrIdentityKind])
		rightKind := fmt.Sprint(points[j].Attrs[attrIdentityKind])
		if leftKind != rightKind {
			return leftKind < rightKind
		}
		return fmt.Sprint(points[i].Attrs[attrIdentityRole]) < fmt.Sprint(points[j].Attrs[attrIdentityRole])
	})
	emitGaugeSnapshot(e, docIdentities.Name, docIdentities.Unit, docIdentities.Description, points)
}

func (c *Collector) emitOrganization(e telemetry.Emitter, organization *b0api.Organization) {
	settings := []struct {
		name string
		on   bool
	}{
		{"mfa_required", organization.MFARequired},
		{"private_network_enabled", organization.PrivateNetworkEnabled},
		{"dns_management_enabled", organization.DNSManagementEnabled},
		{"needs_reauth", organization.NeedsReauth},
		{"ai_assistants_disabled", organization.Metadata.AIAssistantsDisabled},
		{"ai_session_analysis_disabled", organization.Metadata.AISessionAnalysisDisabled},
		{"setup_wizard.completed", organization.SetupWizard.Completed},
	}
	for _, setting := range settings {
		e.Gauge(docOrgSettingEnabled.Name, docOrgSettingEnabled.Unit, docOrgSettingEnabled.Description,
			boolValue(setting.on), telemetry.Attrs{attrSettingName: setting.name})
	}
	e.Gauge(docOrgPlanInfo.Name, docOrgPlanInfo.Unit, docOrgPlanInfo.Description, 1,
		telemetry.Attrs{attrPlan: organization.Subscription.Plan.Slug})

	limits := organization.Subscription.SubscriptionLimit
	for _, limit := range []struct {
		name  string
		value int64
	}{
		{"socket_count", limits.SocketCount},
		{"socket_tcp_count", limits.SocketTCPCount},
		{"user_count", limits.UserCount},
		{"admin_user_count", limits.AdminUserCount},
		{"custom_domain_count", limits.CustomDomainCount},
		{"custom_idp_count", limits.CustomIDPCount},
		{"notification_count", limits.NotificationCount},
	} {
		e.Gauge(docSubscriptionLimit.Name, docSubscriptionLimit.Unit, docSubscriptionLimit.Description,
			float64(limit.value), telemetry.Attrs{attrLimitName: limit.name})
	}
}

func emitGaugeSnapshot(e telemetry.Emitter, name, unit, description string, points []telemetry.GaugePoint) {
	e.GaugeSnapshot(name, unit, description, points)
}

func boolValue(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func cloneAttrs(in telemetry.Attrs) telemetry.Attrs {
	out := make(telemetry.Attrs, len(in)+1)
	for key, value := range in {
		out[key] = value
	}
	return out
}

func sortGaugePoints(points []telemetry.GaugePoint, key string) {
	sort.Slice(points, func(i, j int) bool { return fmt.Sprint(points[i].Attrs[key]) < fmt.Sprint(points[j].Attrs[key]) })
}

type safeSnapshot struct {
	Connectors     []safeConnector        `json:"connectors"`
	Services       []safeService          `json:"services"`
	Policies       []safePolicy           `json:"policies"`
	Identities     []safeIdentityCount    `json:"identities"`
	Organization   safeOrganization       `json:"organization"`
	UpstreamShapes []safeServiceUpstreams `json:"upstream_configurations"`
}

type safeConnector struct {
	Name          string `json:"name"`
	Connected     bool   `json:"connected"`
	ActiveTokens  int    `json:"active_tokens"`
	ActivePlugins int    `json:"active_plugins"`
	Sockets       int    `json:"sockets"`
	LastSeenAt    string `json:"last_seen_at,omitempty"`
	Version       string `json:"version,omitempty"`
	BuiltDate     string `json:"built_date,omitempty"`
}

type safeService struct {
	Name                           string `json:"name"`
	Type                           string `json:"type"`
	Alive                          bool   `json:"alive"`
	RecordingEnabled               bool   `json:"recording_enabled"`
	EndToEndEncryptionEnabled      bool   `json:"end_to_end_encryption_enabled"`
	CloudAuthenticationEnabled     bool   `json:"cloud_authentication_enabled"`
	ConnectorAuthenticationEnabled bool   `json:"connector_authentication_enabled"`
	PrivateSocket                  bool   `json:"private_socket"`
	ProtectedSocket                bool   `json:"protected_socket"`
	ConnectorManaged               bool   `json:"connector_managed"`
	PrivateNetworkEnabled          bool   `json:"private_network_enabled"`
}

type safePolicy struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	OrgWide  bool   `json:"org_wide"`
	ReadOnly bool   `json:"read_only"`
	Expires  bool   `json:"expires"`
}

type safeIdentityCount struct {
	Kind  string `json:"kind"`
	Role  string `json:"role,omitempty"`
	Count int    `json:"count"`
}

type safeOrganization struct {
	MFARequired               bool             `json:"mfa_required"`
	PrivateNetworkEnabled     bool             `json:"private_network_enabled"`
	DNSManagementEnabled      bool             `json:"dns_management_enabled"`
	NeedsReauth               bool             `json:"needs_reauth"`
	AIAssistantsDisabled      bool             `json:"ai_assistants_disabled"`
	AISessionAnalysisDisabled bool             `json:"ai_session_analysis_disabled"`
	SetupWizardCompleted      bool             `json:"setup_wizard_completed"`
	Plan                      string           `json:"plan"`
	SubscriptionLimits        map[string]int64 `json:"subscription_limits"`
}

type safeServiceUpstreams struct {
	ServiceName    string              `json:"service_name"`
	Configurations []safeUpstreamShape `json:"configurations"`
}

type safeUpstreamShape struct {
	ServiceType        string `json:"service_type"`
	ServiceSubtype     string `json:"service_subtype,omitempty"`
	Protocol           string `json:"protocol,omitempty"`
	Port               int    `json:"port,omitempty"`
	AuthenticationType string `json:"authentication_type,omitempty"`
}

func marshalSnapshot(connectors []b0api.Connector, sockets []b0api.Socket, policies []b0api.Policy, users []b0api.IAMUser, groups []b0api.IAMGroup, accounts []b0api.ServiceAccount, organization *b0api.Organization, upstream map[string][]b0api.UpstreamConfiguration) (string, error) {
	out := safeSnapshot{
		Connectors:   make([]safeConnector, 0, len(connectors)),
		Services:     make([]safeService, 0, len(sockets)),
		Policies:     make([]safePolicy, 0, len(policies)),
		Organization: safeOrganizationFrom(organization),
	}
	for i := range connectors {
		connector := &connectors[i]
		meta := connector.Metadata.ConnectorInternalMetadata
		out.Connectors = append(out.Connectors, safeConnector{
			Name: connector.Name, Connected: connector.IsConnected, ActiveTokens: connector.ActiveTokens,
			ActivePlugins: connector.ActivePlugins, Sockets: connector.Sockets,
			LastSeenAt: timestamp(connector.LastSeenAt), Version: meta.Version, BuiltDate: meta.BuiltDate,
		})
	}
	sort.Slice(out.Connectors, func(i, j int) bool { return out.Connectors[i].Name < out.Connectors[j].Name })
	for i := range sockets {
		service := &sockets[i]
		out.Services = append(out.Services, safeService{
			Name: service.Name, Type: service.SocketType, Alive: service.Alive,
			RecordingEnabled: service.RecordingEnabled, EndToEndEncryptionEnabled: service.EndToEndEncryptionEnabled,
			CloudAuthenticationEnabled:     service.CloudAuthenticationEnabled,
			ConnectorAuthenticationEnabled: service.ConnectorAuthenticationEnabled,
			PrivateSocket:                  service.PrivateSocket, ProtectedSocket: service.ProtectedSocket,
			ConnectorManaged: service.ConnectorManaged, PrivateNetworkEnabled: service.PrivateNetworkEnabled,
		})
		shapes := make([]safeUpstreamShape, 0, len(upstream[service.SocketID]))
		for j := range upstream[service.SocketID] {
			shapes = append(shapes, sanitizeUpstream(upstream[service.SocketID][j]))
		}
		sort.Slice(shapes, func(i, j int) bool { return lessUpstreamShape(shapes[i], shapes[j]) })
		out.UpstreamShapes = append(out.UpstreamShapes, safeServiceUpstreams{ServiceName: service.Name, Configurations: shapes})
	}
	sort.Slice(out.Services, func(i, j int) bool { return out.Services[i].Name < out.Services[j].Name })
	sort.Slice(out.UpstreamShapes, func(i, j int) bool { return out.UpstreamShapes[i].ServiceName < out.UpstreamShapes[j].ServiceName })
	for i := range policies {
		policy := &policies[i]
		out.Policies = append(out.Policies, safePolicy{policy.Name, policy.Version, policy.OrgWide, policy.ReadOnly, policy.Expires})
	}
	sort.Slice(out.Policies, func(i, j int) bool { return out.Policies[i].Name < out.Policies[j].Name })
	out.Identities = identityCounts(users, groups, accounts)
	body, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func lessUpstreamShape(left, right safeUpstreamShape) bool {
	if left.ServiceType != right.ServiceType {
		return left.ServiceType < right.ServiceType
	}
	if left.ServiceSubtype != right.ServiceSubtype {
		return left.ServiceSubtype < right.ServiceSubtype
	}
	if left.Protocol != right.Protocol {
		return left.Protocol < right.Protocol
	}
	if left.Port != right.Port {
		return left.Port < right.Port
	}
	return left.AuthenticationType < right.AuthenticationType
}

func identityCounts(users []b0api.IAMUser, groups []b0api.IAMGroup, accounts []b0api.ServiceAccount) []safeIdentityCount {
	type key struct{ kind, role string }
	counts := map[key]int{}
	for i := range users {
		counts[key{"user", users[i].Role}]++
	}
	for range groups {
		counts[key{kind: "group"}]++
	}
	for i := range accounts {
		counts[key{"service_account", accounts[i].Role}]++
	}
	out := make([]safeIdentityCount, 0, len(counts))
	for bucket, count := range counts {
		out = append(out, safeIdentityCount{bucket.kind, bucket.role, count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Role < out[j].Role
	})
	return out
}

func safeOrganizationFrom(org *b0api.Organization) safeOrganization {
	limits := org.Subscription.SubscriptionLimit
	return safeOrganization{
		MFARequired: org.MFARequired, PrivateNetworkEnabled: org.PrivateNetworkEnabled,
		DNSManagementEnabled: org.DNSManagementEnabled, NeedsReauth: org.NeedsReauth,
		AIAssistantsDisabled:      org.Metadata.AIAssistantsDisabled,
		AISessionAnalysisDisabled: org.Metadata.AISessionAnalysisDisabled,
		SetupWizardCompleted:      org.SetupWizard.Completed, Plan: org.Subscription.Plan.Slug,
		SubscriptionLimits: map[string]int64{
			"socket_count": limits.SocketCount, "socket_tcp_count": limits.SocketTCPCount,
			"user_count": limits.UserCount, "admin_user_count": limits.AdminUserCount,
			"custom_domain_count": limits.CustomDomainCount, "custom_idp_count": limits.CustomIDPCount,
			"notification_count": limits.NotificationCount,
		},
	}
}

// sanitizeUpstream copies only structural, non-secret fields. Authentication
// sub-objects, usernames, passwords, private keys, certificates, hostnames and
// database names are excluded before json.Marshal can observe them.
func sanitizeUpstream(configuration b0api.UpstreamConfiguration) safeUpstreamShape {
	config := configuration.Config
	out := safeUpstreamShape{ServiceType: config.ServiceType}
	if database := config.DatabaseServiceConfiguration; database != nil {
		out.ServiceSubtype = database.DatabaseServiceType
		if standard := database.StandardDatabaseServiceConfiguration; standard != nil {
			out.Protocol = standard.Protocol
			out.Port = standard.Port
			out.AuthenticationType = standard.AuthenticationType
		}
	}
	if ssh := config.SSHServiceConfiguration; ssh != nil && ssh.StandardSSHServiceConfiguration != nil {
		standard := ssh.StandardSSHServiceConfiguration
		out.ServiceSubtype = "standard"
		out.Port = standard.Port
		out.AuthenticationType = standard.SSHAuthenticationType
	}
	return out
}

func timestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (c *Collector) emitSnapshot(e telemetry.Emitter, body string) error {
	if c.snapshotEmitter == nil {
		emitter, err := snapshot.New(snapshot.Config{
			Emitter: e, EventName: EventSnapshot, Kind: snapshot.Kind("pam"),
			Heartbeat: c.snapshotHeartbeat, MaxBodyBytes: c.snapshotBodyBytes,
		})
		if err != nil {
			return err
		}
		c.snapshotEmitter = emitter
	}
	c.snapshotEmitter.Observe(c.now(), "", body, nil)
	return nil
}
