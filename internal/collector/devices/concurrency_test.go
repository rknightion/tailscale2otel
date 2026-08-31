package devices_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/collector/devices"
	"github.com/rknightion/tailscale2otel/v4/internal/enrich"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

// blockingSubrequestAPI holds every per-device request until release is closed.
// That makes the pool bound observable without relying on wall-clock timing.
type blockingSubrequestAPI struct {
	devices []tsapi.RichDevice
	release chan struct{}
	started chan struct{}
	active  atomic.Int64
	max     atomic.Int64
}

func (a *blockingSubrequestAPI) DevicesRich(context.Context) ([]tsapi.RichDevice, error) {
	return a.devices, nil
}

func (a *blockingSubrequestAPI) DevicePostureAttributes(context.Context, string) (tsapi.DeviceAttributes, error) {
	a.enter()
	return tsapi.DeviceAttributes{}, nil
}

func (a *blockingSubrequestAPI) DeviceInvites(context.Context, string) ([]tsapi.DeviceInvite, error) {
	a.enter()
	return nil, nil
}

func (a *blockingSubrequestAPI) enter() {
	active := a.active.Add(1)
	for {
		old := a.max.Load()
		if active <= old || a.max.CompareAndSwap(old, active) {
			break
		}
	}
	a.started <- struct{}{}
	<-a.release
	a.active.Add(-1)
}

var _ interface {
	DevicesRich(context.Context) ([]tsapi.RichDevice, error)
	DevicePostureAttributes(context.Context, string) (tsapi.DeviceAttributes, error)
	DeviceInvites(context.Context, string) ([]tsapi.DeviceInvite, error)
} = (*blockingSubrequestAPI)(nil)

func TestCollect_SubrequestsRespectConfiguredConcurrency(t *testing.T) {
	const concurrency = 2
	api := &blockingSubrequestAPI{
		devices: []tsapi.RichDevice{{ID: "d1"}, {ID: "d2"}, {ID: "d3"}},
		release: make(chan struct{}),
		started: make(chan struct{}, 16),
	}
	c := devices.New(api, enrich.NewDeviceCache(), time.Minute, false, true,
		devices.WithPostureLogMode("off"),
		devices.WithSubrequestConcurrency(concurrency))
	done := make(chan error, 1)
	go func() { done <- c.Collect(context.Background(), telemetrytest.New().Emitter()) }()

	for range concurrency {
		select {
		case <-api.started:
		case <-time.After(time.Second):
			t.Fatal("bounded worker pool did not start its configured workers")
		}
	}
	select {
	case <-api.started:
		t.Fatal("device posture requests exceeded configured concurrency")
	default:
	}
	close(api.release)
	if err := <-done; err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if got := api.max.Load(); got > concurrency {
		t.Fatalf("maximum concurrent subrequests = %d, want <= %d", got, concurrency)
	}
}

func TestCollect_SubrequestDefaultRemainsSequential(t *testing.T) {
	api := &blockingSubrequestAPI{
		devices: []tsapi.RichDevice{{ID: "d1"}, {ID: "d2"}},
		release: make(chan struct{}),
		started: make(chan struct{}, 8),
	}
	c := devices.New(api, enrich.NewDeviceCache(), time.Minute, false, true,
		devices.WithPostureLogMode("off"))
	done := make(chan error, 1)
	go func() { done <- c.Collect(context.Background(), telemetrytest.New().Emitter()) }()

	select {
	case <-api.started:
	case <-time.After(time.Second):
		t.Fatal("default posture request did not start")
	}
	select {
	case <-api.started:
		t.Fatal("default subrequest concurrency is no longer sequential")
	case <-time.After(50 * time.Millisecond):
	}
	close(api.release)
	if err := <-done; err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if got := api.max.Load(); got != 1 {
		t.Fatalf("maximum concurrent subrequests = %d, want 1", got)
	}
}
