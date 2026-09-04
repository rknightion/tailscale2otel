package pam_test

import (
	"context"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/apistate"
	"github.com/rknightion/tailscale2otel/v5/internal/b0api"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/pam"
	"github.com/rknightion/tailscale2otel/v5/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

type catalogAPI struct{}

func (catalogAPI) Connectors(context.Context) ([]b0api.Connector, error) {
	return []b0api.Connector{{Name: "connector", IsConnected: true}}, nil
}
func (catalogAPI) Sockets(context.Context) ([]b0api.Socket, error) {
	return []b0api.Socket{{SocketID: "socket", Name: "service", SocketType: "ssh", Alive: true}}, nil
}
func (catalogAPI) Policies(context.Context) ([]b0api.Policy, error) {
	return []b0api.Policy{{Name: "policy", Version: "v2"}}, nil
}
func (catalogAPI) IAMUsers(context.Context) ([]b0api.IAMUser, error) {
	return []b0api.IAMUser{{Role: "admin"}}, nil
}
func (catalogAPI) IAMGroups(context.Context) ([]b0api.IAMGroup, error) {
	return []b0api.IAMGroup{{}}, nil
}
func (catalogAPI) IAMServiceAccounts(context.Context) ([]b0api.ServiceAccount, error) {
	return []b0api.ServiceAccount{{Role: "client"}}, nil
}
func (catalogAPI) Organization(context.Context) (*b0api.Organization, error) {
	return &b0api.Organization{}, nil
}
func (catalogAPI) SocketUpstreamConfigurations(context.Context, string) ([]b0api.UpstreamConfiguration, error) {
	return nil, nil
}

func TestCatalogMatchesEmitted(t *testing.T) {
	rec := telemetrytest.New()
	collector := pam.New(catalogAPI{}, 0, pam.WithAPIState(apistate.NewTracker()), pam.WithSnapshot(true))
	if err := collector.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	declared := map[string]metricdoc.Metric{}
	allMetrics := append(pam.Catalog(), apistate.Catalog()...)
	for _, metric := range allMetrics {
		declared[metric.Name] = metric
	}
	for _, name := range rec.MetricNames() {
		points := rec.MetricPoints(name)
		if len(points) == 0 {
			continue
		}
		declaration, ok := declared[name]
		if !ok {
			t.Errorf("emitted metric %q is not declared", name)
			continue
		}
		if points[0].Unit != declaration.Unit || points[0].Description != declaration.Description {
			t.Errorf("%s emission differs from catalog: point=%+v catalog=%+v", name, points[0], declaration)
		}
	}
	telemetrytest.AssertCatalogAttrs(t, rec, allMetrics, pam.LogCatalog())

	declaredLogs := map[string]bool{}
	for _, event := range pam.LogCatalog() {
		declaredLogs[event.Name] = true
	}
	for _, record := range rec.LogRecords() {
		if record.EventName != "" && !declaredLogs[record.EventName] {
			t.Errorf("emitted log %q is not declared", record.EventName)
		}
	}
}
