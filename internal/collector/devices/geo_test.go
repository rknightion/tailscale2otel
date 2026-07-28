package devices_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/collector/devices"
	"github.com/rknightion/tailscale2otel/v3/internal/enrich"
	"github.com/rknightion/tailscale2otel/v3/internal/geoip"
	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v3/internal/tsapi"
)

// metricDevicesByCountry is spelled out rather than read from the catalog: this
// is a metric name operators query, so pinning the literal here makes an
// accidental rename a test failure rather than a silently broken dashboard.
const metricDevicesByCountry = "tailscale.devices.by_country"

// newGeoCollector builds a devices collector over a fixed device list.
func newGeoCollector(t *testing.T, devs []tsapi.RichDevice, opts ...devices.Option) *devices.Collector {
	t.Helper()
	return devices.New(&fakeAPI{devices: devs}, enrich.NewDeviceCache(), time.Minute, false, false, opts...)
}

type fakeGeo map[string]geoip.Result

func (f fakeGeo) Lookup(addr netip.Addr) (geoip.Result, bool) {
	r, ok := f[addr.String()]
	return r, ok
}

var deviceGeo = fakeGeo{
	"203.0.113.10": {CountryISO: "GB", ContinentCode: "EU"},
	"198.51.100.4": {CountryISO: "US", ContinentCode: "NA"},
}

// geoDevices returns rich devices whose advertised magicsock endpoints are the
// input to device geolocation.
func geoDevices(endpoints ...[]string) []tsapi.RichDevice {
	out := make([]tsapi.RichDevice, 0, len(endpoints))
	for i, eps := range endpoints {
		out = append(out, tsapi.RichDevice{
			ID:        string(rune('a' + i)),
			Hostname:  string(rune('a'+i)) + "-host",
			Endpoints: eps,
		})
	}
	return out
}

// The fleet gauge is one series per country, which is bounded and is the whole
// reason device geography goes on a rollup rather than onto the per-device
// gauges.
func TestDeviceGeo_FleetRollup(t *testing.T) {
	rec := telemetrytest.New()
	c := newGeoCollector(t,
		geoDevices(
			[]string{"203.0.113.10:41641"},
			[]string{"203.0.113.10:41642"},
			[]string{"198.51.100.4:41641"},
		),
		devices.WithGeo(deviceGeo), devices.WithConnectivity(true))

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	got := map[string]float64{}
	for _, p := range rec.MetricPoints(metricDevicesByCountry) {
		got[p.Attrs[semconv.DeviceGeoCountryISO]] = p.Value
	}
	if got["GB"] != 2 || got["US"] != 1 {
		t.Errorf("devices by country = %v, want GB=2 US=1", got)
	}
	// The continent rides along as a second bounded label on the same series.
	for _, p := range rec.MetricPoints(metricDevicesByCountry) {
		if p.Attrs[semconv.DeviceGeoCountryISO] == "GB" && p.Attrs[semconv.DeviceGeoContinentCode] != "EU" {
			t.Errorf("GB continent = %q, want EU", p.Attrs[semconv.DeviceGeoContinentCode])
		}
	}
}

// A device's endpoints routinely include LAN addresses, which say nothing about
// where it is. The first GLOBAL endpoint is the one worth looking up.
func TestDeviceGeo_SkipsPrivateEndpoints(t *testing.T) {
	rec := telemetrytest.New()
	c := newGeoCollector(t,
		geoDevices([]string{"192.168.1.5:41641", "[fe80::1]:41641", "203.0.113.10:41641"}),
		devices.WithGeo(deviceGeo), devices.WithConnectivity(true))

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	pts := rec.MetricPoints(metricDevicesByCountry)
	if len(pts) != 1 || pts[0].Attrs[semconv.DeviceGeoCountryISO] != "GB" {
		t.Fatalf("devices by country = %+v, want a single GB series", pts)
	}
}

// A device with no usable endpoint contributes no series at all -- not an
// "unknown" bucket, which would read as a country that could be queried.
func TestDeviceGeo_NoUsableEndpoint(t *testing.T) {
	rec := telemetrytest.New()
	c := newGeoCollector(t,
		geoDevices([]string{"192.168.1.5:41641"}, nil, []string{"203.0.113.10:41641"}),
		devices.WithGeo(deviceGeo), devices.WithConnectivity(true))

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	pts := rec.MetricPoints(metricDevicesByCountry)
	if len(pts) != 1 || pts[0].Value != 1 {
		t.Fatalf("devices by country = %+v, want one series with value 1", pts)
	}
}

// The gauge exists only when geo is configured -- with no resolver the metric
// must not appear at all, rather than appear empty.
func TestDeviceGeo_AbsentWithoutResolver(t *testing.T) {
	rec := telemetrytest.New()
	c := newGeoCollector(t, geoDevices([]string{"203.0.113.10:41641"}), devices.WithConnectivity(true))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if pts := rec.MetricPoints(metricDevicesByCountry); len(pts) != 0 {
		t.Fatalf("devices-by-country emitted %d points with no geo resolver", len(pts))
	}
}

// Device geography must NOT become a label on the existing per-device gauges:
// adding one changes those series' identity and silently breaks every dashboard
// and recording rule already querying them.
func TestDeviceGeo_NotOnPerDeviceGauges(t *testing.T) {
	rec := telemetrytest.New()
	c := newGeoCollector(t, geoDevices([]string{"203.0.113.10:41641"}),
		devices.WithGeo(deviceGeo), devices.WithConnectivity(true))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, name := range rec.MetricNames() {
		if name == metricDevicesByCountry {
			continue
		}
		for _, p := range rec.MetricPoints(name) {
			for _, k := range []string{semconv.DeviceGeoCountryISO, semconv.DeviceGeoContinentCode} {
				if _, ok := p.Attrs[k]; ok {
					t.Errorf("metric %s carries %s: %v", name, k, p.Attrs)
				}
			}
		}
	}
}
