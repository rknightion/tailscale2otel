package dns

import (
	"context"
	"testing"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// fakeAPI implements the narrow dns api interface for tests.
type fakeAPI struct {
	nameservers []string
	searchPaths []string
	splitDNS    tsclient.SplitDNSResponse
	prefs       *tsclient.DNSPreferences

	errNS, errSP, errSplit, errPrefs error
}

func (f *fakeAPI) DNSNameservers(_ context.Context) ([]string, error) {
	return f.nameservers, f.errNS
}
func (f *fakeAPI) DNSSearchPaths(_ context.Context) ([]string, error) {
	return f.searchPaths, f.errSP
}
func (f *fakeAPI) DNSSplitDNS(_ context.Context) (tsclient.SplitDNSResponse, error) {
	return f.splitDNS, f.errSplit
}
func (f *fakeAPI) DNSPreferences(_ context.Context) (*tsclient.DNSPreferences, error) {
	return f.prefs, f.errPrefs
}

// SnapshotCollector compile-time check.
var _ collector.SnapshotCollector = (*Collector)(nil)

func TestNameAndDefaultInterval(t *testing.T) {
	c := New(&fakeAPI{prefs: &tsclient.DNSPreferences{}}, 0)
	if c.Name() != "dns" {
		t.Fatalf("Name() = %q, want dns", c.Name())
	}
	if got := c.DefaultInterval(); got != 600*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 600s", got)
	}
	c2 := New(&fakeAPI{prefs: &tsclient.DNSPreferences{}}, 120*time.Second)
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

func TestCollectEmitsCountsAndFlags(t *testing.T) {
	api := &fakeAPI{
		nameservers: []string{"1.1.1.1", "8.8.8.8", "9.9.9.9"},
		searchPaths: []string{"example.com", "corp.example.com"},
		splitDNS: tsclient.SplitDNSResponse{
			"corp.example.com": {"10.0.0.1"},
			"dev.example.com":  {"10.0.0.2", "10.0.0.3"},
		},
		prefs: &tsclient.DNSPreferences{MagicDNS: true},
	}
	rec := telemetrytest.New()

	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if got := gaugeValue(t, rec, "tailscale.dns.nameservers.count"); got != 3 {
		t.Fatalf("nameservers.count = %v, want 3", got)
	}
	if got := gaugeValue(t, rec, "tailscale.dns.search_paths.count"); got != 2 {
		t.Fatalf("search_paths.count = %v, want 2", got)
	}
	if got := gaugeValue(t, rec, "tailscale.dns.split_zones.count"); got != 2 {
		t.Fatalf("split_zones.count = %v, want 2", got)
	}
	if got := gaugeValue(t, rec, "tailscale.dns.magic_dns"); got != 1 {
		t.Fatalf("magic_dns = %v, want 1", got)
	}
}

func TestCollectMagicDNSOff(t *testing.T) {
	api := &fakeAPI{
		nameservers: nil,
		searchPaths: nil,
		splitDNS:    tsclient.SplitDNSResponse{},
		prefs:       &tsclient.DNSPreferences{MagicDNS: false},
	}
	rec := telemetrytest.New()

	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if got := gaugeValue(t, rec, "tailscale.dns.nameservers.count"); got != 0 {
		t.Fatalf("nameservers.count = %v, want 0", got)
	}
	if got := gaugeValue(t, rec, "tailscale.dns.search_paths.count"); got != 0 {
		t.Fatalf("search_paths.count = %v, want 0", got)
	}
	if got := gaugeValue(t, rec, "tailscale.dns.split_zones.count"); got != 0 {
		t.Fatalf("split_zones.count = %v, want 0", got)
	}
	if got := gaugeValue(t, rec, "tailscale.dns.magic_dns"); got != 0 {
		t.Fatalf("magic_dns = %v, want 0", got)
	}
}

func TestCollectPropagatesError(t *testing.T) {
	cases := map[string]*fakeAPI{
		"nameservers": {errNS: context.DeadlineExceeded, prefs: &tsclient.DNSPreferences{}},
		"searchpaths": {errSP: context.DeadlineExceeded, prefs: &tsclient.DNSPreferences{}},
		"splitdns":    {errSplit: context.DeadlineExceeded, prefs: &tsclient.DNSPreferences{}},
		"prefs":       {errPrefs: context.DeadlineExceeded},
	}
	for name, api := range cases {
		t.Run(name, func(t *testing.T) {
			rec := telemetrytest.New()
			if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err == nil {
				t.Fatalf("Collect(%s): expected error, got nil", name)
			}
		})
	}
}
