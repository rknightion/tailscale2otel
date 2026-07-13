package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/v2/internal/config"
	"github.com/rknightion/tailscale2otel/v2/internal/enrich"
	"github.com/rknightion/tailscale2otel/v2/internal/semconv"
	"github.com/rknightion/tailscale2otel/v2/internal/tsapi"
)

// discardLog is a no-op logger for discoverer tests that don't assert on logs.
func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeDevicesAPI satisfies nodeDiscoveryAPI for discoverer tests. calls counts
// invocations so cache-path tests (#85) can assert the API poll was skipped.
type fakeDevicesAPI struct {
	devs  []tsapi.RichDevice
	err   error
	calls int
}

func (f *fakeDevicesAPI) DevicesRich(context.Context) ([]tsapi.RichDevice, error) {
	f.calls++
	return f.devs, f.err
}

// fakeDeviceCache satisfies deviceCacheReader for discoverer tests (#85),
// standing in for *enrich.DeviceCache without needing a real cache/Replace.
type fakeDeviceCache struct{ devices []enrich.DeviceMeta }

func (f *fakeDeviceCache) Snapshot() []enrich.DeviceMeta { return f.devices }

// discoveryDefaults returns the documented default discovery config (so tests
// override only the field under test).
func discoveryDefaults() config.NodeMetricsDiscovery {
	return config.Default().Collectors.NodeMetrics.Discovery
}

func mustDiscover(t *testing.T, devs []tsapi.RichDevice, cfg config.NodeMetricsDiscovery) []nodemetrics.Target {
	t.Helper()
	got, err := newNodeDiscoverer(&fakeDevicesAPI{devs: devs}, cfg, discardLog()).Discover(context.Background())
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
	devs := []tsapi.RichDevice{{Hostname: "a", Addresses: []string{"100.64.0.1", "fd7a:115c:a1e0::1"}, ConnectedToControl: true}}
	got := mustDiscover(t, devs, cfg)
	if len(got) != 1 || got[0].URL != "http://[fd7a:115c:a1e0::1]:5252/metrics" {
		t.Fatalf("URL = %+v, want http://[fd7a:115c:a1e0::1]:5252/metrics (IPv6 bracketed)", got)
	}
}

func TestNodeDiscoverer_AddressFallback(t *testing.T) {
	cfg := discoveryDefaults() // ipv4 preferred
	devs := []tsapi.RichDevice{{Hostname: "a", Addresses: []string{"fd7a:115c:a1e0::1"}, ConnectedToControl: true}}
	got := mustDiscover(t, devs, cfg)
	if len(got) != 1 || got[0].URL != "http://[fd7a:115c:a1e0::1]:5252/metrics" {
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
	dev := tsapi.RichDevice{Hostname: "myhost", Name: "host1.tail-scale.ts.net", Addresses: []string{"100.64.0.1"}, ConnectedToControl: true}
	cases := map[string]string{
		"address": "",      // empty so the collector derives host:port from the URL
		"name":    "host1", // MagicDNS short name: the FQDN's first label (tailnet domain stripped)
		// hostname is non-unique, so it is ALWAYS address-suffixed for a stable,
		// batch-independent label (#98) — not left bare.
		"hostname": "myhost@100.64.0.1",
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

func TestNodeDiscoverer_MaxTargetsCapsDiscovery(t *testing.T) {
	cfg := discoveryDefaults()
	cfg.MaxTargets = 2
	devs := []tsapi.RichDevice{
		{Hostname: "a", Addresses: []string{"100.64.0.1"}, ConnectedToControl: true},
		{Hostname: "b", Addresses: []string{"100.64.0.2"}, ConnectedToControl: true},
		{Hostname: "c", Addresses: []string{"100.64.0.3"}, ConnectedToControl: true},
	}
	got := mustDiscover(t, devs, cfg)
	if len(got) != 2 {
		t.Fatalf("targets = %d, want capped to 2; got %+v", len(got), got)
	}
	if got[0].URL != "http://100.64.0.1:5252/metrics" || got[1].URL != "http://100.64.0.2:5252/metrics" {
		t.Fatalf("targets = %+v, want first two matching devices", got)
	}
}

func TestNodeDiscoverer_DisambiguatesCollidingInstances(t *testing.T) {
	// Several devices commonly report the SAME OS hostname (e.g. phones default to
	// "localhost"). With instance_source: hostname that would collapse them onto one
	// tailscale.node label and silently merge their metrics — so colliding labels
	// must be made unique.
	cfg := discoveryDefaults()
	cfg.InstanceSource = "hostname"
	devs := []tsapi.RichDevice{
		{Hostname: "localhost", Addresses: []string{"100.64.0.1"}, ConnectedToControl: true},
		{Hostname: "localhost", Addresses: []string{"100.64.0.2"}, ConnectedToControl: true},
		{Hostname: "camden", Addresses: []string{"100.64.0.3"}, ConnectedToControl: true},
	}
	got := mustDiscover(t, devs, cfg)
	if len(got) != 3 {
		t.Fatalf("targets = %d, want 3", len(got))
	}
	counts := map[string]int{}
	for _, tg := range got {
		counts[tg.Instance]++
	}
	for inst, n := range counts {
		if n > 1 {
			t.Fatalf("instance %q appears %d times; must be unique: %+v", inst, n, got)
		}
	}
	// Every hostname-source instance is address-suffixed (stable, batch-independent
	// — #98), including the non-colliding "camden"; none is left bare.
	for _, want := range []string{"localhost@100.64.0.1", "localhost@100.64.0.2", "camden@100.64.0.3"} {
		if counts[want] != 1 {
			t.Fatalf("want stable suffixed instance %q; got %+v", want, got)
		}
	}
	if counts["localhost"] != 0 || counts["camden"] != 0 {
		t.Fatalf("no bare hostname instance should remain (all address-suffixed); got %+v", got)
	}
}

// TestNodeDiscoverer_StableAcrossSiblingChurn pins #98: a device's instance label
// must NOT change when a colliding sibling goes offline between refreshes.
func TestNodeDiscoverer_StableAcrossSiblingChurn(t *testing.T) {
	cfg := discoveryDefaults()
	cfg.InstanceSource = "hostname"
	dev := tsapi.RichDevice{Hostname: "localhost", Addresses: []string{"100.64.0.1"}, ConnectedToControl: true}
	sibling := tsapi.RichDevice{Hostname: "localhost", Addresses: []string{"100.64.0.2"}, ConnectedToControl: true}

	withSibling := mustDiscover(t, []tsapi.RichDevice{dev, sibling}, cfg)
	alone := mustDiscover(t, []tsapi.RichDevice{dev}, cfg)

	find := func(ts []nodemetrics.Target, addr string) string {
		for _, tg := range ts {
			if strings.Contains(tg.URL, addr) {
				return tg.Instance
			}
		}
		return ""
	}
	withSib := find(withSibling, "100.64.0.1")
	without := find(alone, "100.64.0.1")
	if withSib == "" || withSib != without {
		t.Fatalf("instance label flapped with sibling churn: with=%q without=%q (must be stable)", withSib, without)
	}
}

func TestNodeDiscoverer_WarnsOnInstanceCollision(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	cfg := discoveryDefaults()
	cfg.InstanceSource = "hostname"
	devs := []tsapi.RichDevice{
		{Hostname: "localhost", Addresses: []string{"100.64.0.1"}, ConnectedToControl: true},
		{Hostname: "localhost", Addresses: []string{"100.64.0.2"}, ConnectedToControl: true},
	}
	if _, err := newNodeDiscoverer(&fakeDevicesAPI{devs: devs}, cfg, log).Discover(context.Background()); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "localhost") || !strings.Contains(out, "instance_source=hostname") {
		t.Fatalf("want a collision WARN naming localhost + instance_source=hostname; got %q", out)
	}
}

func TestNodeDiscoverer_APIErrorPropagates(t *testing.T) {
	want := errors.New("boom")
	_, err := newNodeDiscoverer(&fakeDevicesAPI{err: want}, discoveryDefaults(), discardLog()).Discover(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

// --- #85: reuse the devices collector's cache instead of a separate DevicesRich() poll ---

// TestNodeDiscoverer_UsesSharedCacheWhenPopulated verifies that once a
// withDeviceCache option is set and the cache holds devices, Discover sources
// its device view entirely from the cache and never calls the (heaviest,
// rate-limit-sensitive) DevicesRich() API.
func TestNodeDiscoverer_UsesSharedCacheWhenPopulated(t *testing.T) {
	api := &fakeDevicesAPI{devs: []tsapi.RichDevice{{Hostname: "should-not-be-used", Addresses: []string{"100.64.0.9"}, ConnectedToControl: true}}}
	cache := &fakeDeviceCache{devices: []enrich.DeviceMeta{
		{ID: "1", NodeID: "n1", Hostname: "cached", Name: "cached.tail1a2b.ts.net",
			Online: true, Addrs: []netip.Addr{netip.MustParseAddr("100.64.0.1")}},
	}}
	d := newNodeDiscoverer(api, discoveryDefaults(), discardLog(), withDeviceCache(cache))

	got, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if api.calls != 0 {
		t.Fatalf("DevicesRich called %d times, want 0 (cache path must skip the API poll)", api.calls)
	}
	if len(got) != 1 || got[0].URL != "http://100.64.0.1:5252/metrics" {
		t.Fatalf("targets = %+v, want the cached device only", got)
	}
}

// TestNodeDiscoverer_FallsBackToAPIWhenCacheEmpty verifies Discover falls back
// to the API poll when the cache is configured but currently empty — the
// state a disabled (or not-yet-ticked) devices collector leaves it in, so
// node-metrics discovery never silently produces zero targets in that case.
func TestNodeDiscoverer_FallsBackToAPIWhenCacheEmpty(t *testing.T) {
	api := &fakeDevicesAPI{devs: []tsapi.RichDevice{{Hostname: "from-api", Addresses: []string{"100.64.0.5"}, ConnectedToControl: true}}}
	cache := &fakeDeviceCache{} // empty: devices collector disabled or not ticked yet
	d := newNodeDiscoverer(api, discoveryDefaults(), discardLog(), withDeviceCache(cache))

	got, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if api.calls != 1 {
		t.Fatalf("DevicesRich called %d times, want 1 (fallback path)", api.calls)
	}
	if len(got) != 1 || got[0].URL != "http://100.64.0.5:5252/metrics" {
		t.Fatalf("targets = %+v, want the API-sourced device", got)
	}
}

// TestNodeDiscoverer_NoCacheConfiguredUsesAPI pins the default/legacy behavior:
// with no withDeviceCache option at all (e.g. multi-tailnet wiring that hasn't
// adopted the cache, or the option simply omitted), Discover polls the API
// exactly as before #85.
func TestNodeDiscoverer_NoCacheConfiguredUsesAPI(t *testing.T) {
	api := &fakeDevicesAPI{devs: []tsapi.RichDevice{{Hostname: "a", Addresses: []string{"100.64.0.1"}, ConnectedToControl: true}}}
	d := newNodeDiscoverer(api, discoveryDefaults(), discardLog())

	got, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if api.calls != 1 {
		t.Fatalf("DevicesRich called %d times, want 1 (no cache configured)", api.calls)
	}
	if len(got) != 1 {
		t.Fatalf("targets = %+v, want 1", got)
	}
}

// TestNodeDiscoverer_CachePathAppliesSameFiltersAndLabels verifies the
// cache-sourced path (enrich.DeviceMeta) applies the identical online/tag
// filters and HostID/tags/instance labeling as the API-sourced path
// (tsapi.RichDevice) — the #85 acceptance criterion that behavior must be
// identical regardless of source.
func TestNodeDiscoverer_CachePathAppliesSameFiltersAndLabels(t *testing.T) {
	cfg := discoveryDefaults()
	cfg.IncludeTags = []string{"tag:server"}
	cache := &fakeDeviceCache{devices: []enrich.DeviceMeta{
		{ // online, tagged, matches include filter -> kept
			ID: "id-a", NodeID: "n-a", Name: "a.tail1a2b.ts.net", Hostname: "a",
			Tags: []string{"tag:server"}, Online: true,
			Addrs: []netip.Addr{netip.MustParseAddr("100.64.0.1")},
		},
		{ // offline -> dropped by online_only (default true)
			ID: "id-b", NodeID: "n-b", Hostname: "b", Tags: []string{"tag:server"}, Online: false,
			Addrs: []netip.Addr{netip.MustParseAddr("100.64.0.2")},
		},
		{ // online but lacks the include tag -> dropped
			ID: "id-c", NodeID: "n-c", Hostname: "c", Online: true,
			Addrs: []netip.Addr{netip.MustParseAddr("100.64.0.3")},
		},
		{ // external -> dropped by exclude_external (default true)
			ID: "id-d", NodeID: "n-d", Hostname: "d", Tags: []string{"tag:server"}, Online: true,
			External: true, Addrs: []netip.Addr{netip.MustParseAddr("100.64.0.4")},
		},
	}}
	d := newNodeDiscoverer(&fakeDevicesAPI{}, cfg, discardLog(), withDeviceCache(cache))

	got, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("targets = %+v, want only device a (online+tagged+internal)", got)
	}
	tg := got[0]
	if tg.URL != "http://100.64.0.1:5252/metrics" {
		t.Fatalf("URL = %q, want a's", tg.URL)
	}
	if tg.Labels[semconv.HostID] != "id-a" {
		t.Fatalf("HostID label = %q, want id-a (from DeviceMeta.ID)", tg.Labels[semconv.HostID])
	}
	if tg.Labels[semconv.AttrTags] != "tag:server" {
		t.Fatalf("tags label = %q, want tag:server", tg.Labels[semconv.AttrTags])
	}
}

// A scrape target must be a Tailscale-range address: the API's addresses list is
// the input, and anything else (metadata endpoints, loopback, RFC1918) must never
// become a scrape destination even if the control plane is compromised.
func TestPickAddressRejectsNonTailscaleRanges(t *testing.T) {
	cases := []struct {
		name  string
		addrs []string
		order string
		want  string // "" => want ok=false
	}{
		{"metadata endpoint skipped", []string{"169.254.169.254"}, "ipv4", ""},
		{"loopback skipped", []string{"127.0.0.1"}, "ipv4", ""},
		{"rfc1918 skipped, cgnat chosen", []string{"10.1.2.3", "100.64.1.5"}, "ipv4", "100.64.1.5"},
		{"non-ts ula skipped", []string{"fd00::1"}, "ipv6", ""},
		{"ts ula chosen", []string{"fd7a:115c:a1e0::1"}, "ipv6", "fd7a:115c:a1e0::1"},
	}
	for _, tc := range cases {
		got, ok := pickAddress(tc.addrs, tc.order)
		if tc.want == "" {
			if ok {
				t.Errorf("%s: got %s, want no address", tc.name, got)
			}
			continue
		}
		if !ok || got.String() != tc.want {
			t.Errorf("%s: got %v ok=%v, want %s", tc.name, got, ok, tc.want)
		}
	}
}
