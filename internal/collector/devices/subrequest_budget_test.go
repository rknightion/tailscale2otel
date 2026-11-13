package devices_test

import (
	"context"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/collector/devices"
	"github.com/rknightion/tailscale2otel/v5/internal/enrich"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
)

type budgetAPI struct {
	devices  []tsapi.RichDevice
	invite   func(context.Context, string, int) ([]tsapi.DeviceInvite, error)
	invites  atomic.Int64
	postures atomic.Int64
	lists    int
}

func (a *budgetAPI) DevicesRich(context.Context) ([]tsapi.RichDevice, error) {
	a.lists++
	return a.devices, nil
}
func (a *budgetAPI) DevicePostureAttributes(context.Context, string) (tsapi.DeviceAttributes, error) {
	a.postures.Add(1)
	return tsapi.DeviceAttributes{Attributes: map[string]any{"custom:ready": true}}, nil
}
func (a *budgetAPI) DeviceInvites(ctx context.Context, id string) ([]tsapi.DeviceInvite, error) {
	n := a.invites.Add(1)
	return a.invite(ctx, id, int(n))
}
func budgetDevices(n int) []tsapi.RichDevice {
	out := make([]tsapi.RichDevice, n)
	for i := range out {
		out[i] = tsapi.RichDevice{ID: fmt.Sprintf("example-device-%d", i), Hostname: fmt.Sprintf("example-host-%d", i), ConnectedToControl: true}
	}
	return out
}
func TestDeviceSnapshotPublishedWhenSubrequestsStall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := &budgetAPI{devices: budgetDevices(2), invite: func(ctx context.Context, _ string, _ int) ([]tsapi.DeviceInvite, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}}
		c := devices.New(api, enrich.NewDeviceCache(), time.Second, false, false, devices.WithDeviceInvites(true))
		rec := telemetrytest.New()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		started := time.Now()
		if err := c.Collect(ctx, rec.Emitter()); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Errorf("collect elapsed %s, want at most interval", elapsed)
		}
		if points := rec.MetricPoints("tailscale.device.online"); len(points) != 2 {
			t.Errorf("online snapshot = %v", points)
		}
	})
}
func TestSubrequestsStopAfterSystemicDenial(t *testing.T) {
	for _, tc := range []struct {
		name          string
		workers       int
		singleFailure bool
		mixed         bool
	}{{"scope", 1, false, false}, {"concurrent", 4, false, false}, {"one_failure", 1, true, false}, {"different_denials", 1, false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			api := &budgetAPI{devices: budgetDevices(20)}
			api.invite = func(_ context.Context, _ string, n int) ([]tsapi.DeviceInvite, error) {
				if tc.singleFailure && n > 1 {
					return nil, nil
				}
				code := 403
				if tc.mixed && n%2 == 0 {
					code = 401
				}
				return nil, &tsapi.StatusError{Code: code}
			}
			c := devices.New(api, enrich.NewDeviceCache(), time.Second, false, true, devices.WithDeviceInvites(true), devices.WithSubrequestConcurrency(tc.workers))
			if err := c.Collect(context.Background(), telemetrytest.New().Emitter()); err != nil {
				t.Fatal(err)
			}
			if n := api.invites.Load(); tc.singleFailure || tc.mixed {
				if n != 20 {
					t.Errorf("attempts = %d, want all 20", n)
				}
			} else if n > int64(3+tc.workers-1) {
				t.Errorf("attempts = %d, want at most %d", n, 3+tc.workers-1)
			}
			if n := api.postures.Load(); n != 20 {
				t.Errorf("independent successful posture attempts = %d", n)
			}
		})
	}
	t.Run("disabled", func(t *testing.T) {
		api := &budgetAPI{devices: budgetDevices(2)}
		c := devices.New(api, enrich.NewDeviceCache(), time.Second, false, false)
		if err := c.Collect(context.Background(), telemetrytest.New().Emitter()); err != nil {
			t.Fatal(err)
		}
		if api.lists != 1 || api.invites.Load() != 0 || api.postures.Load() != 0 {
			t.Fatal("disabled subrequests made extra API calls")
		}
	})
}
func TestPartialSubrequestPhaseKeepsPreviousPostureSnapshot(t *testing.T) {
	api := &budgetAPI{devices: budgetDevices(2), invite: func(context.Context, string, int) ([]tsapi.DeviceInvite, error) {
		return []tsapi.DeviceInvite{{Accepted: true}}, nil
	}}
	c := devices.New(api, enrich.NewDeviceCache(), time.Second, false, true, devices.WithDeviceInvites(true), devices.WithAttributeNamespaces([]string{"custom"}))
	rec := telemetrytest.New()
	collect := func() {
		t.Helper()
		if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func(name string) map[string]telemetrytest.MetricPoint {
		out := map[string]telemetrytest.MetricPoint{}
		for _, p := range rec.MetricPoints(name) {
			out[fmt.Sprint(p.Attrs)] = p
		}
		return out
	}
	collect()
	names := []string{"tailscale.device.posture", "tailscale.device.attribute", "tailscale.device_invites.count"}
	before := map[string]map[string]telemetrytest.MetricPoint{}
	for _, name := range names {
		before[name] = snapshot(name)
		if len(before[name]) == 0 {
			t.Fatalf("missing initial %s", name)
		}
	}
	api.devices = budgetDevices(10)
	api.invite = func(context.Context, string, int) ([]tsapi.DeviceInvite, error) {
		return nil, &tsapi.StatusError{Code: 403}
	}
	collect()
	for _, name := range names {
		if !reflect.DeepEqual(snapshot(name), before[name]) {
			t.Errorf("partial pass replaced %s", name)
		}
	}
	api.devices = budgetDevices(1)
	api.invite = func(context.Context, string, int) ([]tsapi.DeviceInvite, error) { return nil, nil }
	collect()
	if got := rec.MetricPoints(names[0]); len(got) != 1 {
		t.Errorf("complete posture snapshot = %v", got)
	}
	if got := rec.MetricPoints(names[2]); len(got) != 0 {
		t.Errorf("complete empty invite snapshot = %v", got)
	}
}

func TestPostureComplianceGaugeDropsWithoutResults(t *testing.T) {
	api := &budgetAPI{devices: budgetDevices(1)}
	c := devices.New(api, enrich.NewDeviceCache(), time.Second, false, true, devices.WithPostureComplianceChecks([]devices.PostureComplianceCheck{{Name: "example-check", Attribute: "custom:ready", Equals: "false"}}))
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatal(err)
	}
	const name = "tailscale.devices.posture_compliance.failed"
	if points := rec.MetricPoints(name); len(points) != 1 || points[0].Value != 1 {
		t.Fatalf("initial compliance = %v", points)
	}
	api.devices = nil
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatal(err)
	}
	if points := rec.MetricPoints(name); len(points) != 0 {
		t.Errorf("compliance without results = %v", points)
	}
}
