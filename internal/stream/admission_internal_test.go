package stream

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

// TestAcquireAdmission_CanceledBeforeAcquireLeavesSlotAvailable catches a
// canceled request taking an otherwise available admission slot. The full-slot
// cancellation path already waits on ctx.Done; this case pins the initial
// non-blocking send, where an already-canceled context must be rejected first.
func TestAcquireAdmission_CanceledBeforeAcquireLeavesSlotAvailable(t *testing.T) {
	admit := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	release, ok := acquireAdmission(ctx, admit)
	if ok {
		release()
		t.Fatal("already-canceled request acquired an admission slot")
	}
	select {
	case admit <- struct{}{}:
	default:
		t.Fatal("already-canceled request consumed the available admission slot")
	}
}

type cancelAfterAdmissionContext struct {
	context.Context
	done      chan struct{}
	release   func()
	errCalls  atomic.Int32
	doneCalls atomic.Int32
	closeOnce sync.Once
}

func (c *cancelAfterAdmissionContext) Err() error {
	if c.errCalls.Add(1) == 1 {
		return nil
	}
	c.closeOnce.Do(func() { close(c.done) })
	return context.Canceled
}

func (c *cancelAfterAdmissionContext) Done() <-chan struct{} {
	if c.release != nil && c.doneCalls.Add(1) == 1 {
		c.release()
	}
	return c.done
}

// TestAcquireAdmission_RechecksContextAfterSuccessfulSend catches a
// cancellation racing either successful channel send. The context stays open
// while the send wins, then reports cancellation on the required post-send
// check; both cases must release the slot and return false.
func TestAcquireAdmission_RechecksContextAfterSuccessfulSend(t *testing.T) {
	for _, tc := range []struct {
		name string
		full bool
	}{
		{name: "nonblocking send", full: false},
		{name: "timed send", full: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			admit := make(chan struct{}, 1)
			var releaseBlocker func()
			if tc.full {
				admit <- struct{}{}
				releaseBlocker = func() { <-admit }
			}
			ctx := &cancelAfterAdmissionContext{
				Context: context.Background(),
				done:    make(chan struct{}),
				release: releaseBlocker,
			}

			release, ok := acquireAdmission(ctx, admit)
			if ok {
				release()
				t.Fatal("canceled request acquired an admission slot")
			}
			select {
			case admit <- struct{}{}:
			default:
				t.Fatal("canceled request left the admission slot occupied")
			}
		})
	}
}
