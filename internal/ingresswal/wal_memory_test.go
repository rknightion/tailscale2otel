package ingresswal

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// metadataPathAllocCeiling bounds what one metadata-only operation may allocate
// for a near-limit entry. It is far below the body, so hydrating fails it.
const metadataPathAllocCeiling = 2 << 20

func allocatedBytes(t *testing.T, run func() error) uint64 {
	t.Helper()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	err := run()
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatalf("metadata path: %v", err)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("allocated %d bytes", allocated)
	return allocated
}

// Deliberately not parallel: TotalAlloc is process-wide.
func TestMetadataPathsDoNotHydrateNearLimitBody(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir() + "/wal"
	options := Options{Directory: directory, MaxBytes: 128 << 20, MaxEntries: 4}
	store, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	body := make([]byte, maxBodyBytes-1024)
	for i := range body {
		body[i] = byte(i * 31)
	}
	id, err := NewID("tailnet", "stream", "flow", body)
	if err != nil {
		t.Fatal(err)
	}
	accepted := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	envelope := Envelope{ID: id, Tailnet: "tailnet", Source: "stream", Signal: "flow", Accepted: accepted, Body: body}
	if err := store.Append(ctx, envelope); err != nil {
		t.Fatal(err)
	}
	encoded := store.Health().PendingBytes

	if got := allocatedBytes(t, func() error { return store.Append(ctx, envelope) }); got > metadataPathAllocCeiling {
		t.Errorf("duplicate append allocated %d bytes, ceiling %d", got, metadataPathAllocCeiling)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var reopened *Store
	got := allocatedBytes(t, func() error {
		var openErr error
		reopened, openErr = New(options)
		return openErr
	})
	if got > metadataPathAllocCeiling {
		t.Errorf("load allocated %d bytes, ceiling %d", got, metadataPathAllocCeiling)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if health := reopened.Health(); health.PendingEntries != 1 || health.PendingBytes != encoded {
		t.Fatalf("reopened health = %+v, want one entry of %d bytes", health, encoded)
	}

	var expired []ExpiredEntry
	got = allocatedBytes(t, func() error {
		var expireErr error
		expired, expireErr = reopened.ExpireBefore(ctx, accepted.Add(time.Hour), nil)
		return expireErr
	})
	if got > metadataPathAllocCeiling {
		t.Errorf("expiry allocated %d bytes, ceiling %d", got, metadataPathAllocCeiling)
	}
	if len(expired) != 1 || expired[0].EncodedBytes != encoded || !expired[0].Accepted.Equal(accepted) ||
		expired[0].Tailnet != "tailnet" || expired[0].Source != "stream" || expired[0].Signal != "flow" {
		t.Fatalf("expired = %+v, want the one entry of %d bytes", expired, encoded)
	}
}
