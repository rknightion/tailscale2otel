package telemetry

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// blockingMetricExporter blocks in Shutdown until release is closed (or ctx is
// done), closing started when Shutdown is entered. It models a metric exporter
// whose final flush hangs against an unresponsive backend during graceful
// shutdown — the failure that a sequential Shutdown lets consume the entire
// budget, starving every later pipeline/provider (#204).
type blockingMetricExporter struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockingMetricExporter) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}

func (b *blockingMetricExporter) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(k)
}

func (b *blockingMetricExporter) Export(context.Context, *metricdata.ResourceMetrics) error {
	return nil
}
func (b *blockingMetricExporter) ForceFlush(context.Context) error { return nil }

func (b *blockingMetricExporter) Shutdown(ctx context.Context) error {
	close(b.started)
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// signalLogExporter closes started when its Shutdown is invoked, so a test can
// observe that the log pipeline was given its chance to flush.
type signalLogExporter struct{ started chan struct{} }

func (s *signalLogExporter) Export(context.Context, []sdklog.Record) error { return nil }
func (s *signalLogExporter) ForceFlush(context.Context) error              { return nil }
func (s *signalLogExporter) Shutdown(context.Context) error                { close(s.started); return nil }

// signalTraceExporter closes started when its Shutdown is invoked.
type signalTraceExporter struct{ started chan struct{} }

func (s *signalTraceExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error { return nil }
func (s *signalTraceExporter) Shutdown(context.Context) error                             { close(s.started); return nil }

// TestProviderShutdown_BlockedMetricsDoesNotStarveLogsAndTraces proves the
// acceptance criterion: a metric exporter that blocks until the shared deadline
// must NOT prevent the log and trace pipelines from being shut down. It builds a
// Provider whose metric exporter blocks, then asserts both the log and trace
// exporters' Shutdown are invoked while the metric exporter is still blocked —
// i.e. the three pipelines run concurrently under one bounded deadline. A
// sequential Shutdown fails here: it blocks in the metric exporter and never
// reaches logs/traces before the (test-scoped) timeout.
func TestProviderShutdown_BlockedMetricsDoesNotStarveLogsAndTraces(t *testing.T) {
	metricStarted := make(chan struct{})
	release := make(chan struct{})
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(
		sdkmetric.NewPeriodicReader(
			&blockingMetricExporter{started: metricStarted, release: release},
			sdkmetric.WithInterval(time.Hour),
		),
	))

	logStarted := make(chan struct{})
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(&signalLogExporter{started: logStarted})))

	traceStarted := make(chan struct{})
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(&signalTraceExporter{started: traceStarted}))

	p := &Provider{mp: mp, lp: lp, tp: tp}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.Shutdown(ctx) }()

	for _, c := range []struct {
		name string
		ch   chan struct{}
	}{
		{"logs", logStarted},
		{"traces", traceStarted},
	} {
		select {
		case <-c.ch:
		case <-time.After(time.Second):
			t.Fatalf("%s pipeline shutdown was not invoked while the metric exporter blocked "+
				"(sequential shutdown starves it of the shutdown budget)", c.name)
		}
	}

	<-metricStarted // the metric exporter is indeed the one holding on
	close(release)  // let it finish

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Shutdown returned error after clean concurrent shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not return after unblocking the metric exporter")
	}
}

// TestProviderSetShutdown_BlockedProviderDoesNotStarveOthers proves the
// providerset-level criterion: a provider whose metric exporter blocks must not
// prevent OTHER providers (other tailnets, or the process provider) from being
// shut down. The process provider's metric exporter blocks; the tailnet
// provider's log exporter must still be invoked before the deadline.
func TestProviderSetShutdown_BlockedProviderDoesNotStarveOthers(t *testing.T) {
	procMetricStarted := make(chan struct{})
	procRelease := make(chan struct{})
	procMp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(
		sdkmetric.NewPeriodicReader(
			&blockingMetricExporter{started: procMetricStarted, release: procRelease},
			sdkmetric.WithInterval(time.Hour),
		),
	))
	procLp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(&fakeLogExporter{})))
	proc := &Provider{mp: procMp, lp: procLp}

	tnLogStarted := make(chan struct{})
	tnMp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(
		sdkmetric.NewPeriodicReader(&fakeMetricExporter{}, sdkmetric.WithInterval(time.Hour)),
	))
	tnLp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(&signalLogExporter{started: tnLogStarted})))
	tn := &Provider{mp: tnMp, lp: tnLp}

	ps := &ProviderSet{
		process: proc,
		tailnet: map[string]*Provider{"alpha": tn},
		order:   []string{"alpha"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ps.Shutdown(ctx) }()

	select {
	case <-tnLogStarted:
	case <-time.After(time.Second):
		t.Fatal("tailnet provider was not shut down while the process provider's metric exporter blocked " +
			"(sequential providerset shutdown starves later providers)")
	}

	<-procMetricStarted
	close(procRelease)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ProviderSet.Shutdown did not return after unblocking the process provider")
	}
}

// TestShutdownAll_ConcurrentAndJoinsErrors covers the helper directly: a fast
// pipeline must complete while a slow one is still blocked (concurrency), and the
// returned error must join the errors of every attempted shutdown.
func TestShutdownAll_ConcurrentAndJoinsErrors(t *testing.T) {
	errFast := errors.New("fast pipeline failed")
	errSlow := errors.New("slow pipeline failed")

	fastDone := make(chan struct{})
	slowStarted := make(chan struct{})
	release := make(chan struct{})

	fast := func(context.Context) error {
		close(fastDone)
		return errFast
	}
	slow := func(ctx context.Context) error {
		close(slowStarted)
		select {
		case <-release:
			return errSlow
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got := make(chan error, 1)
	go func() { got <- shutdownAll(ctx, slow, fast) }()

	select {
	case <-fastDone:
	case <-time.After(time.Second):
		t.Fatal("fast pipeline did not run while the slow pipeline blocked")
	}
	<-slowStarted
	close(release)

	err := <-got
	if !errors.Is(err, errFast) {
		t.Errorf("joined error is missing the fast pipeline error: %v", err)
	}
	if !errors.Is(err, errSlow) {
		t.Errorf("joined error is missing the slow pipeline error: %v", err)
	}
}

// TestShutdownAll_BoundedByDeadline proves the total shutdown time stays bounded:
// two functions that only return on ctx cancellation must both return by the
// deadline, and their DeadlineExceeded errors are retained.
func TestShutdownAll_BoundedByDeadline(t *testing.T) {
	block := func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := shutdownAll(ctx, block, block)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("shutdownAll blocked well past the deadline: %v", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
	// Both blocked funcs must have contributed their error (errors.Join dedups by
	// value but must not drop either — assert the message mentions the deadline).
	if err == nil || !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Errorf("joined error should retain the deadline errors: %v", err)
	}
}

// TestShutdownAll_Empty is the trivial guard: no functions means no error.
func TestShutdownAll_Empty(t *testing.T) {
	if err := shutdownAll(context.Background()); err != nil {
		t.Errorf("shutdownAll() with no funcs = %v, want nil", err)
	}
}
