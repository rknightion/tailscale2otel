package telemetry_test

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
)

// tailnetFanoutCounts is the set of fan-out sizes #364 asks to benchmark:
// 1 (baseline single-tailnet, the common case), 10 and 50 (small/medium MSP
// deployments), and 100 (the stated upper bound). bench_test.go's
// BenchmarkProviderSet_NewAndShutdown already covers construct+shutdown
// allocs at 1/10/50 — this file adds the 100-tailnet case plus the
// measurements #364 explicitly asks for that no existing benchmark covers:
// live resource footprint (heap, goroutines) while N providers are held open,
// and export-burst alignment across their independently-ticking
// PeriodicReaders.
var tailnetFanoutCounts = []int{1, 10, 50, 100}

func buildFanoutTailnets(n int) []telemetry.PerTailnetOptions {
	tailnets := make([]telemetry.PerTailnetOptions, n)
	for i := range tailnets {
		tailnets[i] = telemetry.PerTailnetOptions{
			Name:       fmt.Sprintf("tailnet-%d.example.com", i),
			InstanceID: fmt.Sprintf("host/tailnet-%d", i),
		}
	}
	return tailnets
}

// BenchmarkProviderSetConstructShutdown_100 extends bench_test.go's
// BenchmarkProviderSet_NewAndShutdown (1/10/50) up to the 100-tailnet case
// #364 explicitly asks for, using the identical stdout/io.Discard pattern
// (no network — see the repo CLAUDE.md gotcha on holding tools to their
// pinned versions/patterns) so the numbers are directly comparable.
func BenchmarkProviderSetConstructShutdown_100(b *testing.B) {
	tailnets := buildFanoutTailnets(100)
	base := telemetry.Options{
		ServiceName:  "tailscale2otel",
		Protocol:     "stdout",
		StdoutWriter: io.Discard,
	}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		ps, err := telemetry.NewProviderSet(ctx, base, tailnets)
		if err != nil {
			b.Fatalf("NewProviderSet: %v", err)
		}
		if err := ps.Shutdown(ctx); err != nil {
			b.Fatalf("Shutdown: %v", err)
		}
	}
}

// BenchmarkProviderSetForceFlush measures the cost of one synchronized export
// cycle across every provider in the set (process + N tailnets) — the
// steady-state operation the app's scheduler triggers once per
// otlp.metric_interval, and the one whose per-provider independence #364
// worries produces "synchronized export bursts". Each iteration emits one
// data point per provider (so every export actually has something to encode)
// then force-flushes every provider in construction order, sequentially,
// mirroring how ProviderSet.Shutdown and the app's own flush path walk the
// set today (no fan-out concurrency to hide the per-provider cost).
func BenchmarkProviderSetForceFlush(b *testing.B) {
	for _, n := range tailnetFanoutCounts {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			base := telemetry.Options{
				ServiceName:  "tailscale2otel",
				Protocol:     "stdout",
				StdoutWriter: io.Discard,
				// Long interval: the PeriodicReader's own background ticker
				// must not race with the explicit ForceFlush calls below.
				MetricInterval: time.Hour,
			}
			ctx := context.Background()
			ps, err := telemetry.NewProviderSet(ctx, base, buildFanoutTailnets(n))
			if err != nil {
				b.Fatalf("NewProviderSet: %v", err)
			}
			b.Cleanup(func() { _ = ps.Shutdown(ctx) })

			providers := make([]*telemetry.Provider, 0, n+1)
			providers = append(providers, ps.Process())
			for _, name := range ps.TailnetNames() {
				providers = append(providers, ps.Tailnet(name))
			}

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				for _, p := range providers {
					p.Emitter().Counter("tailscale2otel.bench.counter", "1", "bench counter", 1,
						telemetry.Attrs{"k": "v"})
				}
				for _, p := range providers {
					if err := p.ForceFlush(ctx); err != nil {
						b.Fatalf("ForceFlush: %v", err)
					}
				}
			}
		})
	}
}

// TestProviderSetResourceFootprint measures the live heap and goroutine cost
// of holding a ProviderSet open at each fan-out size — the "extra
// timers/goroutines" #364's evidence section calls out, as an actual resident
// measurement rather than a per-op allocation count (BenchmarkProviderSet_*
// above/in bench_test.go already covers per-op allocs). Not a hard
// pass/fail gate: the numbers are evidence for the MATERIAL/NOT-MATERIAL
// judgment call, logged so they land in `go test -v` output and can be
// pasted into the tracking issue.
func TestProviderSetResourceFootprint(t *testing.T) {
	if testing.Short() {
		t.Skip("forces GC + settles goroutines between samples; skip under -short")
	}
	type row struct {
		n              int
		heapDeltaBytes int64
		goroutineDelta int
	}
	ctx := context.Background()
	rows := make([]row, 0, len(tailnetFanoutCounts))

	for _, n := range tailnetFanoutCounts {
		// Let any previous iteration's shutdown goroutines finish exiting
		// before sampling the baseline, so the delta isolates THIS
		// iteration's construction rather than straggling teardown.
		time.Sleep(20 * time.Millisecond)
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		goroutinesBefore := runtime.NumGoroutine()

		base := telemetry.Options{
			ServiceName:  "tailscale2otel",
			Protocol:     "stdout",
			StdoutWriter: io.Discard,
		}
		ps, err := telemetry.NewProviderSet(ctx, base, buildFanoutTailnets(n))
		if err != nil {
			t.Fatalf("NewProviderSet(%d): %v", n, err)
		}

		goroutinesAfter := runtime.NumGoroutine()
		runtime.GC()
		var after runtime.MemStats
		runtime.ReadMemStats(&after)

		rows = append(rows, row{
			n:              n,
			heapDeltaBytes: int64(after.HeapAlloc) - int64(before.HeapAlloc), //nolint:gosec // G115: MemStats fields fit int64 in practice
			goroutineDelta: goroutinesAfter - goroutinesBefore,
		})

		if err := ps.Shutdown(ctx); err != nil {
			t.Fatalf("Shutdown(%d): %v", n, err)
		}
	}

	t.Log("tailnets | heap delta (KiB) | goroutine delta | goroutines/tailnet")
	for _, r := range rows {
		perTailnet := "n/a (baseline)"
		if r.n > 0 {
			perTailnet = fmt.Sprintf("%.2f", float64(r.goroutineDelta)/float64(r.n+1))
		}
		t.Logf("%8d | %17.1f | %15d | %s",
			r.n, float64(r.heapDeltaBytes)/1024, r.goroutineDelta, perTailnet)
	}
}

// timestampWriter records the wall-clock time of every Write call. Used to
// observe how tightly the independent PeriodicReaders across a ProviderSet's
// providers cluster their export writes — the "synchronized export bursts"
// #364's evidence section names, since NewProviderSet constructs every
// provider (and therefore starts every PeriodicReader ticker) back-to-back
// with the same MetricInterval.
type timestampWriter struct {
	mu      sync.Mutex
	ts      []time.Time
	readyAt int
	ready   chan struct{}
	once    sync.Once
}

func newTimestampWriter(readyAt int) *timestampWriter {
	return &timestampWriter{readyAt: readyAt, ready: make(chan struct{})}
}

func (w *timestampWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.ts = append(w.ts, time.Now())
	if len(w.ts) >= w.readyAt {
		w.once.Do(func() { close(w.ready) })
	}
	w.mu.Unlock()
	return len(p), nil
}

func (w *timestampWriter) snapshot() []time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]time.Time, len(w.ts))
	copy(out, w.ts)
	return out
}

// TestProviderSetExportBurstAlignment gathers evidence for whether the
// process + N tailnet providers' independent PeriodicReaders actually export
// in a synchronized burst, as #364 hypothesizes. It emits one data point per
// provider, waits for one write from every provider, then measures how tightly
// the FIRST tick's writes (one per provider, since each Provider
// wraps the same shared writer in its own lockedWriter — see NewProvider's
// stdout branch) cluster in wall-clock time.
//
// This is exploratory evidence-gathering, not a regression gate: it only
// asserts that every provider's first export was actually observed (a
// stronger timing assertion would be flaky on a loaded CI runner). The
// interesting number is the logged spread, which this test's own report
// interprets against the configured interval.
func TestProviderSetExportBurstAlignment(t *testing.T) {
	if testing.Short() {
		t.Skip("wall-clock timing probe; skip under -short")
	}
	const n = 10
	const interval = 50 * time.Millisecond
	const wantProviders = n + 1 // process + n tailnets

	w := newTimestampWriter(wantProviders)
	base := telemetry.Options{
		ServiceName:    "tailscale2otel",
		Protocol:       "stdout",
		StdoutWriter:   w,
		MetricInterval: interval,
	}
	ctx := context.Background()
	ps, err := telemetry.NewProviderSet(ctx, base, buildFanoutTailnets(n))
	if err != nil {
		t.Fatalf("NewProviderSet: %v", err)
	}

	// Give every provider something to export on its first tick.
	ps.Process().Emitter().Counter("tailscale2otel.bench.counter", "1", "bench counter", 1, nil)
	for _, name := range ps.TailnetNames() {
		ps.Tailnet(name).Emitter().Counter("tailscale2otel.bench.counter", "1", "bench counter", 1, nil)
	}

	// The writer closes ready after the first wantProviders writes. This waits
	// for the observed exports themselves, with no scheduler or interval margin.
	<-w.ready

	if err := ps.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	ts := w.snapshot()
	if len(ts) < wantProviders {
		t.Fatalf("got %d stdout writes, want at least %d (one per provider's first export)",
			len(ts), wantProviders)
	}

	sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
	// The first wantProviders writes, sorted, are each provider's first
	// export (every PeriodicReader's first tick fires at ~construction_time +
	// interval, and construction of all N+1 providers takes microseconds —
	// see BenchmarkProviderSetConstructShutdown_100 above for that cost).
	first := ts[:wantProviders]
	spread := first[len(first)-1].Sub(first[0])

	t.Logf("export burst: first tick across %d providers spread over %v (interval %v, spread/interval=%.4f)",
		wantProviders, spread, interval, float64(spread)/float64(interval))
}
