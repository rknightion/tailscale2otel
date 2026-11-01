package devices_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/collector/devices"
	"github.com/rknightion/tailscale2otel/v5/internal/enrich"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry/pii"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
)

func statusFixture() []tsapi.RichDevice {
	return []tsapi.RichDevice{
		{ID: "synthetic-1", Hostname: "synthetic-one", PostureStatus: map[string]tsapi.PostureStatus{
			"posture:managed":     {Passing: true},
			"posture:recommended": {FailingAssertions: []string{"synthetic assertion"}},
			"posture:required":    {Impacting: true, FailingAssertions: []string{"private value", "other assertion"}},
		}},
		{ID: "synthetic-2", PostureStatus: map[string]tsapi.PostureStatus{"posture:required": {Passing: true}}},
		{ID: "synthetic-3"},
	}
}

func TestPostureStatusSignals(t *testing.T) {
	for _, perDevice := range []bool{true, false} {
		t.Run(fmt.Sprint(perDevice), func(t *testing.T) {
			api := &fakeAPI{devices: statusFixture()}
			c := devices.New(api, enrich.NewDeviceCache(), 0, false, false, devices.WithPerEntity(perDevice))
			rec := telemetrytest.New()
			if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
				t.Fatal(err)
			}
			for _, metric := range []string{"passing", "impacting"} {
				points := rec.MetricPoints("tailscale.device.posture." + metric)
				want := 0
				if perDevice {
					want = 4
				}
				if len(points) != want {
					t.Fatalf("%s points=%d want %d", metric, len(points), want)
				}
				for _, p := range points {
					if p.Attrs["host.id"] == "synthetic-1" && p.Attrs["tailscale.posture.name"] == "posture:required" {
						wantValue := float64(0)
						if metric == "impacting" {
							wantValue = 1
						}
						if p.Value != wantValue {
							t.Errorf("%s=%v want %v", metric, p.Value, wantValue)
						}
					}
				}
			}
			counts := rec.MetricPoints("tailscale.devices.posture.failed")
			if len(counts) != 6 {
				t.Fatalf("rollup points=%d want 6", len(counts))
			}
			for _, p := range counts {
				want := float64(0)
				if (p.Attrs["tailscale.posture.name"] == "posture:required" && p.Attrs["tailscale.posture.impacting"] == "true") || (p.Attrs["tailscale.posture.name"] == "posture:recommended" && p.Attrs["tailscale.posture.impacting"] == "false") {
					want = 1
				}
				if p.Value != want {
					t.Errorf("rollup %+v want %v", p, want)
				}
				if len(p.Attrs) != 2 {
					t.Errorf("rollup leaked device/assertion labels: %+v", p.Attrs)
				}
			}
			for _, name := range rec.MetricNames() {
				for _, p := range rec.MetricPoints(name) {
					for _, v := range p.Attrs {
						if strings.Contains(v, "private value") {
							t.Fatal("assertion leaked into metric")
						}
					}
				}
			}
			telemetrytest.AssertCatalogAttrs(t, rec, devices.Catalog(), devices.LogCatalog())
		})
	}
}

func TestPostureStatusChangesAndRecovery(t *testing.T) {
	api := &fakeAPI{devices: statusFixture()}
	c := devices.New(api, enrich.NewDeviceCache(), 0, false, false)
	count := 0
	poll := func(want int) {
		t.Helper()
		rec := telemetrytest.New()
		if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
			t.Fatal(err)
		}
		for _, l := range rec.LogRecords() {
			if l.EventName == "tailscale.device.posture.failing_assertions" {
				count++
				if !strings.Contains(l.Attrs["tailscale.device.posture.details"], "failingAssertions") {
					t.Error("missing assertion details")
				}
			}
		}
		if count != want {
			t.Fatalf("change logs=%d want %d", count, want)
		}
	}
	poll(1)
	poll(1)
	v := api.devices[0].PostureStatus["posture:required"]
	v.FailingAssertions = []string{"other assertion", "private value"}
	api.devices[0].PostureStatus["posture:required"] = v
	poll(1) // order is not a change
	v.FailingAssertions = []string{"changed private value"}
	api.devices[0].PostureStatus["posture:required"] = v
	poll(2)
	api.err = errors.New("temporary failure")
	if err := c.Collect(context.Background(), telemetrytest.New().Emitter()); err == nil {
		t.Fatal("wanted failure")
	}
	api.err = nil
	poll(2)
	original := api.devices
	api.devices = nil
	poll(2)
	api.devices = original
	poll(3)
	for name, v := range api.devices[0].PostureStatus {
		v.Passing = true
		v.FailingAssertions = nil
		api.devices[0].PostureStatus[name] = v
	}
	poll(3)
	v = api.devices[0].PostureStatus["posture:required"]
	v.Passing = false
	v.FailingAssertions = []string{"new failure"}
	api.devices[0].PostureStatus["posture:required"] = v
	poll(4)
}

func TestPostureStatusCapAndPrivacy(t *testing.T) {
	api := &fakeAPI{devices: []tsapi.RichDevice{{ID: "synthetic", PostureStatus: map[string]tsapi.PostureStatus{}}}}
	for i := 0; i < 60; i++ {
		api.devices[0].PostureStatus[fmt.Sprintf("posture:%02d", i)] = tsapi.PostureStatus{Impacting: true, FailingAssertions: []string{"sensitive synthetic assertion"}}
	}
	for _, off := range []bool{false, true} {
		opts := []devices.Option{devices.WithPerEntity(false)}
		if off {
			opts = append(opts, devices.WithPostureLogMode("off"))
		}
		c := devices.New(api, enrich.NewDeviceCache(), 0, false, false, opts...)
		rec := telemetrytest.NewWithPII(pii.Categories{pii.CatFreeTextDetails: false})
		if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
			t.Fatal(err)
		}
		if p := rec.MetricPoints("tailscale.devices.posture.names_dropped"); len(p) != 1 || p[0].Value != 10 {
			t.Fatalf("dropped=%+v", p)
		}
		points := rec.MetricPoints("tailscale.devices.posture.failed")
		if len(points) != 100 {
			t.Fatalf("cap not enforced: %d", len(points))
		}
		for _, p := range points {
			if p.Attrs["tailscale.posture.name"] >= "posture:50" {
				t.Fatal("nondeterministic selected names")
			}
		}
		logs := 0
		for _, l := range rec.LogRecords() {
			if l.EventName == "tailscale.device.posture.failing_assertions" {
				logs++
				if strings.Contains(fmt.Sprint(l), "sensitive synthetic assertion") {
					t.Fatal("PII filter leaked assertions")
				}
			}
		}
		want := 1
		if off {
			want = 0
		}
		if logs != want {
			t.Fatalf("logs=%d want %d", logs, want)
		}
	}
}
