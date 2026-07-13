package services

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/collector"
	"github.com/rknightion/tailscale2otel/v2/internal/enrich"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v2/internal/tsapi"
)

type fakeAPI struct {
	svcs    []tsapi.VIPService
	svcErr  error
	hosts   map[string][]tsapi.ServiceHost
	hostErr map[string]error

	addrs    []tsapi.ServiceAddr
	addrsErr error
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

func (f *fakeAPI) ServiceAddrs(context.Context) ([]tsapi.ServiceAddr, error) {
	return f.addrs, f.addrsErr
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

// sampleServiceAddrs mirrors the real /services response shape (name + both
// IPv4/IPv6 VIP addrs), anonymized from .capture/services-live-20260713.json.
func sampleServiceAddrs() []tsapi.ServiceAddr {
	return []tsapi.ServiceAddr{
		{Name: "svc:argocd", Addrs: []string{"100.124.43.64", "fd7a:115c:a1e0::7501:2b54"}},
		{Name: "svc:grpc", Addrs: []string{"100.69.161.118", "fd7a:115c:a1e0::c501:a17f"}},
	}
}

func TestWithEnrichCache_PopulatesServiceVIPMap(t *testing.T) {
	cache := enrich.NewDeviceCache()
	api := &fakeAPI{svcs: sampleServices(), addrs: sampleServiceAddrs()}
	c := New(api, 0, WithEnrichCache(cache))

	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if got := cache.ResolveName("100.124.43.64:443"); got != "svc:argocd" {
		t.Errorf("ResolveName(argocd VIPv4) = %q, want svc:argocd", got)
	}
	if got := cache.ResolveName("[fd7a:115c:a1e0::7501:2b54]:443"); got != "svc:argocd" {
		t.Errorf("ResolveName(argocd VIPv6) = %q, want svc:argocd", got)
	}
	if got := cache.ResolveName("100.69.161.118:443"); got != "svc:grpc" {
		t.Errorf("ResolveName(grpc VIPv4) = %q, want svc:grpc", got)
	}
}

func TestWithoutEnrichCache_DoesNotFetchAddrs(t *testing.T) {
	// No WithEnrichCache option -> ServiceAddrs must not even be called. A
	// forced error confirms the collector never reaches it.
	api := &fakeAPI{svcs: sampleServices(), addrsErr: fmt.Errorf("must not be called")}
	rec := telemetrytest.New()
	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect should not fail: %v", err)
	}
}

func TestWithEnrichCache_AddrsFetchErrorIsNonFatal(t *testing.T) {
	cache := enrich.NewDeviceCache()
	// Seed the cache with a stale entry so we can confirm a failed refresh
	// leaves it in place rather than clearing it.
	cache.ReplaceServices(map[netip.Addr]string{
		netip.MustParseAddr("100.124.43.64"): "svc:argocd",
	})
	api := &fakeAPI{svcs: sampleServices(), addrsErr: fmt.Errorf("transient")}
	c := New(api, 0, WithEnrichCache(cache))

	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect should not fail when ServiceAddrs errors: %v", err)
	}
	// Inventory metrics are unaffected by the addr-fetch failure.
	if cnt := rec.MetricPoints("tailscale.services.count"); len(cnt) != 1 || cnt[0].Value != 2 {
		t.Fatalf("count = %+v, want 2", cnt)
	}
	if got := cache.ResolveName("100.124.43.64:443"); got != "svc:argocd" {
		t.Errorf("stale cache entry lost on fetch error: got %q, want svc:argocd", got)
	}
}

// TestGuard_RawServiceAddressesNeverEmitted is the #166 acceptance criterion:
// even with an addr-bearing fake wired through WithEnrichCache, no emitted
// metric or log attribute value may contain a raw service address. The
// service-VIP addresses may only ever be used as in-memory cache keys.
func TestGuard_RawServiceAddressesNeverEmitted(t *testing.T) {
	cache := enrich.NewDeviceCache()
	addrs := sampleServiceAddrs()
	api := &fakeAPI{
		svcs: sampleServices(),
		hosts: map[string][]tsapi.ServiceHost{
			"svc:argocd": {{NodeID: "n1", ApprovalLevel: "approved:auto", Configured: "ready"}},
		},
		addrs: addrs,
	}
	c := New(api, 0, WithEnrichCache(cache), WithCollectHosts(true))

	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	var rawAddrs []string
	for _, s := range addrs {
		rawAddrs = append(rawAddrs, s.Addrs...)
	}

	checkAttrs := func(attrs map[string]string) {
		for k, v := range attrs {
			for _, raw := range rawAddrs {
				if strings.Contains(v, raw) {
					t.Errorf("attribute %q = %q contains raw service address %q", k, v, raw)
				}
			}
		}
	}
	for _, name := range []string{"tailscale.services.count", "tailscale.service.ports", "tailscale.service.hosts"} {
		for _, p := range rec.MetricPoints(name) {
			checkAttrs(p.Attrs)
		}
	}
}
