package app

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestRuntimeHistory_SampleComputesGCRate(t *testing.T) {
	h := newRuntimeHistory(10)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// First sample: no prior, so GC rate is 0.
	h.sample(t0, runtimeStats{goroutines: 5, heapAlloc: 1000, numGC: 10}, 100, map[string]int{"m": 3})
	if g := h.gcRate.Values(); len(g) != 1 || g[0] != 0 {
		t.Fatalf("first gcRate = %v, want [0]", g)
	}

	// 10s later, NumGC advanced by 5 -> 0.5 cycles/sec.
	h.sample(t0.Add(10*time.Second), runtimeStats{goroutines: 6, heapAlloc: 2000, numGC: 15}, 110, map[string]int{"m": 4})
	if g := h.gcRate.Values(); len(g) != 2 || g[1] != 0.5 {
		t.Fatalf("gcRate = %v, want second 0.5", g)
	}
	if gr := h.goroutines.Values(); len(gr) != 2 || gr[0] != 5 || gr[1] != 6 {
		t.Fatalf("goroutines = %v, want [5 6]", gr)
	}
	if ha := h.heapAlloc.Values(); len(ha) != 2 || ha[0] != 1000 || ha[1] != 2000 {
		t.Fatalf("heapAlloc = %v, want [1000 2000]", ha)
	}
	if ct := h.cardTotal.Values(); len(ct) != 2 || ct[0] != 100 || ct[1] != 110 {
		t.Fatalf("cardTotal = %v, want [100 110]", ct)
	}
}

func TestRuntimeHistory_SampleHandlesNumGCWrap(t *testing.T) {
	h := newRuntimeHistory(10)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h.sample(t0, runtimeStats{numGC: 100}, 0, nil)
	h.sample(t0.Add(time.Second), runtimeStats{numGC: 50}, 0, nil) // counter went backwards
	if g := h.gcRate.Values(); len(g) != 2 || g[1] != 0 {
		t.Fatalf("gcRate after wrap = %v, want second 0 (no negative rate)", g)
	}
}

func TestRunSampler_SamplesOnStartAndTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newRuntimeHistory(60)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		read := func() runtimeStats { return runtimeStats{goroutines: 7} }
		card := func() int { return 42 }

		go runSampler(ctx, h, time.Hour, read, card, func() map[string]int { return map[string]int{"m": 9} })

		synctest.Wait()
		if got := h.goroutines.Len(); got != 1 {
			t.Fatalf("after start samples = %d, want 1", got)
		}

		time.Sleep(time.Hour)
		synctest.Wait()
		if got := h.goroutines.Len(); got != 2 {
			t.Fatalf("after one tick samples = %d, want 2", got)
		}
		if ct := h.cardTotal.Values(); len(ct) == 0 || ct[len(ct)-1] != 42 {
			t.Fatalf("cardTotal last = %v, want 42", ct)
		}
	})
}

func TestRuntimeHistory_PerMetricSeries(t *testing.T) {
	h := newRuntimeHistory(3)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h.sample(t0, runtimeStats{}, 5, map[string]int{"a": 3})
	h.sample(t0.Add(time.Second), runtimeStats{}, 7, map[string]int{"a": 4, "b": 1})

	got := h.perMetricSeries()
	if want := []int{3, 4}; !equalInts(got["a"], want) {
		t.Errorf("a series = %v, want %v", got["a"], want)
	}
	// b appeared only on the second tick -> its own fresh ring.
	if want := []int{1}; !equalInts(got["b"], want) {
		t.Errorf("b series = %v, want %v", got["b"], want)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
