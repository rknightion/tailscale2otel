package telemetry

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

func waitForForceFlushStart(t *testing.T, signal string, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatalf("%s ForceFlush did not start while the other signal was blocked", signal)
	}
}

func TestProviderForceFlush_FlushesMetricsAndLogsConcurrentlyWithoutStoppingProviders(t *testing.T) {
	release := make(chan struct{})
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
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })

	done := make(chan error, 1)
	go func() {
		done <- p.ForceFlush(context.Background())
	}()

	waitForForceFlushStart(t, "metric", metricExporter.started)
	waitForForceFlushStart(t, "log", logExporter.started)
	close(release)

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
	metricExporter := &forceFlushMetricExporter{started: forceFlushStartedChannel()}
	logExporter := &forceFlushLogExporter{started: forceFlushStartedChannel()}
	traceProcessor := &forceFlushTraceProcessor{}
	p := newForceFlushProvider(metricExporter, logExporter, traceProcessor)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })

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
		for _, signal := range []string{"metric force flush", "log force flush"} {
			if err == nil || !strings.Contains(err.Error(), signal) {
				t.Errorf("ForceFlush error is missing %s context failure: %v", signal, err)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("ForceFlush did not honor context cancellation")
	}
}

func TestProviderForceFlush_AllowsConcurrentBarriers(t *testing.T) {
	release := make(chan struct{})
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

	done := make(chan error, 2)
	for range 2 {
		go func() {
			done <- p.ForceFlush(context.Background())
		}()
	}

	// Both calls must reach the log SDK/exporter before either barrier is
	// released. This distinguishes actual overlap from two sequential calls.
	waitForForceFlushStart(t, "first log", logExporter.started)
	waitForForceFlushStart(t, "second log", logExporter.started)
	close(release)

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
