package services

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

type fakeAPI struct {
	svcs    []tsapi.VIPService
	svcErr  error
	hosts   map[string][]tsapi.ServiceHost
	hostErr map[string]error
}

func (f *fakeAPI) Services(context.Context) ([]tsapi.VIPService, error) {
	return f.svcs, f.svcErr
}

func (f *fakeAPI) ServiceHosts(_ context.Context, name string) ([]tsapi.ServiceHost, error) {
	if e := f.hostErr[name]; e != nil {
		return nil, e
	}
	return f.hosts[name], nil
}

var _ collector.SnapshotCollector = (*Collector)(nil)

func sampleServices() []tsapi.VIPService {
	return []tsapi.VIPService{
		{Name: "svc:argocd", Ports: []string{"tcp:443"}, Tags: []string{"tag:k8s"}},
		{Name: "svc:grpc", Ports: []string{"tcp:443", "tcp:80"}, Tags: []string{"tag:k8s"}},
	}
}

func TestNameAndDefaultInterval(t *testing.T) {
	c := New(&fakeAPI{}, 0)
	if c.Name() != "services" {
		t.Fatalf("Name() = %q, want services", c.Name())
	}
	if got := c.DefaultInterval(); got != 600*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 600s", got)
	}
}

func TestCollectEmitsCountAndPorts(t *testing.T) {
	rec := telemetrytest.New()
	if err := New(&fakeAPI{svcs: sampleServices()}, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if cnt := rec.MetricPoints("tailscale.services.count"); len(cnt) != 1 || cnt[0].Value != 2 {
		t.Fatalf("services.count = %+v, want one point value 2", cnt)
	}
	ports := map[string]float64{}
	for _, p := range rec.MetricPoints("tailscale.service.ports") {
		ports[p.Attrs["tailscale.service.name"]] = p.Value
	}
	if ports["svc:argocd"] != 1 || ports["svc:grpc"] != 2 {
		t.Fatalf("ports = %v, want argocd 1 / grpc 2", ports)
	}
	// collect_hosts is off by default → no hosts series.
	if h := rec.MetricPoints("tailscale.service.hosts"); len(h) != 0 {
		t.Fatalf("hosts points = %d, want 0 (collect_hosts off)", len(h))
	}
}

func TestPerEntityOffDropsPorts(t *testing.T) {
	rec := telemetrytest.New()
	c := New(&fakeAPI{svcs: sampleServices()}, 0, WithPerEntity(false))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if cnt := rec.MetricPoints("tailscale.services.count"); len(cnt) != 1 || cnt[0].Value != 2 {
		t.Fatalf("count = %+v, want 2", cnt)
	}
	if p := rec.MetricPoints("tailscale.service.ports"); len(p) != 0 {
		t.Fatalf("ports points = %d, want 0 when per_entity off", len(p))
	}
}

func TestCollectHostsBuckets(t *testing.T) {
	api := &fakeAPI{
		svcs: []tsapi.VIPService{{Name: "svc:argocd", Ports: []string{"tcp:443"}}},
		hosts: map[string][]tsapi.ServiceHost{
			"svc:argocd": {
				{NodeID: "n1", ApprovalLevel: "approved:auto", Configured: "ready"},
				{NodeID: "n2", ApprovalLevel: "approved:auto", Configured: "ready"},
				{NodeID: "n3", ApprovalLevel: "pending", Configured: "pending"},
			},
		},
	}
	rec := telemetrytest.New()
	c := New(api, 0, WithCollectHosts(true))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	type key struct{ approval, configured string }
	got := map[key]float64{}
	for _, p := range rec.MetricPoints("tailscale.service.hosts") {
		got[key{p.Attrs["tailscale.service.approval"], p.Attrs["tailscale.service.configured"]}] = p.Value
	}
	if got[key{"approved:auto", "ready"}] != 2 {
		t.Errorf("approved:auto/ready = %v, want 2", got[key{"approved:auto", "ready"}])
	}
	if got[key{"pending", "pending"}] != 1 {
		t.Errorf("pending/pending = %v, want 1", got[key{"pending", "pending"}])
	}
}

func TestServiceHostErrorNonFatal(t *testing.T) {
	api := &fakeAPI{
		svcs:    sampleServices(),
		hostErr: map[string]error{"svc:argocd": context.DeadlineExceeded},
		hosts:   map[string][]tsapi.ServiceHost{"svc:grpc": {{NodeID: "n1", ApprovalLevel: "approved:auto", Configured: "ready"}}},
	}
	rec := telemetrytest.New()
	c := New(api, 0, WithCollectHosts(true))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect should not fail on a per-service host error: %v", err)
	}
	// argocd hosts skipped; grpc hosts present; count+ports still emitted.
	if cnt := rec.MetricPoints("tailscale.services.count"); len(cnt) != 1 || cnt[0].Value != 2 {
		t.Fatalf("count = %+v, want 2", cnt)
	}
	var grpc bool
	for _, p := range rec.MetricPoints("tailscale.service.hosts") {
		if p.Attrs["tailscale.service.name"] == "svc:argocd" {
			t.Errorf("argocd hosts should be skipped on host error")
		}
		if p.Attrs["tailscale.service.name"] == "svc:grpc" {
			grpc = true
		}
	}
	if !grpc {
		t.Errorf("svc:grpc hosts missing")
	}
}

func TestCollectPropagatesServicesError(t *testing.T) {
	rec := telemetrytest.New()
	if err := New(&fakeAPI{svcErr: context.DeadlineExceeded}, 0).Collect(context.Background(), rec.Emitter()); err == nil {
		t.Fatal("expected error when Services() fails")
	}
}

func TestEmptyServices(t *testing.T) {
	rec := telemetrytest.New()
	if err := New(&fakeAPI{svcs: nil}, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if cnt := rec.MetricPoints("tailscale.services.count"); len(cnt) != 1 || cnt[0].Value != 0 {
		t.Fatalf("count = %+v, want 0", cnt)
	}
}
