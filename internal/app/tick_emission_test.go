package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/rknightion/tailscale2otel/v2/internal/collector"
	"github.com/rknightion/tailscale2otel/v2/internal/config"
	"github.com/rknightion/tailscale2otel/v2/internal/enrich"
	"github.com/rknightion/tailscale2otel/v2/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v2/internal/provider"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

// rollupFlowRecord is a single accumulable flow used to prime the rollup path.
func rollupFlowRecord() flowlog.FlowLog {
	return flowlog.FlowLog{
		NodeID: "nLaptop",
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: 6, Src: "100.64.0.1:50000", Dst: "100.64.0.2:443", TxPkts: 10, TxBytes: 1000, RxPkts: 8, RxBytes: 800},
		},
	}
}

// TestRunRollupFlusher_TickEmits pins #92: the ticker branch of runRollupFlusher
// actually invokes FlushRollup (nothing before the first tick; the *.rollup
// counters after one interval).
func TestRunRollupFlusher_TickEmits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := telemetrytest.New()
		proc := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{FlowMetricsMode: "rollup"})
		proc.Process(rollupFlowRecord(), rec.Emitter()) // accumulate; not yet flushed

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go runRollupFlusher(ctx, proc, rec.Emitter(), time.Hour)
		synctest.Wait()

		if got := len(rec.MetricPoints(flowlog.MetricIORollup)); got != 0 {
			t.Fatalf("rollup emitted before the first tick: %d points", got)
		}
		time.Sleep(time.Hour)
		synctest.Wait()
		if got := len(rec.MetricPoints(flowlog.MetricIORollup)); got == 0 {
			t.Fatal("rollup counter not emitted after one tick (ticker branch not dispatching FlushRollup)")
		}
	})
}

// TestRunCardinalityReporter_TickEmits pins #92: the ticker branch of
// runCardinalityReporter actually invokes reportCardinalityCycle.
func TestRunCardinalityReporter_TickEmits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := telemetrytest.New()
		tr := telemetry.NewCardinalityTracker()
		tr.Observe("tailscale.device.online", telemetry.Attrs{"id": "a"})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go runCardinalityReporter(ctx, rec.Emitter(), tr, map[string]string{"tailscale.device.online": "Devices"}, time.Hour)
		synctest.Wait()

		if got := len(rec.MetricPoints("tailscale2otel.series.active")); got != 0 {
			t.Fatalf("series.active emitted before the first tick: %d points", got)
		}
		time.Sleep(time.Hour)
		synctest.Wait()
		if got := len(rec.MetricPoints("tailscale2otel.series.active")); got == 0 {
			t.Fatal("series.active not emitted after one tick (ticker branch not dispatching reportCardinalityCycle)")
		}
	})
}

// TestApp_RunFlushesRollupOnShutdown pins #92: the authoritative final rollup
// flush in Run() (after schedulers stop, before telemetry shutdown) emits the
// last interval's accumulated *.rollup counters instead of dropping them.
func TestApp_RunFlushesRollupOnShutdown(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	cfg := config.Default()
	cfg.Tailscale.Tailnet = "example.com"
	cfg.Tailscale.Auth.Method = "apikey"
	cfg.Tailscale.Auth.APIKey = "tskey-test"
	cfg.Cardinality.Flow.MetricsMode = "rollup" // default; explicit so the flusher runs

	rec := telemetrytest.New()
	a := newApp(cfg, "v9.9.9", nil, rec.Emitter(), tracenoop.NewTracerProvider().Tracer("test"),
		func(context.Context) error { return nil },
		provider.Tailscale(newTestClient(t, ts.URL)), collector.NewMemoryStore(), NewAPIStats())

	// Accumulate a flow into the rollup accumulator; in rollup mode Process does not
	// emit the *.rollup counters — only FlushRollup does.
	a.runtimes[0].flowProc.Process(rollupFlowRecord(), rec.Emitter())
	if got := len(rec.MetricPoints(flowlog.MetricIORollup)); got != 0 {
		t.Fatalf("rollup emitted by Process (should only emit on flush): %d", got)
	}

	// The export interval (60s) won't tick within this window, so the ONLY thing
	// that can emit the rollup counter is the final flush on shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := a.Run(ctx); err != nil {
		t.Fatalf("Run() = %v, want nil", err)
	}
	if got := len(rec.MetricPoints(flowlog.MetricIORollup)); got == 0 {
		t.Fatal("rollup counters were not flushed on shutdown (final FlushRollup missing/reordered)")
	}
}
