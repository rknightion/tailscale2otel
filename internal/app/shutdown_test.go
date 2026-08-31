package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/flowstore"
)

// shutdownTestFlowStore embeds the real in-memory implementation so the test
// only replaces Close, which is the lifecycle seam under test.
type shutdownTestFlowStore struct {
	*flowstore.Memory
	closeFn func() error
}

var _ flowstore.Store = (*shutdownTestFlowStore)(nil)

func newShutdownTestFlowStore(closeFn func() error) *shutdownTestFlowStore {
	return &shutdownTestFlowStore{Memory: flowstore.NewMemory(1), closeFn: closeFn}
}

func (s *shutdownTestFlowStore) Close() error { return s.closeFn() }

// TestCloseFlowStores_BlockedRuntimeDoesNotStarveOthers proves that one
// runtime's context-less Close cannot consume the opportunity for other
// runtimes to close. The blocked Close remains held beyond the shared test
// budget, then is released after the bounded wait has returned.
func TestCloseFlowStores_BlockedRuntimeDoesNotStarveOthers(t *testing.T) {
	slowStarted := make(chan struct{})
	slowRelease := make(chan struct{})
	slowDone := make(chan struct{})
	var releaseOnce sync.Once
	releaseSlow := func() { releaseOnce.Do(func() { close(slowRelease) }) }
	defer releaseSlow()

	slow := newShutdownTestFlowStore(func() error {
		close(slowStarted)
		<-slowRelease
		close(slowDone)
		return nil
	})
	fastClosed := make(chan struct{})
	fast := newShutdownTestFlowStore(func() error {
		close(fastClosed)
		return nil
	})
	a := &App{
		logger: slog.New(slog.DiscardHandler),
		runtimes: []*tailnetRuntime{
			{name: "slow", flowStore: slow},
			{name: "fast", flowStore: fast},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.closeFlowStores(ctx) }()

	select {
	case <-slowStarted:
	case <-time.After(time.Second):
		t.Fatal("blocked flow-store Close was not started")
	}
	select {
	case <-fastClosed:
	case <-time.After(time.Second):
		t.Fatal("fast runtime flow-store Close was starved by the blocked runtime")
	}

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("closeFlowStores() error = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("closeFlowStores() did not return at the shared deadline")
	}

	releaseSlow()
	select {
	case <-slowDone:
	case <-time.After(time.Second):
		t.Fatal("blocked flow-store Close did not finish after release")
	}
}

func TestAppClose_ClosesFlowStores(t *testing.T) {
	closed := make(chan struct{})
	store := newShutdownTestFlowStore(func() error {
		close(closed)
		return nil
	})
	a := &App{
		logger: slog.New(slog.DiscardHandler),
		runtimes: []*tailnetRuntime{
			{name: "tailnet", flowStore: store},
		},
		shutdown: func(context.Context) error { return nil },
	}

	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("App.Close() error = %v", err)
	}
	select {
	case <-closed:
	default:
		t.Fatal("App.Close() returned without closing the runtime flow store")
	}
}
