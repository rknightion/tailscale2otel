package telemetry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type forceFlushMetricExporter struct {
	started            chan struct{}
	release            <-chan struct{}
	err                error
	forceFlushCalls    atomic.Int32
	exportedDataPoints atomic.Int64
	shutdownCalls      atomic.Int32
}

func (*forceFlushMetricExporter) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}

func (*forceFlushMetricExporter) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(k)
}

func (e *forceFlushMetricExporter) Export(_ context.Context, rm *metricdata.ResourceMetrics) error {
	e.exportedDataPoints.Add(countDataPoints(rm))
	return nil
}

func (e *forceFlushMetricExporter) ForceFlush(ctx context.Context) error {
	e.forceFlushCalls.Add(1)
	signalForceFlushStarted(e.started)
	if e.release != nil {
		select {
		case <-e.release:
			return e.err
		case <-ctx.Done():
		}
	} else {
		<-ctx.Done()
	}
	return fmt.Errorf("metric force flush: %w", ctx.Err())
}

func (e *forceFlushMetricExporter) Shutdown(context.Context) error {
	e.shutdownCalls.Add(1)
	return nil
}

type forceFlushLogExporter struct {
	started         chan struct{}
	release         <-chan struct{}
	err             error
	forceFlushCalls atomic.Int32
	exportedRecords atomic.Int64
	shutdownCalls   atomic.Int32
}

func (e *forceFlushLogExporter) Export(_ context.Context, records []sdklog.Record) error {
	e.exportedRecords.Add(int64(len(records)))
	return nil
}

func (e *forceFlushLogExporter) ForceFlush(ctx context.Context) error {
	e.forceFlushCalls.Add(1)
	signalForceFlushStarted(e.started)
	if e.release != nil {
		select {
		case <-e.release:
			return e.err
		case <-ctx.Done():
		}
	} else {
		<-ctx.Done()
	}
	return fmt.Errorf("log force flush: %w", ctx.Err())
}

func (e *forceFlushLogExporter) Shutdown(context.Context) error {
	e.shutdownCalls.Add(1)
	return nil
}

type forceFlushTraceProcessor struct {
	forceFlushCalls atomic.Int32
	shutdownCalls   atomic.Int32
}

func (*forceFlushTraceProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}
func (*forceFlushTraceProcessor) OnEnd(sdktrace.ReadOnlySpan)                     {}

func (p *forceFlushTraceProcessor) ForceFlush(context.Context) error {
	p.forceFlushCalls.Add(1)
	return nil
}

func (p *forceFlushTraceProcessor) Shutdown(context.Context) error {
	p.shutdownCalls.Add(1)
	return nil
}

func newForceFlushProvider(
	metricExporter *forceFlushMetricExporter,
	logExporter *forceFlushLogExporter,
	traceProcessor *forceFlushTraceProcessor,
) *Provider {
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(
		sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(time.Hour)),
	))
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)))
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(traceProcessor))
	emitter := NewEmitter(mp.Meter(scopeName), lp.Logger(scopeName))
	return &Provider{mp: mp, lp: lp, tp: tp, emitter: emitter}
}

func forceFlushStartedChannel() chan struct{} { return make(chan struct{}, 8) }

func signalForceFlushStarted(started chan<- struct{}) {
	select {
	case started <- struct{}{}:
	default:
	}
}

// newForceFlushRelease returns a barrier channel and an idempotent func that
// opens it, for tests that park the fake exporters mid-flush. Pair it with
// cleanupForceFlushProvider so the barrier is always opened before shutdown.
func newForceFlushRelease() (chan struct{}, func()) {
	release := make(chan struct{})
	return release, sync.OnceFunc(func() { close(release) })
}

// cleanupForceFlushProvider opens the barrier and only then shuts p down, in one
// cleanup so the order cannot be got wrong by registration sequence. Opening
// first is load-bearing: sdklog's LoggerProvider.Shutdown blocks until every
// in-flight processor operation drains (v0.21.0+, provider.go
// waitForProcessorOperations) and the cleanup context is uncancellable, so a
// t.Fatal before the body opens the barrier would otherwise wedge Shutdown
// forever — a ten-minute package timeout instead of a reported failure.
func cleanupForceFlushProvider(t *testing.T, p *Provider, openBarrier func()) {
	t.Helper()
	t.Cleanup(func() {
		openBarrier()
		_ = p.Shutdown(context.Background())
	})
}

func waitForForceFlushStart(t *testing.T, signal string, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatalf("%s ForceFlush did not start while the other signal was blocked", signal)
	}
}

func TestProviderForceFlush_FlushesMetricsAndLogsConcurrentlyWithoutStoppingProviders(t *testing.T) {
	release, openBarrier := newForceFlushRelease()
	errMetric := errors.New("metric flush failed")
	errLog := errors.New("log flush failed")
	metricExporter := &forceFlushMetricExporter{
		started: forceFlushStartedChannel(),
		release: release,
		err:     errMetric,
	}
	logExporter := &forceFlushLogExporter{
		started: forceFlushStartedChannel(),
		release: release,
		err:     errLog,
	}
	traceProcessor := &forceFlushTraceProcessor{}
	p := newForceFlushProvider(metricExporter, logExporter, traceProcessor)
	cleanupForceFlushProvider(t, p, openBarrier)

	done := make(chan error, 1)
	go func() {
		done <- p.ForceFlush(context.Background())
	}()

	waitForForceFlushStart(t, "metric", metricExporter.started)
	waitForForceFlushStart(t, "log", logExporter.started)
	openBarrier()

	select {
	case err := <-done:
		if !errors.Is(err, errMetric) {
			t.Errorf("ForceFlush error is missing metric failure: %v", err)
		}
		if !errors.Is(err, errLog) {
			t.Errorf("ForceFlush error is missing log failure: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ForceFlush did not return after both signal flushes completed")
	}

	if got := metricExporter.shutdownCalls.Load(); got != 0 {
		t.Errorf("metric exporter Shutdown calls = %d, want 0", got)
	}
	if got := logExporter.shutdownCalls.Load(); got != 0 {
		t.Errorf("log exporter Shutdown calls = %d, want 0", got)
	}
	if got := traceProcessor.forceFlushCalls.Load(); got != 0 {
		t.Errorf("trace processor ForceFlush calls = %d, want 0", got)
	}
	if got := traceProcessor.shutdownCalls.Load(); got != 0 {
		t.Errorf("trace processor Shutdown calls = %d, want 0", got)
	}
}

func TestProviderForceFlush_PropagatesContextCancellationThroughBothSDKs(t *testing.T) {
	// release is closed once the assertions below are done so the fakes stop
	// blocking before the cleanup Shutdown runs. sdklog's BatchProcessor calls
	// the decorated exporter's ForceFlush from its shutdown path as well as from
	// ForceFlush (sdk/log v0.21.0+, batch.go shutdownExporter), and the cleanup
	// context is uncancellable — a fake still parked on <-ctx.Done() there hangs
	// the test forever rather than failing it.
	release, openBarrier := newForceFlushRelease()
	metricExporter := &forceFlushMetricExporter{started: forceFlushStartedChannel(), release: release}
	logExporter := &forceFlushLogExporter{started: forceFlushStartedChannel(), release: release}
	traceProcessor := &forceFlushTraceProcessor{}
	p := newForceFlushProvider(metricExporter, logExporter, traceProcessor)
	cleanupForceFlushProvider(t, p, openBarrier)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- p.ForceFlush(ctx)
	}()

	waitForForceFlushStart(t, "metric", metricExporter.started)
	waitForForceFlushStart(t, "log", logExporter.started)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("ForceFlush error = %v, want context.Canceled", err)
		}
		if err == nil || !strings.Contains(err.Error(), "metric force flush") {
			t.Errorf("ForceFlush error is missing metric force flush context failure: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ForceFlush did not honor context cancellation")
	}

	// The log leg is asserted by call count rather than by error text. sdklog's
	// BatchProcessor returns a bare ctx.Err() as soon as the context is done and
	// discards the decorated exporter's own error (v0.21.0+, batch.go); v0.20.0
	// always joined it, which is where the "log force flush" wrapper used to
	// surface. Reaching the exporter is the property that matters, and counting
	// the call proves it without depending on error text upstream no longer
	// propagates. Nothing in production parses the signal out of this error —
	// ingresswal.go only distinguishes nil from non-nil.
	if got := logExporter.forceFlushCalls.Load(); got != 1 {
		t.Errorf("log exporter ForceFlush calls = %d, want 1 (cancellation must reach the log exporter)", got)
	}
}

func TestProviderForceFlush_AllowsConcurrentBarriers(t *testing.T) {
	release, openBarrier := newForceFlushRelease()
	metricExporter := &forceFlushMetricExporter{
		started: forceFlushStartedChannel(),
		release: release,
	}
	logExporter := &forceFlushLogExporter{
		started: forceFlushStartedChannel(),
		release: release,
	}
	traceProcessor := &forceFlushTraceProcessor{}
	p := newForceFlushProvider(metricExporter, logExporter, traceProcessor)
	cleanupForceFlushProvider(t, p, openBarrier)

	done := make(chan error, 2)
	for range 2 {
		go func() {
			done <- p.ForceFlush(context.Background())
		}()
	}

	// One barrier of each signal must be in flight before anything is released,
	// so both callers are genuinely queued against a blocked exporter rather
	// than running to completion one after the other.
	//
	// This deliberately does NOT require two LOG flushes to occupy the exporter
	// at once. sdklog's BatchProcessor owns every exporter call in a single
	// worker goroutine and serializes ForceFlush requests through it (v0.21.0+,
	// batch.go), so a second concurrent ForceFlush cannot enter the exporter
	// until the first returns. What Provider must still guarantee is that
	// concurrent callers are safe: both complete, both reach the exporter, and
	// neither shuts a pipeline down. Production never depends on log-flush
	// simultaneity and always calls this under a deadline (ingresswal.go).
	waitForForceFlushStart(t, "metric", metricExporter.started)
	waitForForceFlushStart(t, "log", logExporter.started)
	openBarrier()

	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("concurrent ForceFlush returned error: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("concurrent ForceFlush calls did not complete")
		}
	}

	if got := metricExporter.forceFlushCalls.Load(); got != 2 {
		t.Errorf("metric ForceFlush calls = %d, want 2", got)
	}
	if got := logExporter.forceFlushCalls.Load(); got != 2 {
		t.Errorf("log ForceFlush calls = %d, want 2", got)
	}
	if got := metricExporter.shutdownCalls.Load(); got != 0 {
		t.Errorf("metric exporter Shutdown calls = %d, want 0", got)
	}
	if got := logExporter.shutdownCalls.Load(); got != 0 {
		t.Errorf("log exporter Shutdown calls = %d, want 0", got)
	}
	if got := traceProcessor.forceFlushCalls.Load(); got != 0 {
		t.Errorf("trace processor ForceFlush calls = %d, want 0", got)
	}
	if got := traceProcessor.shutdownCalls.Load(); got != 0 {
		t.Errorf("trace processor Shutdown calls = %d, want 0", got)
	}
}

func TestProviderForceFlush_PreservesEmitterUsabilityBetweenAndAfterBarriers(t *testing.T) {
	release := make(chan struct{})
	close(release)
	metricExporter := &forceFlushMetricExporter{
		started: forceFlushStartedChannel(),
		release: release,
	}
	logExporter := &forceFlushLogExporter{
		started: forceFlushStartedChannel(),
		release: release,
	}
	traceProcessor := &forceFlushTraceProcessor{}
	p := newForceFlushProvider(metricExporter, logExporter, traceProcessor)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })

	if err := p.ForceFlush(context.Background()); err != nil {
		t.Fatalf("first ForceFlush: %v", err)
	}

	p.Emitter().Gauge("test.forceflush.usable", "1", "force-flush usability probe", 1, nil)
	p.Emitter().LogEvent(Event{Name: "test.forceflush.usable", Body: "between barriers"})
	if err := p.ForceFlush(context.Background()); err != nil {
		t.Fatalf("second ForceFlush: %v", err)
	}
	metricAfterSecond := metricExporter.exportedDataPoints.Load()
	logsAfterSecond := logExporter.exportedRecords.Load()
	if metricAfterSecond == 0 {
		t.Fatal("metric emitted between barriers was not exported")
	}
	if logsAfterSecond == 0 {
		t.Fatal("log emitted between barriers was not exported")
	}

	p.Emitter().Gauge("test.forceflush.usable", "1", "force-flush usability probe", 2, nil)
	p.Emitter().LogEvent(Event{Name: "test.forceflush.usable", Body: "after barrier"})
	if err := p.ForceFlush(context.Background()); err != nil {
		t.Fatalf("third ForceFlush: %v", err)
	}
	if got := metricExporter.exportedDataPoints.Load(); got <= metricAfterSecond {
		t.Errorf("metric data points after third barrier = %d, want > %d", got, metricAfterSecond)
	}
	if got := logExporter.exportedRecords.Load(); got <= logsAfterSecond {
		t.Errorf("log records after third barrier = %d, want > %d", got, logsAfterSecond)
	}
	if got := metricExporter.forceFlushCalls.Load(); got != 3 {
		t.Errorf("metric ForceFlush calls = %d, want 3", got)
	}
	if got := logExporter.forceFlushCalls.Load(); got != 3 {
		t.Errorf("log ForceFlush calls = %d, want 3", got)
	}
	if got := metricExporter.shutdownCalls.Load(); got != 0 {
		t.Errorf("metric exporter Shutdown calls = %d, want 0", got)
	}
	if got := logExporter.shutdownCalls.Load(); got != 0 {
		t.Errorf("log exporter Shutdown calls = %d, want 0", got)
	}
	if got := traceProcessor.forceFlushCalls.Load(); got != 0 {
		t.Errorf("trace processor ForceFlush calls = %d, want 0", got)
	}
	if got := traceProcessor.shutdownCalls.Load(); got != 0 {
		t.Errorf("trace processor Shutdown calls = %d, want 0", got)
	}
}
