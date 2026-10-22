package ingresswal

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestExpireBeforeRetiresOnlyStaleEntriesDurably pins the replay-age policy at
// the storage seam: entries accepted before the cutoff are retired through the
// durable completion path (so they never replay after a restart), newer
// entries are untouched, and the caller gets each retired entry's route for
// loss accounting.
func TestExpireBeforeRetiresOnlyStaleEntriesDurably(t *testing.T) {
	t.Parallel()

	opts := Options{Directory: filepath.Join(t.TempDir(), "wal"), MaxBytes: 1 << 20, MaxEntries: 10}
	store := mustNew(t, opts)
	t0 := time.Date(2026, 10, 11, 8, 32, 0, 0, time.UTC)
	stale1 := testEnvelope(t, t0, "tail-a", "stream", "network", []byte("stale-1"))
	stale2 := testEnvelope(t, t0.Add(time.Hour), "tail-b", "webhook", "events", []byte("stale-2"))
	fresh := testEnvelope(t, t0.Add(10*time.Hour), "tail-a", "stream", "network", []byte("fresh"))
	for _, e := range []Envelope{stale1, stale2, fresh} {
		if err := store.Append(context.Background(), e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened := mustNew(t, opts)
	expired, err := reopened.ExpireBefore(context.Background(), t0.Add(2*time.Hour), nil)
	if err != nil {
		t.Fatalf("ExpireBefore: %v", err)
	}
	if len(expired) != 2 {
		t.Fatalf("expired = %+v, want 2 entries", expired)
	}
	if expired[0].Tailnet != "tail-a" || expired[0].Source != "stream" || expired[0].Signal != "network" ||
		!expired[0].Accepted.Equal(t0) || expired[0].EncodedBytes <= 0 {
		t.Errorf("expired[0] = %+v, want the oldest stream entry with its size", expired[0])
	}
	if expired[1].Tailnet != "tail-b" || expired[1].Source != "webhook" {
		t.Errorf("expired[1] = %+v, want the webhook entry", expired[1])
	}
	if h := reopened.Health(); h.PendingEntries != 1 {
		t.Fatalf("pending after expiry = %d, want 1", h.PendingEntries)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("Close reopened: %v", err)
	}

	again := mustNew(t, opts)
	var replayed []Envelope
	if err := again.Replay(context.Background(), func(_ context.Context, e Envelope) error {
		replayed = append(replayed, e)
		return nil
	}, nil); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(replayed) != 1 || replayed[0].ID != fresh.ID {
		t.Fatalf("replayed after restart = %+v, want only the fresh entry", replayed)
	}
}

// TestExpireBeforeSkipsPreparedAndZeroCutoff pins that expiry never retires an
// entry this process already holds in its replay window, and that a zero
// cutoff (policy disabled) retires nothing.
func TestExpireBeforeSkipsPreparedAndZeroCutoff(t *testing.T) {
	t.Parallel()

	store := mustNew(t, Options{Directory: filepath.Join(t.TempDir(), "wal"), MaxBytes: 1 << 20, MaxEntries: 10})
	t0 := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	held := testEnvelope(t, t0, "tail-a", "stream", "network", []byte("held"))
	if err := store.Append(context.Background(), held); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if expired, err := store.ExpireBefore(context.Background(), time.Time{}, nil); err != nil || len(expired) != 0 {
		t.Fatalf("zero cutoff expired = %+v, %v; want none", expired, err)
	}
	prepared, err := store.PrepareWindow(context.Background(), WindowLimits{MaxEntries: 4, MaxBytes: 1 << 20}, nil, nil)
	if err != nil || len(prepared) != 1 {
		t.Fatalf("PrepareWindow = %d, %v; want 1", len(prepared), err)
	}
	expired, err := store.ExpireBefore(context.Background(), t0.Add(time.Hour), nil)
	if err != nil {
		t.Fatalf("ExpireBefore: %v", err)
	}
	if len(expired) != 0 {
		t.Fatalf("expired a prepared entry: %+v", expired)
	}
	if h := store.Health(); h.PendingEntries != 1 {
		t.Fatalf("pending = %d, want the held entry kept", h.PendingEntries)
	}
}

// TestExpireBeforeKeepsIneligibleRoutes pins that an entry the caller cannot
// account for is left for replay (which fails closed on an unknown route)
// while a newer stale eligible entry is still retired.
func TestExpireBeforeKeepsIneligibleRoutes(t *testing.T) {
	t.Parallel()

	store := mustNew(t, Options{Directory: filepath.Join(t.TempDir(), "wal"), MaxBytes: 1 << 20, MaxEntries: 10})
	t0 := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	unknown := testEnvelope(t, t0, "tail-gone", "stream", "network", []byte("unknown"))
	known := testEnvelope(t, t0.Add(time.Minute), "tail-a", "stream", "network", []byte("known"))
	for _, e := range []Envelope{unknown, known} {
		if err := store.Append(context.Background(), e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	expired, err := store.ExpireBefore(context.Background(), t0.Add(time.Hour), func(tailnet, _, _ string) bool {
		return tailnet == "tail-a"
	})
	if err != nil {
		t.Fatalf("ExpireBefore: %v", err)
	}
	if len(expired) != 1 || expired[0].Tailnet != "tail-a" {
		t.Fatalf("expired = %+v, want only the eligible entry", expired)
	}
	if h := store.Health(); h.PendingEntries != 1 {
		t.Fatalf("pending = %d, want the ineligible entry kept", h.PendingEntries)
	}
}
