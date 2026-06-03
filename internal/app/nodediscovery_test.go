package app

import (
	"context"
	"errors"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/internal/config"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

// fakeDevicesAPI satisfies nodeDiscoveryAPI for discoverer tests.
type fakeDevicesAPI struct {
	devs []tsapi.RichDevice
	err  error
}

func (f *fakeDevicesAPI) DevicesRich(context.Context) ([]tsapi.RichDevice, error) {
	return f.devs, f.err
}

// discoveryDefaults returns the documented default discovery config (so tests
// override only the field under test).
func discoveryDefaults() config.NodeMetricsDiscovery {
	return config.Default().Collectors.NodeMetrics.Discovery
}

func mustDiscover(t *testing.T, devs []tsapi.RichDevice, cfg config.NodeMetricsDiscovery) []nodemetrics.Target {
	t.Helper()
	got, err := newNodeDiscoverer(&fakeDevicesAPI{devs: devs}, cfg).Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	return got
}

func TestNodeDiscoverer_FiltersOnlineAndExternal(t *testing.T) {
	devs := []tsapi.RichDevice{
		{Hostname: "a", Addresses: []string{"100.64.0.1"}, ConnectedToControl: true, IsExternal: false},
		{Hostname: "b", Addresses: []string{"100.64.0.2"}, ConnectedToControl: false, IsExternal: false},
		{Hostname: "c", Addresses: []string{"100.64.0.3"}, ConnectedToControl: true, IsExternal: true},
	}
	got := mustDiscover(t, devs, discoveryDefaults())
	if len(got) != 1 {
		t.Fatalf("targets = %d, want 1 (only online internal); got %+v", len(got), got)
	}
	if got[0].URL != "http://100.64.0.1:5252/metrics" {
		t.Fatalf("URL = %q, want a's", got[0].URL)
	}
}

func TestNodeDiscoverer_TagIncludeExclude(t *testing.T) {
	cfg := discoveryDefaults()
	cfg.IncludeTags = []string{"tag:server"}
	cfg.ExcludeTags = []string{"tag:no"}
	devs := []tsapi.RichDevice{
		{Hostname: "a", Addresses: []string{"100.64.0.1"}, ConnectedToControl: true, Tags: []string{"tag:server"}},
		{Hostname: "b", Addresses: []string{"100.64.0.2"}, ConnectedToControl: true, Tags: []string{"tag:client"}},
		{Hostname: "c", Addresses: []string{"100.64.0.3"}, ConnectedToControl: true, Tags: []string{"tag:server", "tag:no"}},
	}
	got := mustDiscover(t, devs, cfg)
	if len(got) != 1 || got[0].URL != "http://100.64.0.1:5252/metrics" {
		t.Fatalf("targets = %+v, want only a (b lacks include tag; c excluded by tag:no)", got)
	}
}

func TestNodeDiscoverer_IPv6Bracketing(t *testing.T) {
	cfg := discoveryDefaults()
	cfg.AddressOrder = "ipv6"
	devs := []tsapi.RichDevice{{Hostname: "a", Addresses: []string{"100.64.0.1", "fd7a::1"}, ConnectedToControl: true}}
	got := mustDiscover(t, devs, cfg)
	if len(got) != 1 || got[0].URL != "http://[fd7a::1]:5252/metrics" {
		t.Fatalf("URL = %+v, want http://[fd7a::1]:5252/metrics (IPv6 bracketed)", got)
	}
}

func TestNodeDiscoverer_AddressFallback(t *testing.T) {
	cfg := discoveryDefaults() // ipv4 preferred
	devs := []tsapi.RichDevice{{Hostname: "a", Addresses: []string{"fd7a::1"}, ConnectedToControl: true}}
	got := mustDiscover(t, devs, cfg)
	if len(got) != 1 || got[0].URL != "http://[fd7a::1]:5252/metrics" {
		t.Fatalf("URL = %+v, want IPv6 fallback when no IPv4 present", got)
	}
}

func TestNodeDiscoverer_EmptyAddressSkipped(t *testing.T) {
	devs := []tsapi.RichDevice{
		{Hostname: "noaddr", Addresses: nil, ConnectedToControl: true},
		{Hostname: "bad", Addresses: []string{"not-an-ip"}, ConnectedToControl: true},
		{Hostname: "ok", Addresses: []string{"100.64.0.5"}, ConnectedToControl: true},
	}
	got := mustDiscover(t, devs, discoveryDefaults())
	if len(got) != 1 || got[0].URL != "http://100.64.0.5:5252/metrics" {
		t.Fatalf("targets = %+v, want only the device with a usable address", got)
	}
}

func TestNodeDiscoverer_InstanceSource(t *testing.T) {
	dev := tsapi.RichDevice{Hostname: "host1", Name: "host1.ts.net", Addresses: []string{"100.64.0.1"}, ConnectedToControl: true}
	cases := map[string]string{
		"address":  "", // empty so the collector derives host:port from the URL
		"name":     "host1.ts.net",
		"hostname": "host1",
	}
	for src, want := range cases {
		cfg := discoveryDefaults()
		cfg.InstanceSource = src
		got := mustDiscover(t, []tsapi.RichDevice{dev}, cfg)
		if len(got) != 1 {
			t.Fatalf("[%s] targets = %d, want 1", src, len(got))
		}
		if got[0].Instance != want {
			t.Fatalf("[%s] Instance = %q, want %q", src, got[0].Instance, want)
		}
	}
}

func TestNodeDiscoverer_PassthroughLabels(t *testing.T) {
	tagged := tsapi.RichDevice{Hostname: "h", ID: "id1", Addresses: []string{"100.64.0.1"}, ConnectedToControl: true, Tags: []string{"tag:a", "tag:b"}}
	got := mustDiscover(t, []tsapi.RichDevice{tagged}, discoveryDefaults())
	if len(got) != 1 {
		t.Fatalf("targets = %d, want 1", len(got))
	}
	lbl := got[0].Labels
	if lbl[semconv.HostName] != "h" || lbl[semconv.HostID] != "id1" {
		t.Fatalf("host labels = %v, want host.name=h host.id=id1", lbl)
	}
	if lbl[semconv.AttrTags] != "tag:a,tag:b" {
		t.Fatalf("tags label = %q, want tag:a,tag:b", lbl[semconv.AttrTags])
	}

	// Untagged device: the tags label key must be absent.
	untagged := tsapi.RichDevice{Hostname: "h2", ID: "id2", Addresses: []string{"100.64.0.2"}, ConnectedToControl: true}
	got = mustDiscover(t, []tsapi.RichDevice{untagged}, discoveryDefaults())
	if _, ok := got[0].Labels[semconv.AttrTags]; ok {
		t.Fatalf("untagged device should not set %s; labels=%v", semconv.AttrTags, got[0].Labels)
	}

	// Both label toggles off: no passthrough labels at all.
	cfg := discoveryDefaults()
	cfg.IncludeHostLabels = false
	cfg.IncludeTagsLabel = false
	got = mustDiscover(t, []tsapi.RichDevice{tagged}, cfg)
	if len(got[0].Labels) != 0 {
		t.Fatalf("labels with both toggles off = %v, want none", got[0].Labels)
	}
}

func TestNodeDiscoverer_APIErrorPropagates(t *testing.T) {
	want := errors.New("boom")
	_, err := newNodeDiscoverer(&fakeDevicesAPI{err: want}, discoveryDefaults()).Discover(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}
