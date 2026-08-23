package credreload

import (
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

// TestPoller_PicksUpRotationOnTick uses the Go 1.27 fake clock (synctest) so
// the bounded poller's interval advances deterministically instead of the
// test sleeping in real time.
func TestPoller_PicksUpRotationOnTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "token")
		writeFile(t, path, "token-v1")

		r, err := New(Options{Sources: Sources{BearerTokenFile: path}, Interval: time.Minute})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer r.Stop()

		r.Start()
		synctest.Wait()

		future := time.Now().Add(time.Second)
		writeFile(t, path, "token-v2")
		if err := os.Chtimes(path, future, future); err != nil {
			t.Fatalf("chtimes: %v", err)
		}

		// Before the tick fires, the poller must not have picked it up yet.
		if got := r.Headers()["Authorization"]; got != "Bearer token-v1" {
			t.Fatalf("before tick: Authorization = %q, want unchanged v1", got)
		}

		time.Sleep(time.Minute)
		synctest.Wait()

		if got := r.Headers()["Authorization"]; got != "Bearer token-v2" {
			t.Errorf("after tick: Authorization = %q, want %q", got, "Bearer token-v2")
		}
	})
}

// TestPoller_ZeroIntervalNeverStarts proves Start() is a no-op when Interval
// <= 0, i.e. explicit-Reload()-only mode never spins up a background
// goroutine at all.
func TestPoller_ZeroIntervalNeverStarts(t *testing.T) {
	r, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.Start()
	// Stop() must also be a safe no-op: there is no stopCh/doneCh to close.
	r.Stop()
	r.Stop() // idempotent
}

// TestPoller_StartStopIdempotent proves Start/Stop tolerate being called
// more than once without panicking or double-closing a channel.
func TestPoller_StartStopIdempotent(t *testing.T) {
	r, err := New(Options{Interval: time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.Start()
	r.Start() // second Start before Stop must be a no-op, not a second goroutine
	r.Stop()
	r.Stop() // second Stop must be a no-op, not a panic on double-close
}
