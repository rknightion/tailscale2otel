package coordination

import (
	"context"
	"errors"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"
)

func TestNewStartsAsStandbyAndReportsConfiguration(t *testing.T) {
	c, err := New(Options{
		Client:        fake.NewSimpleClientset(),
		Identity:      "pod-a",
		LeaseName:     "tailscale2otel",
		Namespace:     "default",
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 10 * time.Second,
		RetryPeriod:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := c.Status()
	if got.State != StateStandby || got.Identity != "pod-a" || got.LeaseName != "tailscale2otel" {
		t.Fatalf("initial status = %#v, want standby pod-a/tailscale2otel", got)
	}
}

func TestNewRejectsInvalidElectionOptions(t *testing.T) {
	_, err := New(Options{Client: fake.NewSimpleClientset(), Identity: "pod-a", LeaseName: "lease", Namespace: "default", LeaseDuration: 10 * time.Second, RenewDeadline: 10 * time.Second, RetryPeriod: time.Second})
	if err == nil || !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("New invalid timings error = %v, want ErrInvalidOptions", err)
	}
}

func TestRunCanceledBeforeElectionDoesNotReportDemotion(t *testing.T) {
	c, err := New(Options{
		Client:        fake.NewSimpleClientset(),
		Identity:      "pod-a",
		LeaseName:     "tailscale2otel",
		Namespace:     "default",
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 10 * time.Second,
		RetryPeriod:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Run(ctx, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Run canceled context = %v, want nil", err)
	}
	if got := c.Status().State; got != StateStopped {
		t.Fatalf("status after shutdown = %q, want %q", got, StateStopped)
	}
}

func TestRunWaitsForActiveCallbackShutdown(t *testing.T) {
	c, err := New(Options{
		Client:        fake.NewSimpleClientset(),
		Identity:      "pod-a",
		LeaseName:     "tailscale2otel",
		Namespace:     "default",
		LeaseDuration: 300 * time.Millisecond,
		RenewDeadline: 200 * time.Millisecond,
		RetryPeriod:   50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	draining := make(chan struct{})
	release := make(chan struct{})
	returned := make(chan error, 1)
	go func() {
		returned <- c.Run(ctx, func(activeCtx context.Context) error {
			close(started)
			<-activeCtx.Done()
			close(draining)
			<-release
			return nil
		})
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("coordinator never acquired the test Lease")
	}
	cancel()
	select {
	case <-draining:
	case <-time.After(2 * time.Second):
		t.Fatal("active callback did not begin shutdown")
	}
	select {
	case err := <-returned:
		close(release)
		t.Fatalf("Run returned before active shutdown completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Run after active shutdown = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after active shutdown completed")
	}
}

func TestRunStopsElectionAndPropagatesActiveCallbackError(t *testing.T) {
	c, err := New(Options{
		Client:        fake.NewSimpleClientset(),
		Identity:      "pod-a",
		LeaseName:     "tailscale2otel",
		Namespace:     "default",
		LeaseDuration: 300 * time.Millisecond,
		RenewDeadline: 200 * time.Millisecond,
		RetryPeriod:   50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	want := errors.New("active runtime failed")
	started := make(chan struct{})
	returned := make(chan error, 1)
	go func() {
		returned <- c.Run(ctx, func(context.Context) error {
			close(started)
			return want
		})
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("coordinator never acquired the test Lease")
	}
	select {
	case err := <-returned:
		if !errors.Is(err, want) {
			t.Fatalf("Run error = %v, want active callback error", err)
		}
	case <-time.After(200 * time.Millisecond):
		cancel()
		<-returned
		t.Fatal("Run kept renewing the Lease after the active callback returned")
	}
}
