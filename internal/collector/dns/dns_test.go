package dns

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/collector"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v2/internal/tsapi"
)

// fakeAPI implements the narrow dns api interface for tests.
type fakeAPI struct {
	cfg *tsapi.DNSConfig
	err error
}

func (f *fakeAPI) DNSConfiguration(_ context.Context) (*tsapi.DNSConfig, error) {
	return f.cfg, f.err
}

// SnapshotCollector compile-time check.
var _ collector.SnapshotCollector = (*Collector)(nil)

func TestNameAndDefaultInterval(t *testing.T) {
	c := New(&fakeAPI{cfg: &tsapi.DNSConfig{}}, 0)
	if c.Name() != "dns" {
		t.Fatalf("Name() = %q, want dns", c.Name())
	}
	if got := c.DefaultInterval(); got != 600*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 600s", got)
	}
	c2 := New(&fakeAPI{cfg: &tsapi.DNSConfig{}}, 120*time.Second)
	if got := c2.DefaultInterval(); got != 120*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 120s", got)
	}
}

// gaugeValue returns the single gauge value for name with unit "1".
func gaugeValue(t *testing.T, rec *telemetrytest.Recorder, name string) float64 {
	t.Helper()
	pts := rec.MetricPoints(name)
	if len(pts) != 1 {
		t.Fatalf("%s points = %d, want 1", name, len(pts))
	}
	p := pts[0]
	if p.Kind != "gauge" {
		t.Fatalf("%s kind = %q, want gauge", name, p.Kind)
	}
	if p.Unit != "1" {
		t.Fatalf("%s unit = %q, want 1", name, p.Unit)
	}
	return p.Value
}

// resolverPoint finds the per-resolver info-gauge point matching address+domain.
func resolverPoint(t *testing.T, rec *telemetrytest.Recorder, address, domain string) telemetrytest.MetricPoint {
	t.Helper()
	for _, p := range rec.MetricPoints("tailscale.dns.resolver") {
		if p.Attrs[attrAddress] == address && p.Attrs[attrDomain] == domain {
			return p
		}
	}
	t.Fatalf("no tailscale.dns.resolver point for address=%q domain=%q", address, domain)
	return telemetrytest.MetricPoint{}
}

func TestCollectEmitsCountsFlagsAndResolvers(t *testing.T) {
	api := &fakeAPI{cfg: &tsapi.DNSConfig{
		Nameservers: []tsapi.DNSResolver{
			{Address: "192.0.2.254"},
			{Address: "1.1.1.1", UseWithExitNode: true},
		},
		SplitDNS: map[string][]tsapi.DNSResolver{
			"corp.example.com": {
				{Address: "192.0.2.53", UseWithExitNode: true},
				{Address: "192.0.2.153"},
			},
			"dev.example.com": {{Address: "192.0.2.253"}},
		},
		SearchPaths:      []string{"example.com", "corp.example.com"},
		OverrideLocalDNS: true,
		MagicDNS:         true,
	}}
	rec := telemetrytest.New()

	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if got := gaugeValue(t, rec, "tailscale.dns.nameservers.count"); got != 2 {
		t.Errorf("nameservers.count = %v, want 2", got)
	}
	if got := gaugeValue(t, rec, "tailscale.dns.search_paths.count"); got != 2 {
		t.Errorf("search_paths.count = %v, want 2", got)
	}
	if got := gaugeValue(t, rec, "tailscale.dns.split_zones.count"); got != 2 {
		t.Errorf("split_zones.count = %v, want 2", got)
	}
	if got := gaugeValue(t, rec, "tailscale.dns.magic_dns"); got != 1 {
		t.Errorf("magic_dns = %v, want 1", got)
	}
	if got := gaugeValue(t, rec, "tailscale.dns.override_local"); got != 1 {
		t.Errorf("override_local = %v, want 1", got)
	}
	// 2 exit-node-eligible resolvers: 1.1.1.1 (global) + 192.0.2.53 (split).
	if got := gaugeValue(t, rec, "tailscale.dns.resolvers.use_with_exit_node"); got != 2 {
		t.Errorf("use_with_exit_node = %v, want 2", got)
	}

	// Per-resolver info gauge: 4 series total (2 global + 2 corp + 1 dev = 5).
	if got := len(rec.MetricPoints("tailscale.dns.resolver")); got != 5 {
		t.Fatalf("resolver points = %d, want 5", got)
	}
	// Global resolver with exit-node off.
	g := resolverPoint(t, rec, "192.0.2.254", "")
	if g.Value != 1 || g.Attrs[attrKind] != "global" || g.Attrs[attrUseWithExitNode] != "false" {
		t.Errorf("192.0.2.254 = %+v, want value 1 kind=global use_with_exit_node=false", g.Attrs)
	}
	// Split resolver with exit-node on, carries its domain label.
	s := resolverPoint(t, rec, "192.0.2.53", "corp.example.com")
	if s.Value != 1 || s.Attrs[attrKind] != "split" || s.Attrs[attrUseWithExitNode] != "true" {
		t.Errorf("192.0.2.53 = %+v, want value 1 kind=split use_with_exit_node=true", s.Attrs)
	}
}

// TestCollectSplitDNSEmptyResolverListGetsIdentifiablePoint is the regression
// test for #63: a split-DNS domain with a null/empty resolver list is counted
// in tailscale.dns.split_zones.count, but the per-resolver loop previously
// emitted nothing for it, leaving no series to identify which counted domain
// has no resolvers. It must now get its own point (address="") so it stays
// identifiable alongside a domain with real resolvers.
func TestCollectSplitDNSEmptyResolverListGetsIdentifiablePoint(t *testing.T) {
	api := &fakeAPI{cfg: &tsapi.DNSConfig{
		SplitDNS: map[string][]tsapi.DNSResolver{
			"corp.example.com":  {{Address: "192.0.2.53", UseWithExitNode: true}},
			"empty.example.com": nil, // wire value decoded as a present key with an empty slice
		},
	}}
	rec := telemetrytest.New()

	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	// split_zones.count counts the domain key regardless of its resolver list.
	if got := gaugeValue(t, rec, "tailscale.dns.split_zones.count"); got != 2 {
		t.Fatalf("split_zones.count = %v, want 2", got)
	}

	// The populated domain still emits its normal resolver point.
	p := resolverPoint(t, rec, "192.0.2.53", "corp.example.com")
	if p.Value != 1 || p.Attrs[attrKind] != resolverKindSplit {
		t.Errorf("corp resolver = %+v, want value 1 kind=split", p.Attrs)
	}

	// The empty-resolver-list domain gets its own identifiable point: address
	// empty, but a populated split-DNS domain label — a combination that never
	// occurs for a real resolver (global resolvers always have domain="").
	empty := resolverPoint(t, rec, "", "empty.example.com")
	if empty.Value != 1 {
		t.Errorf("empty.example.com resolver value = %v, want 1", empty.Value)
	}
	if empty.Attrs[attrKind] != resolverKindSplit {
		t.Errorf("empty.example.com resolver kind = %q, want split", empty.Attrs[attrKind])
	}
	if empty.Attrs[attrUseWithExitNode] != "false" {
		t.Errorf("empty.example.com use_with_exit_node = %q, want false", empty.Attrs[attrUseWithExitNode])
	}

	// Exactly 2 resolver points total: the one real corp resolver plus the one
	// synthetic point for the empty domain.
	if got := len(rec.MetricPoints("tailscale.dns.resolver")); got != 2 {
		t.Fatalf("resolver points = %d, want 2", got)
	}
}

func TestCollectMinimalConfig(t *testing.T) {
	// Matches the real capture: one global resolver, no exit-node, no splitDNS.
	api := &fakeAPI{cfg: &tsapi.DNSConfig{
		Nameservers:      []tsapi.DNSResolver{{Address: "192.0.2.254"}},
		SearchPaths:      []string{"example.com"},
		OverrideLocalDNS: true,
		MagicDNS:         true,
	}}
	rec := telemetrytest.New()

	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if got := gaugeValue(t, rec, "tailscale.dns.split_zones.count"); got != 0 {
		t.Errorf("split_zones.count = %v, want 0", got)
	}
	if got := gaugeValue(t, rec, "tailscale.dns.resolvers.use_with_exit_node"); got != 0 {
		t.Errorf("use_with_exit_node = %v, want 0", got)
	}
	if got := len(rec.MetricPoints("tailscale.dns.resolver")); got != 1 {
		t.Errorf("resolver points = %d, want 1", got)
	}
}

func TestCollectEmptyConfig(t *testing.T) {
	api := &fakeAPI{cfg: &tsapi.DNSConfig{}}
	rec := telemetrytest.New()

	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, name := range []string{
		"tailscale.dns.nameservers.count", "tailscale.dns.search_paths.count",
		"tailscale.dns.split_zones.count", "tailscale.dns.magic_dns",
		"tailscale.dns.override_local", "tailscale.dns.resolvers.use_with_exit_node",
	} {
		if got := gaugeValue(t, rec, name); got != 0 {
			t.Errorf("%s = %v, want 0", name, got)
		}
	}
	if got := len(rec.MetricPoints("tailscale.dns.resolver")); got != 0 {
		t.Errorf("resolver points = %d, want 0 (no resolvers)", got)
	}
}

func TestCollectPropagatesError(t *testing.T) {
	api := &fakeAPI{err: context.DeadlineExceeded}
	rec := telemetrytest.New()
	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err == nil {
		t.Fatalf("Collect: expected error, got nil")
	}
}

func TestCollectSearchPathInfoGauge(t *testing.T) {
	api := &fakeAPI{cfg: &tsapi.DNSConfig{
		SearchPaths: []string{"corp.example.com", "lab.example.com"},
	}}
	rec := telemetrytest.New()

	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	// The count metric must still be present and correct.
	if got := gaugeValue(t, rec, "tailscale.dns.search_paths.count"); got != 2 {
		t.Errorf("search_paths.count = %v, want 2", got)
	}

	// The new per-search-path info gauge must have exactly 2 datapoints.
	pts := rec.MetricPoints("tailscale.dns.search_path")
	if len(pts) != 2 {
		t.Fatalf("search_path points = %d, want 2", len(pts))
	}

	// Build a map of domain → point for easy lookup.
	byDomain := make(map[string]telemetrytest.MetricPoint, len(pts))
	for _, p := range pts {
		byDomain[p.Attrs[attrSearchPathDomain]] = p
	}

	for _, domain := range []string{"corp.example.com", "lab.example.com"} {
		p, ok := byDomain[domain]
		if !ok {
			t.Errorf("no search_path point for domain=%q", domain)
			continue
		}
		if p.Kind != "gauge" {
			t.Errorf("search_path[%s] kind = %q, want gauge", domain, p.Kind)
		}
		if p.Unit != "1" {
			t.Errorf("search_path[%s] unit = %q, want 1", domain, p.Unit)
		}
		if p.Value != 1 {
			t.Errorf("search_path[%s] value = %v, want 1", domain, p.Value)
		}
	}
}

// TestResolverSeriesDropsOutOnReuse is the #55 regression test: reusing the
// SAME collector instance (so its GaugeSnapshotBuilder persists) across two
// Collects, a resolver present on the first tick but gone on the second must
// have its tailscale.dns.resolver series cleared after the second collection —
// not ghost forever under cumulative temporality. rec.MetricPoints triggers a
// fresh SDK collection each call, so the second read reflects the second tick.
func TestResolverSeriesDropsOutOnReuse(t *testing.T) {
	api := &fakeAPI{cfg: &tsapi.DNSConfig{
		Nameservers: []tsapi.DNSResolver{
			{Address: "192.0.2.1"},
			{Address: "192.0.2.2"},
		},
	}}
	rec := telemetrytest.New()
	c := New(api, 0) // single instance reused across both ticks

	// Tick 1: two resolvers — both series present.
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect tick 1: %v", err)
	}
	if got := len(rec.MetricPoints("tailscale.dns.resolver")); got != 2 {
		t.Fatalf("tick 1 resolver points = %d, want 2", got)
	}

	// Tick 2 on the SAME collector: 192.0.2.2 is gone. Its series must drop.
	api.cfg = &tsapi.DNSConfig{
		Nameservers: []tsapi.DNSResolver{{Address: "192.0.2.1"}},
	}
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect tick 2: %v", err)
	}

	pts := rec.MetricPoints("tailscale.dns.resolver")
	if len(pts) != 1 {
		t.Fatalf("tick 2 resolver points = %d, want 1 (departed resolver must drop, not ghost)", len(pts))
	}
	if got := pts[0].Attrs[attrAddress]; got != "192.0.2.1" {
		t.Fatalf("tick 2 surviving resolver address = %q, want 192.0.2.1", got)
	}
	for _, p := range pts {
		if p.Attrs[attrAddress] == "192.0.2.2" {
			t.Errorf("tick 2: departed resolver 192.0.2.2 still present (ghost): %+v", p.Attrs)
		}
	}
}

func TestCollectSearchPathEmptyYieldsNoInfoPoints(t *testing.T) {
	api := &fakeAPI{cfg: &tsapi.DNSConfig{}}
	rec := telemetrytest.New()

	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if got := len(rec.MetricPoints("tailscale.dns.search_path")); got != 0 {
		t.Errorf("search_path points = %d, want 0 (no search paths)", got)
	}
}
