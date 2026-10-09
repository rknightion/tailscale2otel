package ingresswal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestPreparedWindowOriginalsAndBounds(t *testing.T) {
	ctx := context.Background()
	s := mustNew(t, Options{Directory: filepath.Join(t.TempDir(), "wal"), MaxBytes: 1 << 20, MaxEntries: 100})
	var originals []Envelope
	for i := range 70 {
		e := testEnvelope(t, time.Unix(int64(i+1), 0), "example.com", "stream", "hec", []byte{byte(i)})
		originals = append(originals, e)
		if err := s.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	before := s.Health()
	window, err := s.PrepareWindow(ctx, WindowLimits{MaxEntries: 100, MaxBytes: 1 << 20}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(window) != 64 {
		t.Fatalf("window=%d, want 64", len(window))
	}
	if got := s.Health(); !reflect.DeepEqual(got, before) {
		t.Fatalf("prepare committed state: %+v", got)
	}
	var held []Generation
	var bytes int64
	for i, entry := range window {
		if !reflect.DeepEqual(entry.Envelope, originals[i]) {
			t.Fatalf("original %d changed", i)
		}
		held = append(held, entry.Generation)
		bytes += entry.EncodedBytes
	}
	if bytes > s.opts.MaxBytes {
		t.Fatalf("window bytes=%d", bytes)
	}
	if next, err := s.PrepareWindow(ctx, WindowLimits{MaxEntries: 64, MaxBytes: 1 << 20}, held, nil); err != nil || len(next) != 0 {
		t.Fatalf("registration bound: %d %v", len(next), err)
	}
	// Later eligible generations can retire independently of the oldest one.
	if outcome, err := s.CommitPrepared(ctx, held[1]); err != nil || outcome != PreparedRetired {
		t.Fatalf("retire later: %v %v", outcome, err)
	}
	if err := s.ReleasePrepared(held[1]); err != nil {
		t.Fatal(err)
	}
	held = append(held[:1], held[2:]...)
	next, err := s.PrepareWindow(ctx, WindowLimits{MaxEntries: 1, MaxBytes: 1 << 20}, held, nil)
	if err != nil || len(next) != 1 || !reflect.DeepEqual(next[0].Envelope, originals[64]) {
		t.Fatalf("next original: %v %v", next, err)
	}
	if s.Health().PendingEntries != 69 {
		t.Fatal("wrong original retired")
	}
}

func TestPreparedGenerationAmbiguousCleanupAndSameID(t *testing.T) {
	for _, reappend := range []bool{false, true} {
		t.Run(map[bool]string{false: "absence", true: "newer_same_ID"}[reappend], func(t *testing.T) {
			ctx := context.Background()
			s := mustNew(t, Options{Directory: filepath.Join(t.TempDir(), "wal"), MaxBytes: 1 << 20, MaxEntries: 4})
			e := testEnvelope(t, time.Unix(1, 0), "example.com", "stream", "hec", []byte("original"))
			if err := s.Append(ctx, e); err != nil {
				t.Fatal(err)
			}
			window, err := s.PrepareWindow(ctx, WindowLimits{4, 1 << 20}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			g := window[0].Generation
			realLstat := s.ops.lstat
			injected := false
			s.ops.lstat = func(path string) (os.FileInfo, error) {
				if !injected && len(s.pending) == 0 && len(s.completed) == 0 {
					injected = true
					return nil, errors.New("final directory validation failure")
				}
				return realLstat(path)
			}
			outcome, err := s.CommitPrepared(ctx, g)
			if err == nil || outcome != PreparedPending || !injected {
				t.Fatalf("ambiguous result=%v err=%v injected=%v", outcome, err, injected)
			}
			if health := s.Health(); health.PendingEntries != 0 || health.PendingBytes != 0 || health.CompletionMarkers != 0 {
				t.Fatalf("cleanup not durably removed: %+v", health)
			}
			s.ops.lstat = realLstat
			if reappend {
				if err := s.Append(ctx, e); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				outcome, err = s.CommitPrepared(ctx, g)
				if err != nil || outcome != PreparedRetired {
					t.Fatalf("reconcile=%v %v", outcome, err)
				}
			}
			if reappend && s.Health().PendingEntries != 1 {
				t.Fatal("old token deleted newer generation")
			}
			if err := s.ReleasePrepared(g); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CommitPrepared(ctx, g); err == nil {
				t.Fatal("released token reused")
			}
			if err := s.ReleasePrepared(g); err == nil {
				t.Fatal("released token accepted twice")
			}
			if len(s.prepared) != 0 {
				t.Fatal("terminal registration quota not released")
			}
			if reappend {
				newer, err := s.PrepareWindow(ctx, WindowLimits{4, 1 << 20}, nil, nil)
				if err != nil || len(newer) != 1 || newer[0].Generation == g {
					t.Fatalf("new generation=%v %v", newer, err)
				}
			}
		})
	}
}

func TestPreparedTokenOwnershipAndMarkerRecovery(t *testing.T) {
	ctx := context.Background()
	a := mustNew(t, Options{Directory: filepath.Join(t.TempDir(), "a"), MaxBytes: 1 << 20, MaxEntries: 4})
	b := mustNew(t, Options{Directory: filepath.Join(t.TempDir(), "b"), MaxBytes: 1 << 20, MaxEntries: 4})
	e := testEnvelope(t, time.Unix(1, 0), "example.com", "stream", "hec", []byte("original"))
	if err := a.Append(ctx, e); err != nil {
		t.Fatal(err)
	}
	entries, err := a.PrepareWindow(ctx, WindowLimits{4, 1 << 20}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := entries[0].Generation
	for _, invalid := range []Generation{{}, g} {
		if _, err := b.CommitPrepared(ctx, invalid); err == nil {
			t.Fatal("foreign/unregistered token accepted")
		}
	}
	if err := a.ReleasePrepared(g); err == nil {
		t.Fatal("pending token released")
	}
	realRemove := a.ops.removeAt
	boom := errors.New("entry removal fault")
	a.ops.removeAt = func(d *os.File, name string) error {
		if name == entryName(g.sequence, g.id) {
			return boom
		}
		return realRemove(d, name)
	}
	if outcome, err := a.CommitPrepared(ctx, g); outcome != PreparedPending || !errors.Is(err, boom) {
		t.Fatalf("marker failure=%v %v", outcome, err)
	}
	if a.Health().CompletionMarkers != 1 {
		t.Fatal("completion marker not retained")
	}
	a.ops.removeAt = realRemove
	var observed []Generation
	result, err := a.PrepareWindow(ctx, WindowLimits{4, 1 << 20}, []Generation{g}, func(done Generation) { observed = append(observed, done); _ = a.Health() })
	if err != nil || len(result) != 0 || !reflect.DeepEqual(observed, []Generation{g}) {
		t.Fatalf("generation cleanup=%v %v %v", result, observed, err)
	}
	if err := a.ReleasePrepared(g); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedWindowByteAndNondurableLimits(t *testing.T) {
	ctx := context.Background()
	s := mustNew(t, Options{Directory: filepath.Join(t.TempDir(), "wal"), MaxBytes: 1 << 20, MaxEntries: 4})
	e := testEnvelope(t, time.Unix(1, 0), "example.com", "stream", "hec", []byte("original"))
	if err := s.Append(ctx, e); err != nil {
		t.Fatal(err)
	}
	size := s.Health().PendingBytes
	if entries, err := s.PrepareWindow(ctx, WindowLimits{4, size - 1}, nil, nil); err != nil || len(entries) != 0 {
		t.Fatalf("byte bound=%v %v", entries, err)
	}
	pending := s.pending[e.ID]
	pending.durable = false
	s.pending[e.ID] = pending
	if entries, err := s.PrepareWindow(ctx, WindowLimits{4, 1 << 20}, nil, nil); err != nil || len(entries) != 0 {
		t.Fatalf("visible nondurable selected=%v %v", entries, err)
	}
	pending.durable = true
	s.pending[e.ID] = pending
	entries, err := s.PrepareWindow(ctx, WindowLimits{4, size}, nil, nil)
	if err != nil || len(entries) != 1 || entries[0].EncodedBytes != size {
		t.Fatalf("lone historical entry=%v %v", entries, err)
	}
}

// An entry skipped because it does not fit the remaining byte quota must not
// consume one of the 64 new-registration slots: the window still fills with
// 64 later originals that do fit.
func TestPreparedWindowSkippedEntryKeepsRegistrationQuota(t *testing.T) {
	ctx := context.Background()
	s := mustNew(t, Options{Directory: filepath.Join(t.TempDir(), "wal"), MaxBytes: 1 << 20, MaxEntries: 100})
	big := testEnvelope(t, time.Unix(1, 0), "example.com", "stream", "hec", make([]byte, 64<<10))
	if err := s.Append(ctx, big); err != nil {
		t.Fatal(err)
	}
	var small []Envelope
	for i := range 70 {
		e := testEnvelope(t, time.Unix(int64(i+2), 0), "example.com", "stream", "hec", []byte{byte(i)})
		small = append(small, e)
		if err := s.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	// Room for every small original but never the big one.
	window, err := s.PrepareWindow(ctx, WindowLimits{MaxEntries: 100, MaxBytes: 32 << 10}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(window) != 64 {
		t.Fatalf("window=%d originals, want 64: the skipped big original consumed a registration slot", len(window))
	}
	for i, entry := range window {
		if !reflect.DeepEqual(entry.Envelope, small[i]) {
			t.Fatalf("window[%d] is not small original %d", i, i)
		}
	}
}
