package telemetry_test

// #358: configure the log/trace batch processor queues and expose queue
// saturation/drop telemetry. These tests drive telemetry.NewProvider (the
// real production entry point — the same one provider.go's own tests use) so
// newLogProcessor/newSpanProcessor are exercised exactly as they run in prod,
// and assert the resulting telemetry through internal/telemetrytest.Recorder
// (never SDK internals — sdklog.BatchProcessor and sdktrace's batch span
// processor are both unexported concrete types with no queue-size or
// dropped-count accessor; confirmed by reading the pinned
// sdk/log@v0.20.0/batch.go and sdk@v1.44.0/trace/batch_span_processor.go
// sources, see the #358 report).

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"testing/synctest"
	"time"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

// blockingLogServer is an httptest handler that signals reached on every
// request, then blocks until release is closed (or the request context is
// canceled). It stands in for a stalled OTLP backend.
func blockingServer(reached chan<- struct{}, release <-chan struct{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		select {
		case reached <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

// TestNewProvider_ZeroBatchOptionsLogsFlowThrough proves a zero BatchOptions
// does not break or silently drop anything: with no Tracker and no QueueOptions,
// newLogProcessor must fall back to the plain sdklog.NewBatchProcessor(exp) path
// (no wrapping, no functional options — see the QueueOptions.isZero() guard in
// processors.go), so records emitted here must reach the exporter after a
// ForceFlush exactly as they did before #358.
func TestNewProvider_ZeroBatchOptionsLogsFlowThrough(t *testing.T) {
	done := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, telemetry.Options{
		ServiceName: "t358",
		Protocol:    "http",
		Endpoint:    srv.URL,
		// Batch left zero on purpose.
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := p.Shutdown(shutdownCtx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	}()

	p.Emitter().LogEvent(telemetry.Event{Name: "test.event", Body: "hello"})
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("log record never reached the exporter under zero BatchOptions")
	}
}

// TestNewProvider_ZeroBatchOptionsTracesFlowThrough is the trace-side
// counterpart: zero BatchOptions must still deliver spans via the plain
// sdktrace.NewBatchSpanProcessor(exp) path.
func TestNewProvider_ZeroBatchOptionsTracesFlowThrough(t *testing.T) {
	// The server and watchdog live outside the fake clock: socket I/O is real,
	// while the unmodified SDK batch processor's five-second boundary is fake.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	watchdog := ctx.Done() // Create this real-clock channel outside the bubble.
	done := make(chan *collectortrace.ExportTraceServiceRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/traces" {
			w.WriteHeader(http.StatusOK)
			return // An unrelated metric/log request cannot satisfy this proof.
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var request collectortrace.ExportTraceServiceRequest
		if err != nil || proto.Unmarshal(body, &request) != nil {
			t.Error("invalid trace protobuf request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		done <- &request
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	synctest.Test(t, func(t *testing.T) {
		// Close sockets before leaving the bubble, joining SDK transport loops.
		defer srv.Close()
		p, err := telemetry.NewProvider(context.Background(), telemetry.Options{
			ServiceName: "t358", Protocol: "http", Endpoint: srv.URL,
			TracingEnabled: true, // Batch deliberately remains entirely zero.
		})
		if err != nil {
			t.Fatalf("NewProvider: %v", err)
		}
		defer func() {
			shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if err := p.Shutdown(shutdownCtx); err != nil {
				t.Errorf("Shutdown: %v", err)
			}
		}()
		_, span := p.Tracer().Start(context.Background(), "test-span")
		identity := span.SpanContext()
		span.End()
		if err := p.ForceFlush(context.Background()); err != nil {
			t.Fatalf("ForceFlush: %v", err)
		}
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("trace exported before normal SDK batch boundary")
		default:
		}
		time.Sleep(5 * time.Second)
		select {
		case request := <-done:
			if len(request.ResourceSpans) != 1 || len(request.ResourceSpans[0].ScopeSpans) != 1 || len(request.ResourceSpans[0].ScopeSpans[0].Spans) != 1 {
				t.Fatalf("want exactly one trace span: %v", request)
			}
			got := request.ResourceSpans[0].ScopeSpans[0].Spans[0]
			traceID, spanID := identity.TraceID(), identity.SpanID()
			if got.Name != "test-span" || string(got.TraceId) != string(traceID[:]) || string(got.SpanId) != string(spanID[:]) {
				t.Fatalf("wrong exported trace identity: %v", got)
			}
		case <-watchdog:
			t.Fatal("span never reached trace endpoint under zero BatchOptions")
		}
	})
}

// TestBatchQueueTracker_LogsSaturateAndDropUnderStalledExporter is the #358
// acceptance test: a deliberately stalled log exporter, the log queue driven
// to full, and the drop metric proven to move — asserted through
// telemetrytest.Recorder, never internals.
func TestBatchQueueTracker_LogsSaturateAndDropUnderStalledExporter(t *testing.T) {
	reached := make(chan struct{}, 1)
	release := make(chan struct{})
	srv := httptest.NewServer(blockingServer(reached, release))
	defer srv.Close()

	tracker := telemetry.NewBatchQueueTracker()
	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, telemetry.Options{
		ServiceName: "t358",
		Protocol:    "http",
		Endpoint:    srv.URL,
		Batch: telemetry.BatchOptions{
			Tracker: tracker,
			Logs: telemetry.QueueOptions{
				MaxQueueSize:       4,
				ExportMaxBatchSize: 1,
				ExportInterval:     5 * time.Millisecond,
				ExportTimeout:      time.Minute,
			},
		},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	// First record: the background flush loop picks it up almost immediately
	// (5ms interval, batch size 1) and gets stuck in the handler, holding the
	// export goroutine — this is what makes the queue actually fill rather
	// than draining as fast as it's offered.
	p.Emitter().LogEvent(telemetry.Event{Name: "test.event", Body: "0"})
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("stalled exporter was never reached — first record never flushed")
	}

	// Now flood past the configured queue size of 4; some must be refused.
	for i := 1; i <= 20; i++ {
		p.Emitter().LogEvent(telemetry.Event{Name: "test.event", Body: "x"})
	}

	rec := telemetrytest.New()
	tracker.Report(rec.Emitter())

	sizePts := rec.MetricPoints("tailscale2otel.processor.queue.size")
	capPts := rec.MetricPoints("tailscale2otel.processor.queue.capacity")
	dropPts := rec.MetricPoints("tailscale2otel.processor.dropped")

	var sawLogsSize, sawLogsCap, sawLogsDrop bool
	for _, p := range sizePts {
		if p.Attrs["signal"] == "logs" {
			sawLogsSize = true
			if p.Value != 4 {
				t.Errorf("queue.size{signal=logs} = %v, want 4 (queue full)", p.Value)
			}
		}
	}
	for _, p := range capPts {
		if p.Attrs["signal"] == "logs" {
			sawLogsCap = true
			if p.Value != 4 {
				t.Errorf("queue.capacity{signal=logs} = %v, want 4", p.Value)
			}
		}
	}
	for _, p := range dropPts {
		if p.Attrs["signal"] == "logs" {
			sawLogsDrop = true
			if p.Attrs["reason"] != "queue_full" {
				t.Errorf("dropped{signal=logs} reason = %q, want %q", p.Attrs["reason"], "queue_full")
			}
			if p.Value <= 0 {
				t.Errorf("dropped{signal=logs} = %v, want > 0", p.Value)
			}
		}
	}
	if !sawLogsSize || !sawLogsCap || !sawLogsDrop {
		t.Fatalf("missing expected points: size=%v cap=%v drop=%v (size=%+v cap=%+v drop=%+v)",
			sawLogsSize, sawLogsCap, sawLogsDrop, sizePts, capPts, dropPts)
	}

	close(release)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Shutdown(shutdownCtx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

// TestBatchQueueTracker_TracesSaturateAndDropUnderStalledExporter is the trace
// counterpart of the saturation test above.
func TestBatchQueueTracker_TracesSaturateAndDropUnderStalledExporter(t *testing.T) {
	reached := make(chan struct{}, 1)
	release := make(chan struct{})
	srv := httptest.NewServer(blockingServer(reached, release))
	defer srv.Close()

	tracker := telemetry.NewBatchQueueTracker()
	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, telemetry.Options{
		ServiceName:    "t358",
		Protocol:       "http",
		Endpoint:       srv.URL,
		TracingEnabled: true,
		Batch: telemetry.BatchOptions{
			Tracker: tracker,
			Traces: telemetry.QueueOptions{
				MaxQueueSize:       4,
				ExportMaxBatchSize: 1,
				ExportInterval:     5 * time.Millisecond,
				ExportTimeout:      time.Minute,
			},
		},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	_, span := p.Tracer().Start(ctx, "span-0")
	span.End()
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("stalled exporter was never reached — first span never flushed")
	}

	for i := 0; i < 20; i++ {
		_, s := p.Tracer().Start(ctx, "span-n")
		s.End()
	}

	rec := telemetrytest.New()
	tracker.Report(rec.Emitter())

	var sawTracesDrop bool
	for _, pt := range rec.MetricPoints("tailscale2otel.processor.dropped") {
		if pt.Attrs["signal"] == "traces" {
			sawTracesDrop = true
			if pt.Value <= 0 {
				t.Errorf("dropped{signal=traces} = %v, want > 0", pt.Value)
			}
		}
	}
	if !sawTracesDrop {
		t.Fatalf("no dropped{signal=traces} point emitted: %+v", rec.MetricPoints("tailscale2otel.processor.dropped"))
	}

	close(release)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Shutdown(shutdownCtx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

// TestBatchQueueTracker_NoGoroutineLeak builds and tears down several
// instrumented providers and asserts the goroutine count returns to baseline,
// proving the queueing processors' background loops always exit on Shutdown
// even when their exporter was blocked.
func TestBatchQueueTracker_NoGoroutineLeak(t *testing.T) {
	baseline := runtime.NumGoroutine()

	for i := 0; i < 3; i++ {
		release := make(chan struct{})
		srv := httptest.NewServer(blockingServer(make(chan struct{}, 1), release))

		tracker := telemetry.NewBatchQueueTracker()
		ctx := context.Background()
		p, err := telemetry.NewProvider(ctx, telemetry.Options{
			ServiceName:    "t358-leak",
			Protocol:       "http",
			Endpoint:       srv.URL,
			TracingEnabled: true,
			Batch: telemetry.BatchOptions{
				Tracker: tracker,
				Logs:    telemetry.QueueOptions{MaxQueueSize: 4, ExportMaxBatchSize: 1, ExportInterval: 5 * time.Millisecond, ExportTimeout: time.Second},
				Traces:  telemetry.QueueOptions{MaxQueueSize: 4, ExportMaxBatchSize: 1, ExportInterval: 5 * time.Millisecond, ExportTimeout: time.Second},
			},
		})
		if err != nil {
			t.Fatalf("NewProvider: %v", err)
		}
		p.Emitter().LogEvent(telemetry.Event{Name: "test.event", Body: "leak"})
		_, span := p.Tracer().Start(ctx, "leak-span")
		span.End()

		close(release) // let the export timeout / handler unblock promptly
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := p.Shutdown(shutdownCtx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
		cancel()
		srv.Close()
	}

	var final int
	for attempt := 0; attempt < 50; attempt++ {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		final = runtime.NumGoroutine()
		if final <= baseline+2 { // small slack for test/runtime bookkeeping goroutines
			return
		}
	}
	t.Fatalf("goroutine count after teardown = %d, want <= baseline(%d)+2", final, baseline)
}
