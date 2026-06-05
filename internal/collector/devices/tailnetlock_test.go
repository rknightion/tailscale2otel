package devices_test

import (
	"context"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/collector/devices"
	"github.com/rknightion/tailscale2otel/internal/enrich"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

func byRegion(pts []telemetrytest.MetricPoint) map[string]float64 {
	out := map[string]float64{}
	for _, p := range pts {
		out[p.Attrs["tailscale.derp.region"]] = p.Value
	}
	return out
}

func TestTailnetLockErrors(t *testing.T) {
	api := &fakeAPI{devices: []tsapi.RichDevice{
		{ID: "1", Hostname: "h1", TailnetLockError: ""},
		{ID: "2", Hostname: "h2", TailnetLockError: "node is not signed"},
		{ID: "3", Hostname: "h3", TailnetLockError: "locked out"},
	}}
	rec := telemetrytest.New()
	c := devices.New(api, enrich.NewDeviceCache(), 0, false, false)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	errs := rec.MetricPoints("tailscale.tailnet_lock.errors")
	if len(errs) != 1 || errs[0].Value != 2 {
		t.Fatalf("tailnet_lock.errors = %+v, want one point value 2", errs)
	}

	logCount := 0
	for _, lr := range rec.LogRecords() {
		if lr.EventName != "tailscale.device.tailnet_lock_error" {
			continue
		}
		logCount++
		if lr.Body == "" {
			t.Errorf("tailnet_lock_error log has empty body")
		}
		if lr.Attrs["host.id"] == "" {
			t.Errorf("tailnet_lock_error log missing host.id: %+v", lr.Attrs)
		}
	}
	if logCount != 2 {
		t.Fatalf("tailnet_lock_error logs = %d, want 2", logCount)
	}
}

func TestTailnetLockErrorsZero(t *testing.T) {
	api := &fakeAPI{devices: []tsapi.RichDevice{{ID: "1", Hostname: "h1"}}}
	rec := telemetrytest.New()
	if err := devices.New(api, enrich.NewDeviceCache(), 0, false, false).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	errs := rec.MetricPoints("tailscale.tailnet_lock.errors")
	if len(errs) != 1 || errs[0].Value != 0 {
		t.Fatalf("tailnet_lock.errors = %+v, want value 0", errs)
	}
	for _, lr := range rec.LogRecords() {
		if lr.EventName == "tailscale.device.tailnet_lock_error" {
			t.Errorf("unexpected tailnet_lock_error log when no errors")
		}
	}
}

func TestDerpRegionRollup(t *testing.T) {
	api := &fakeAPI{devices: []tsapi.RichDevice{
		{ID: "1", Hostname: "h1", DERPLatency: map[string]tsapi.DERPRegion{
			"Frankfurt": {Preferred: true, LatencyMs: 12.0},
			"Amsterdam": {Preferred: false, LatencyMs: 8.0},
		}},
		{ID: "2", Hostname: "h2", DERPLatency: map[string]tsapi.DERPRegion{
			"Frankfurt": {Preferred: false, LatencyMs: 20.0},
		}},
	}}
	rec := telemetrytest.New()
	if err := devices.New(api, enrich.NewDeviceCache(), 0, false, false).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	dev := byRegion(rec.MetricPoints("tailscale.derp.region.devices"))
	if dev["Frankfurt"] != 2 || dev["Amsterdam"] != 1 {
		t.Fatalf("region devices = %v, want Frankfurt 2 / Amsterdam 1", dev)
	}
	pref := byRegion(rec.MetricPoints("tailscale.derp.region.preferred"))
	if pref["Frankfurt"] != 1 || pref["Amsterdam"] != 0 {
		t.Fatalf("region preferred = %v, want Frankfurt 1 / Amsterdam 0", pref)
	}
	min := rec.MetricPoints("tailscale.derp.region.latency_min")
	if len(min) == 0 || min[0].Unit != "s" {
		t.Fatalf("latency_min = %+v, want unit s", min)
	}
	byReg := byRegion(min)
	if byReg["Frankfurt"] != 12.0/1000 { // min(12,20)ms in seconds
		t.Errorf("Frankfurt latency_min = %v, want %v", byReg["Frankfurt"], 12.0/1000)
	}
	if byReg["Amsterdam"] != 8.0/1000 {
		t.Errorf("Amsterdam latency_min = %v, want %v", byReg["Amsterdam"], 8.0/1000)
	}
}

func TestDerpRegionRollupDisabled(t *testing.T) {
	api := &fakeAPI{devices: []tsapi.RichDevice{
		{ID: "1", Hostname: "h1", DERPLatency: map[string]tsapi.DERPRegion{"Frankfurt": {LatencyMs: 12.0}}},
	}}
	rec := telemetrytest.New()
	c := devices.New(api, enrich.NewDeviceCache(), 0, false, false, devices.WithDerpRegionRollup(false))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if pts := rec.MetricPoints("tailscale.derp.region.devices"); len(pts) != 0 {
		t.Fatalf("region rollup emitted when disabled: %d points", len(pts))
	}
}
