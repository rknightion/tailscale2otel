package services

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/enrich"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

type fakeAPI struct {
	svcs    []tsapi.VIPService
	svcErr  error
	hosts   map[string][]tsapi.ServiceHost
	hostErr map[string]error

	addrs    []tsapi.ServiceAddr
	addrsErr error
}

type cancelAfterFirstHostAPI struct {
	services          []tsapi.VIPService
	cancel            context.CancelFunc
	dispatchObserved  <-chan struct{}
	dispatchConfirmed atomic.Bool
}

type dispatchObservationContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (c *dispatchObservationContext) Err() error {
	err := c.Context.Err()
	if err != nil {
		c.once.Do(func() { close(c.observed) })
	}
	return err
}

func (a *cancelAfterFirstHostAPI) Services(context.Context) ([]tsapi.VIPService, error) {
	return a.services, nil
}

func (a *cancelAfterFirstHostAPI) ServiceHosts(_ context.Context, name string) ([]tsapi.ServiceHost, error) {
	if name == a.services[0].Name {
		a.cancel()
		// Keep the worker in this call until the dispatcher has observed the
		// canceled context, so the next unbuffered job cannot be sent. The
		// timeout turns a regression in that synchronization into a test failure
		// instead of hanging the suite.
		select {
		case <-a.dispatchObserved:
			a.dispatchConfirmed.Store(true)
		case <-time.After(time.Second):
		}
	}
	return []tsapi.ServiceHost{{NodeID: name, ApprovalLevel: "approved:auto", Configured: "ready"}}, nil
}

func (a *cancelAfterFirstHostAPI) ServiceAddrs(context.Context) ([]tsapi.ServiceAddr, error) {
	return nil, nil
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

func pointByAttr(pts []telemetrytest.MetricPoint, want map[string]string) (telemetrytest.MetricPoint, bool) {
	for _, p := range pts {
		match := true
		for k, v := range want {
			if p.Attrs[k] != v {
				match = false
				break
			}
		}
		if match {
			return p, true
		}
	}
	return telemetrytest.MetricPoint{}, false
}

func sampleServices() []tsapi.VIPService {
	return []tsapi.VIPService{
		{Name: "svc:argocd", DisplayName: "Argo CD", Ports: []string{"tcp:443"}, Tags: []string{"tag:k8s"}},
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
	for _, p := range rec.MetricPoints("tailscale.service.ports") {
		if p.Attrs["tailscale.service.name"] == "svc:argocd" &&
			p.Attrs["tailscale.service.display_name"] != "Argo CD" {
			t.Errorf("argocd display name = %q, want Argo CD", p.Attrs["tailscale.service.display_name"])
		}
		if p.Attrs["tailscale.service.name"] == "svc:grpc" {
			if _, ok := p.Attrs["tailscale.service.display_name"]; ok {
				t.Errorf("svc:grpc unexpectedly has an empty display name: attrs=%v", p.Attrs)
			}
		}
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
		svcs: []tsapi.VIPService{{Name: "svc:argocd", DisplayName: "Argo CD", Ports: []string{"tcp:443"}}},
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

func TestCollectHostInfoClearsRemovedSeries(t *testing.T) {
	api := &fakeAPI{
		svcs: []tsapi.VIPService{{Name: "svc:alpha"}},
		hosts: map[string][]tsapi.ServiceHost{
			"svc:alpha": {{NodeID: "node-1", ApprovalLevel: "approved:auto", Configured: "ready"}},
		},
	}
	rec := telemetrytest.New()
	c := New(api, 0, WithCollectHosts(true))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("first Collect: %v", err)
	}
	if got := len(rec.MetricPoints(metricHostInfo)); got != 1 {
		t.Fatalf("first host-info points = %d, want 1", got)
	}

	api.hosts["svc:alpha"] = nil
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("second Collect: %v", err)
	}
	if got := len(rec.MetricPoints(metricHostInfo)); got != 0 {
		t.Fatalf("host-info points after removal = %d, want 0", got)
	}
}

// TestCollectSkipsHostInfoSnapshotAfterCanceledDispatch prevents a partial
// host inventory from being published as a complete GaugeSnapshot. The first
// host request succeeds but cancels the collection before the second request
// can be dispatched; only a fully dispatched set may replace host.info.
func TestCollectSkipsHostInfoSnapshotAfterCanceledDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dispatchObserved := make(chan struct{})
	probeCtx := &dispatchObservationContext{Context: ctx, observed: dispatchObserved}
	api := &cancelAfterFirstHostAPI{
		services:         []tsapi.VIPService{{Name: "svc:first"}, {Name: "svc:second"}},
		cancel:           cancel,
		dispatchObserved: dispatchObserved,
	}
	rec := telemetrytest.New()
	c := New(api, 0, WithCollectHosts(true))
	if err := c.Collect(probeCtx, rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !api.dispatchConfirmed.Load() {
		t.Fatal("test did not observe the dispatcher cancel before the first host request returned")
	}
	if got := rec.MetricPoints(metricHostInfo); len(got) != 0 {
		t.Fatalf("partial host.info snapshot = %+v, want no snapshot", got)
	}
}

func TestCollectServiceTagRollupCap(t *testing.T) {
	api := &fakeAPI{svcs: []tsapi.VIPService{
		{Name: "svc:alpha", Tags: []string{"tag:kept"}},
		{Name: "svc:beta", Tags: []string{"tag:kept", "tag:folded"}},
	}}
	rec := telemetrytest.New()
	c := New(api, 0, WithTagRollup(true, 1))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints("tailscale.services.by_tag")
	if len(pts) != 2 {
		t.Fatalf("services.by_tag points = %d, want 2 (kept + __other__)", len(pts))
	}
	if p, ok := pointByAttr(pts, map[string]string{"tailscale.tag": "tag:kept"}); !ok || p.Value != 2 {
		t.Errorf("services.by_tag tag:kept = %+v, want value 2", p)
	}
	if p, ok := pointByAttr(pts, map[string]string{"tailscale.tag": "__other__"}); !ok || p.Value != 1 {
		t.Errorf("services.by_tag __other__ = %+v, want value 1", p)
	}
}

func TestCollectServiceTagRollupDisabled(t *testing.T) {
	rec := telemetrytest.New()
	c := New(&fakeAPI{svcs: sampleServices()}, 0, WithTagRollup(false, 1))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if pts := rec.MetricPoints("tailscale.services.by_tag"); len(pts) != 0 {
		t.Fatalf("services.by_tag points = %d, want 0 when rollup disabled", len(pts))
	}
}

func TestCollectServiceHostJoinsDeviceByNodeID(t *testing.T) {
	cache := enrich.NewDeviceCache()
	cache.Replace([]enrich.DeviceMeta{{
		ID:        "device-1",
		NodeID:    "node-1",
		Hostname:  "service-host",
		OS:        "linux",
		OSVersion: "1.2.3",
		User:      "user@example.invalid",
		Tags:      []string{"tag:server", "tag:prod"},
	}})
	api := &fakeAPI{
		svcs: []tsapi.VIPService{{Name: "svc:alpha", DisplayName: "Alpha service"}},
		hosts: map[string][]tsapi.ServiceHost{
			"svc:alpha": {{NodeID: "node-1", ApprovalLevel: "approved:auto", Configured: "ready"}},
		},
	}
	rec := telemetrytest.New()
	c := New(api, 0, WithEnrichCache(cache), WithCollectHosts(true))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints("tailscale.service.host.info")
	if len(pts) != 1 {
		t.Fatalf("service.host.info points = %d, want 1", len(pts))
	}
	p := pts[0]
	want := map[string]string{
		"tailscale.service.name":         "svc:alpha",
		"tailscale.service.display_name": "Alpha service",
		"tailscale.node.id":              "node-1",
		"tailscale.service.approval":     "approved:auto",
		"tailscale.service.configured":   "ready",
		"host.name":                      "service-host",
		"host.id":                        "device-1",
		"os.type":                        "linux",
		"os.version":                     "1.2.3",
		"tailscale.user":                 "user@example.invalid",
		"tailscale.tags":                 "tag:server,tag:prod",
	}
	for k, v := range want {
		if p.Attrs[k] != v {
			t.Errorf("service.host.info %s = %q, want %q (attrs=%v)", k, p.Attrs[k], v, p.Attrs)
		}
	}
}

func TestCollectServiceHostWithoutDeviceStillEmitsNodeID(t *testing.T) {
	api := &fakeAPI{
		svcs: []tsapi.VIPService{{Name: "svc:alpha"}},
		hosts: map[string][]tsapi.ServiceHost{
			"svc:alpha": {{NodeID: "node-missing", ApprovalLevel: "pending", Configured: "pending"}},
		},
	}
	rec := telemetrytest.New()
	if err := New(api, 0, WithCollectHosts(true)).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	pts := rec.MetricPoints("tailscale.service.host.info")
	if len(pts) != 1 {
		t.Fatalf("service.host.info points = %d, want 1", len(pts))
	}
	if got := pts[0].Attrs["tailscale.node.id"]; got != "node-missing" {
		t.Errorf("missing-device node id = %q, want node-missing", got)
	}
	if _, ok := pts[0].Attrs["host.name"]; ok {
		t.Errorf("missing-device host unexpectedly joined: attrs=%v", pts[0].Attrs)
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

// availabilityStates returns, per operation, the single state whose gauge is 1.
// Fails the test outright if any operation has two states pinned at 1 — the
// signal that a duplicate Observe call clobbered the tracker entry.
func availabilityStates(t *testing.T, rec *telemetrytest.Recorder) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, p := range rec.MetricPoints(apistate.MetricAvailability) {
		op := p.Attrs["tailscale.api.operation"]
		st := p.Attrs["tailscale.api.state"]
		switch p.Value {
		case 1:
			if prev, dup := out[op]; dup {
				t.Fatalf("operation %q has two states at 1: %q and %q", op, prev, st)
			}
			out[op] = st
		case 0:
		default:
			t.Fatalf("availability gauge for %q/%q = %v, want 0 or 1", op, st, p.Value)
		}
	}
	return out
}

// TestAvailabilityListServicesStates is the #524 acceptance case for the
// listServices operation: it must classify (not blanket-propagate) the
// Services() error, exactly like webhooks/postureintegrations do for their own
// single-call operations.
func TestAvailabilityListServicesStates(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantErr   bool
		wantState apistate.State
	}{
		{"success", nil, false, apistate.StateSupported},
		{"401 credential rejected", &tsapi.StatusError{Code: 401}, true, apistate.StateCredentialRejected},
		{"403 scope denied", &tsapi.StatusError{Code: 403}, true, apistate.StateScopeDenied},
		{"400 request rejected", &tsapi.StatusError{Code: 400}, true, apistate.StateRequestRejected},
		{"transport error", context.DeadlineExceeded, true, apistate.StateTransientFailure},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := telemetrytest.New()
			api := &fakeAPI{svcs: sampleServices(), svcErr: tc.err}
			c := New(api, 0, WithAPIState(apistate.NewTracker()))
			err := c.Collect(context.Background(), rec.Emitter())
			if tc.wantErr != (err != nil) {
				t.Fatalf("Collect() err = %v, wantErr = %v", err, tc.wantErr)
			}
			if got := availabilityStates(t, rec)[opListServices]; got != string(tc.wantState) {
				t.Errorf("listServices availability = %q, want %q", got, tc.wantState)
			}
		})
	}
}

// TestAvailabilitySuccessfulTickBothOperationsSupported covers a fully clean
// tick with collect_hosts on and N>0 services: both listServices and
// listServiceHosts must read supported.
func TestAvailabilitySuccessfulTickBothOperationsSupported(t *testing.T) {
	api := &fakeAPI{
		svcs: sampleServices(),
		hosts: map[string][]tsapi.ServiceHost{
			"svc:argocd": {{NodeID: "n1", ApprovalLevel: "approved:auto", Configured: "ready"}},
			"svc:grpc":   {{NodeID: "n2", ApprovalLevel: "approved:auto", Configured: "ready"}},
		},
	}
	rec := telemetrytest.New()
	c := New(api, 0, WithCollectHosts(true), WithAPIState(apistate.NewTracker()))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	states := availabilityStates(t, rec)
	if states[opListServices] != string(apistate.StateSupported) {
		t.Errorf("listServices availability = %q, want supported", states[opListServices])
	}
	if states[opListServiceHosts] != string(apistate.StateSupported) {
		t.Errorf("listServiceHosts availability = %q, want supported", states[opListServiceHosts])
	}
}

// TestAvailabilityListServicesObservedOnceDespiteTwoCallers is the #524 trap
// regression: Services() and ServiceAddrs() both hit GET .../services, and
// with the enrich cache wired both run every tick. listServices must still be
// observed exactly once, not twice.
func TestAvailabilityListServicesObservedOnceDespiteTwoCallers(t *testing.T) {
	cache := enrich.NewDeviceCache()
	api := &fakeAPI{svcs: sampleServices(), addrs: sampleServiceAddrs()}
	rec := telemetrytest.New()
	c := New(api, 0, WithEnrichCache(cache), WithAPIState(apistate.NewTracker()))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	states := availabilityStates(t, rec) // fails outright on a duplicate entry
	if states[opListServices] != string(apistate.StateSupported) {
		t.Errorf("listServices availability = %q, want supported", states[opListServices])
	}
	var count int
	for _, p := range rec.MetricPoints(apistate.MetricAvailability) {
		if p.Attrs["tailscale.api.operation"] == opListServices {
			count++
		}
	}
	if want := len(apistate.States()); count != want {
		t.Errorf("listServices availability points = %d, want %d (exactly one Observe call this tick)", count, want)
	}
}

// TestAvailabilityHostsFailingWhileServicesSucceed covers the mixed-outcome
// tick: listServices clean, listServiceHosts denied — both must be correct in
// the same tick, independently.
func TestAvailabilityHostsFailingWhileServicesSucceed(t *testing.T) {
	api := &fakeAPI{
		svcs: sampleServices(),
		hostErr: map[string]error{
			"svc:argocd": &tsapi.StatusError{Code: 403},
			"svc:grpc":   &tsapi.StatusError{Code: 403},
		},
	}
	rec := telemetrytest.New()
	c := New(api, 0, WithCollectHosts(true), WithAPIState(apistate.NewTracker()))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect should not fail on a per-service host error: %v", err)
	}
	states := availabilityStates(t, rec)
	if states[opListServices] != string(apistate.StateSupported) {
		t.Errorf("listServices availability = %q, want supported", states[opListServices])
	}
	if states[opListServiceHosts] != string(apistate.StateScopeDenied) {
		t.Errorf("listServiceHosts availability = %q, want scope_denied", states[opListServiceHosts])
	}
}

// TestAvailabilityNoHostAttemptsEmitsNoEntry is the #524 zero-attempts case:
// listServiceHosts must stay unknown (no availability entry at all) rather
// than falsely claiming a state, both when collect_hosts is off and when there
// are no services to iterate.
func TestAvailabilityNoHostAttemptsEmitsNoEntry(t *testing.T) {
	assertNoHostsEntry := func(t *testing.T, rec *telemetrytest.Recorder) {
		t.Helper()
		for _, p := range rec.MetricPoints(apistate.MetricAvailability) {
			if p.Attrs["tailscale.api.operation"] == opListServiceHosts {
				t.Fatalf("unexpected listServiceHosts availability point: %+v", p)
			}
		}
	}

	t.Run("collect_hosts off", func(t *testing.T) {
		rec := telemetrytest.New()
		c := New(&fakeAPI{svcs: sampleServices()}, 0, WithAPIState(apistate.NewTracker()))
		if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
			t.Fatalf("Collect: %v", err)
		}
		assertNoHostsEntry(t, rec)
	})

	t.Run("no services", func(t *testing.T) {
		rec := telemetrytest.New()
		c := New(&fakeAPI{svcs: nil}, 0, WithCollectHosts(true), WithAPIState(apistate.NewTracker()))
		if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
			t.Fatalf("Collect: %v", err)
		}
		assertNoHostsEntry(t, rec)
	})
}

// TestAvailabilityTrackerAndClock mirrors postureintegrations'
// TestAvailabilitySupportedAndTracker: the tracker records one entry and the
// last-probe gauge uses the injected clock.
func TestAvailabilityTrackerAndClock(t *testing.T) {
	probe := time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC)
	tr := apistate.NewTracker()
	rec := telemetrytest.New()
	c := New(&fakeAPI{svcs: sampleServices()}, 0, WithAPIState(tr), WithClock(func() time.Time { return probe }))

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	// collect_hosts is off, so only the listServices entry is recorded.
	snap := tr.Snapshot()
	if len(snap) != 1 || snap[0].Collector != "services" || snap[0].Operation != opListServices {
		t.Fatalf("tracker snapshot = %+v, want one services/listServices entry", snap)
	}
	lp := rec.MetricPoints(apistate.MetricLastProbe)
	if len(lp) != 1 || lp[0].Value != float64(probe.Unix()) {
		t.Fatalf("last_probe = %+v, want one point at %v", lp, probe.Unix())
	}
}
