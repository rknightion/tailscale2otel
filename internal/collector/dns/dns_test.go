package dns

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
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
			{Address: "10.0.0.254"},
			{Address: "1.1.1.1", UseWithExitNode: true},
		},
		SplitDNS: map[string][]tsapi.DNSResolver{
			"corp.example.com": {
				{Address: "10.0.0.53", UseWithExitNode: true},
				{Address: "10.0.1.53"},
			},
			"dev.example.com": {{Address: "10.0.2.53"}},
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
	// 2 exit-node-eligible resolvers: 1.1.1.1 (global) + 10.0.0.53 (split).
	if got := gaugeValue(t, rec, "tailscale.dns.resolvers.use_with_exit_node"); got != 2 {
		t.Errorf("use_with_exit_node = %v, want 2", got)
	}

	// Per-resolver info gauge: 4 series total (2 global + 2 corp + 1 dev = 5).
	if got := len(rec.MetricPoints("tailscale.dns.resolver")); got != 5 {
		t.Fatalf("resolver points = %d, want 5", got)
	}
	// Global resolver with exit-node off.
	g := resolverPoint(t, rec, "10.0.0.254", "")
	if g.Value != 1 || g.Attrs[attrKind] != "global" || g.Attrs[attrUseWithExitNode] != "false" {
		t.Errorf("10.0.0.254 = %+v, want value 1 kind=global use_with_exit_node=false", g.Attrs)
	}
	// Split resolver with exit-node on, carries its domain label.
	s := resolverPoint(t, rec, "10.0.0.53", "corp.example.com")
	if s.Value != 1 || s.Attrs[attrKind] != "split" || s.Attrs[attrUseWithExitNode] != "true" {
		t.Errorf("10.0.0.53 = %+v, want value 1 kind=split use_with_exit_node=true", s.Attrs)
	}
}

func TestCollectMinimalConfig(t *testing.T) {
	// Matches the real capture: one global resolver, no exit-node, no splitDNS.
	api := &fakeAPI{cfg: &tsapi.DNSConfig{
		Nameservers:      []tsapi.DNSResolver{{Address: "10.0.0.254"}},
		SearchPaths:      []string{"rob-knight.net"},
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
