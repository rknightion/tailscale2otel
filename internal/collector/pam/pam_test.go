package pam

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/apistate"
	"github.com/rknightion/tailscale2otel/v5/internal/b0api"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

type fakeAPI struct {
	connectors      []b0api.Connector
	sockets         []b0api.Socket
	policies        []b0api.Policy
	users           []b0api.IAMUser
	groups          []b0api.IAMGroup
	serviceAccounts []b0api.ServiceAccount
	organization    *b0api.Organization
	upstream        map[string][]b0api.UpstreamConfiguration
	errAt           string
	err             error
	upstreamCalls   int
}

func (f *fakeAPI) fail(operation string) error {
	if f.errAt == operation {
		return f.err
	}
	return nil
}

func (f *fakeAPI) Connectors(context.Context) ([]b0api.Connector, error) {
	return f.connectors, f.fail(opConnectors)
}
func (f *fakeAPI) Sockets(context.Context) ([]b0api.Socket, error) {
	return f.sockets, f.fail(opSockets)
}
func (f *fakeAPI) Policies(context.Context) ([]b0api.Policy, error) {
	return f.policies, f.fail(opPolicies)
}
func (f *fakeAPI) IAMUsers(context.Context) ([]b0api.IAMUser, error) {
	return f.users, f.fail(opIAMUsers)
}
func (f *fakeAPI) IAMGroups(context.Context) ([]b0api.IAMGroup, error) {
	return f.groups, f.fail(opIAMGroups)
}
func (f *fakeAPI) IAMServiceAccounts(context.Context) ([]b0api.ServiceAccount, error) {
	return f.serviceAccounts, f.fail(opIAMServiceAccounts)
}
func (f *fakeAPI) Organization(context.Context) (*b0api.Organization, error) {
	return f.organization, f.fail(opOrganization)
}
func (f *fakeAPI) SocketUpstreamConfigurations(_ context.Context, socketID string) ([]b0api.UpstreamConfiguration, error) {
	f.upstreamCalls++
	return f.upstream[socketID], f.fail(opUpstreamConfigurations)
}

func TestNameAndDefaultInterval(t *testing.T) {
	c := New(&fakeAPI{}, 0)
	if got := c.Name(); got != "pam" {
		t.Fatalf("Name() = %q, want pam", got)
	}
	if got := c.DefaultInterval(); got != 10*time.Minute {
		t.Fatalf("DefaultInterval() = %v, want 10m", got)
	}
	if got := New(&fakeAPI{}, time.Minute).DefaultInterval(); got != time.Minute {
		t.Fatalf("configured DefaultInterval() = %v, want 1m", got)
	}
}

func TestCollectRejectsMissingOrganization(t *testing.T) {
	rec := telemetrytest.New()
	if err := New(&fakeAPI{}, 0).Collect(context.Background(), rec.Emitter()); err == nil {
		t.Fatal("Collect returned nil when the API returned no organization")
	}
}

func TestCollectFixtureInventoryAndConfigurationShape(t *testing.T) {
	api := fixtureAPI(t)
	now := time.Date(2026, 9, 4, 19, 24, 43, 0, time.UTC)
	rec := telemetrytest.New()
	if err := New(api, 0, WithClock(func() time.Time { return now })).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	assertSingleValue(t, rec, metricConnectors, 2)
	if got := len(rec.MetricPoints(metricConnectorConnected)); got != 2 {
		t.Fatalf("connector.connected points = %d, want 2", got)
	}
	for _, point := range rec.MetricPoints(metricConnectorLastSeenAge) {
		if point.Value < 0 || point.Value > 70 {
			t.Errorf("connector.last_seen_age = %v, want recent non-negative age", point.Value)
		}
	}
	if got := len(rec.MetricPoints(metricConnectorInfo)); got != 2 {
		t.Fatalf("connector.info points = %d, want 2", got)
	}

	serviceCounts := metricBuckets(rec.MetricPoints(metricServices), attrServiceType)
	if serviceCounts["database"] != 1 || serviceCounts["ssh"] != 1 || len(serviceCounts) != 2 {
		t.Fatalf("service counts = %v, want database=1 ssh=1", serviceCounts)
	}
	if got := len(rec.MetricPoints(metricServiceAlive)); got != 2 {
		t.Fatalf("service.alive points = %d, want 2", got)
	}
	if got := len(rec.MetricPoints(metricServiceSettingEnabled)); got != 16 {
		t.Fatalf("service setting points = %d, want 16", got)
	}

	assertSingleValue(t, rec, metricPolicies, 1)
	if got := len(rec.MetricPoints(metricPolicySettingEnabled)); got != 3 {
		t.Fatalf("policy setting points = %d, want 3", got)
	}

	identity := map[string]float64{}
	for _, point := range rec.MetricPoints(metricIdentities) {
		identity[point.Attrs[attrIdentityKind]+"/"+point.Attrs[attrIdentityRole]] = point.Value
	}
	wantIdentity := map[string]float64{
		"group/": 3, "user/admin": 1, "service_account/admin": 1,
		"service_account/read only": 1, "service_account/client": 23,
	}
	if len(identity) != len(wantIdentity) {
		t.Fatalf("identity buckets = %v, want %v", identity, wantIdentity)
	}
	for bucket, want := range wantIdentity {
		if got := identity[bucket]; got != want {
			t.Errorf("identity %s = %v, want %v", bucket, got, want)
		}
	}

	if got := len(rec.MetricPoints(metricOrgSettingEnabled)); got != 7 {
		t.Fatalf("org setting points = %d, want 7", got)
	}
	plan := rec.MetricPoints(metricOrgPlanInfo)
	if len(plan) != 1 || plan[0].Value != 1 || plan[0].Attrs[attrPlan] != "ts-free" {
		t.Fatalf("org plan points = %+v, want ts-free info gauge", plan)
	}
	if got := len(rec.MetricPoints(metricSubscriptionLimit)); got != 7 {
		t.Fatalf("subscription limit points = %d, want 7", got)
	}

	for _, forbidden := range []string{"tailscale.service.ports", "tailscale.config.audit.changes"} {
		if points := rec.MetricPoints(forbidden); len(points) != 0 {
			t.Fatalf("collector duplicated %s with %d points", forbidden, len(points))
		}
	}
	if api.upstreamCalls != 0 {
		t.Fatalf("upstream configuration calls = %d, want 0 while snapshots are disabled", api.upstreamCalls)
	}
}

func TestForbiddenIsScopeDenied(t *testing.T) {
	api := &fakeAPI{errAt: opConnectors, err: &b0api.StatusError{Code: 403}}
	rec := telemetrytest.New()
	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err == nil {
		t.Fatal("Collect returned nil for 403")
	}
	states := map[string]float64{}
	for _, point := range rec.MetricPoints(apistate.MetricAvailability) {
		if point.Attrs["tailscale.api.operation"] == opConnectors {
			states[point.Attrs["tailscale.api.state"]] = point.Value
		}
	}
	if states[string(apistate.StateScopeDenied)] != 1 {
		t.Fatalf("availability states = %v, want scope_denied=1", states)
	}
	if states[string(apistate.StateDisabled)] != 0 {
		t.Fatalf("availability states = %v, 403 must not be disabled", states)
	}
}

func assertSingleValue(t *testing.T, rec *telemetrytest.Recorder, name string, want float64) {
	t.Helper()
	points := rec.MetricPoints(name)
	if len(points) != 1 || points[0].Value != want {
		t.Fatalf("%s points = %+v, want one value %v", name, points, want)
	}
}

func metricBuckets(points []telemetrytest.MetricPoint, attr string) map[string]float64 {
	out := make(map[string]float64, len(points))
	for _, point := range points {
		out[point.Attrs[attr]] = point.Value
	}
	return out
}

func fixtureAPI(t *testing.T) *fakeAPI {
	t.Helper()
	api := &fakeAPI{upstream: map[string][]b0api.UpstreamConfiguration{}}
	readFixture(t, "pam_connectors.json", &struct {
		List *[]b0api.Connector `json:"list"`
	}{List: &api.connectors})
	readFixture(t, "pam_sockets.json", &struct {
		List *[]b0api.Socket `json:"list"`
	}{List: &api.sockets})
	readFixture(t, "pam_policies.json", &api.policies)
	readFixture(t, "pam_iam_users.json", &struct {
		List *[]b0api.IAMUser `json:"list"`
	}{List: &api.users})
	readFixture(t, "pam_iam_groups.json", &struct {
		List *[]b0api.IAMGroup `json:"list"`
	}{List: &api.groups})
	readFixture(t, "pam_iam_service_accounts.json", &struct {
		List *[]b0api.ServiceAccount `json:"list"`
	}{List: &api.serviceAccounts})
	readFixture(t, "pam_organization.json", &api.organization)
	var upstream struct {
		List []b0api.UpstreamConfiguration `json:"list"`
	}
	readFixture(t, "pam_socket_upstream_config.json", &upstream)
	for i := range api.sockets {
		api.upstream[api.sockets[i].SocketID] = upstream.List
	}
	return api
}

func readFixture(t *testing.T, name string, out any) {
	t.Helper()
	path := filepath.Join("..", "..", "b0api", "testdata", name)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read tracked fixture %s: %v", name, err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("decode tracked fixture %s: %v", name, err)
	}
}
