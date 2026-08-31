package services

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

// blockingHostsAPI holds each ServiceHosts call until release is closed. The
// fake is intentionally package-local so the test can exercise the unexported
// narrow API seam without adding production surface.
type blockingHostsAPI struct {
	services []tsapi.VIPService
	release  chan struct{}
	started  chan struct{}
	active   atomic.Int64
	max      atomic.Int64
}

func (a *blockingHostsAPI) Services(context.Context) ([]tsapi.VIPService, error) {
	return a.services, nil
}

func (a *blockingHostsAPI) ServiceHosts(context.Context, string) ([]tsapi.ServiceHost, error) {
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
	return nil, nil
}

func (a *blockingHostsAPI) ServiceAddrs(context.Context) ([]tsapi.ServiceAddr, error) {
	return nil, nil
}

var _ api = (*blockingHostsAPI)(nil)

func TestCollect_HostSubrequestsRespectConfiguredConcurrency(t *testing.T) {
	const concurrency = 2
	api := &blockingHostsAPI{
		services: []tsapi.VIPService{{Name: "s1"}, {Name: "s2"}, {Name: "s3"}},
		release:  make(chan struct{}),
		started:  make(chan struct{}, 8),
	}
	c := New(api, time.Minute, WithCollectHosts(true), WithSubrequestConcurrency(concurrency))
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
		t.Fatal("service host requests exceeded configured concurrency")
	default:
	}
	close(api.release)
	if err := <-done; err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if got := api.max.Load(); got > concurrency {
		t.Fatalf("maximum concurrent host requests = %d, want <= %d", got, concurrency)
	}
}

func TestCollect_HostSubrequestDefaultRemainsSequential(t *testing.T) {
	api := &blockingHostsAPI{
		services: []tsapi.VIPService{{Name: "s1"}, {Name: "s2"}},
		release:  make(chan struct{}),
		started:  make(chan struct{}, 8),
	}
	c := New(api, time.Minute, WithCollectHosts(true))
	done := make(chan error, 1)
	go func() { done <- c.Collect(context.Background(), telemetrytest.New().Emitter()) }()

	select {
	case <-api.started:
	case <-time.After(time.Second):
		t.Fatal("default host request did not start")
	}
	select {
	case <-api.started:
		t.Fatal("default host subrequest concurrency is no longer sequential")
	case <-time.After(50 * time.Millisecond):
	}
	close(api.release)
	if err := <-done; err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if got := api.max.Load(); got != 1 {
		t.Fatalf("maximum concurrent host requests = %d, want 1", got)
	}
}
