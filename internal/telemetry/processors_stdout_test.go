package telemetry_test

// #384: make the "stdout" protocol a genuinely immediate debugging exporter —
// logs and spans visible synchronously (no batching wait), metrics visible on
// a short interval rather than the production 60s default, and the shared
// lockedWriter serialization preserved now that Simple processors write far
// more often than the Batch processors they replace.

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
)

// syncBuffer is a *bytes.Buffer guarded by a mutex so a test can read its
// contents concurrently with the provider's own writes without racing —
// distinct from the production lockedWriter, which this test also relies on
// to keep concurrent exporter writes from interleaving.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// TestNewProvider_StdoutLogIsImmediate proves a log record is visible on the
// shared writer synchronously — before any ForceFlush/Shutdown call — because
// stdout uses sdklog.NewSimpleProcessor rather than a batch processor.
func TestNewProvider_StdoutLogIsImmediate(t *testing.T) {
	var buf syncBuffer
	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, telemetry.Options{
		ServiceName:  "t384",
		Protocol:     "stdout",
		StdoutWriter: &buf,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.Shutdown(shutdownCtx)
	}()

	p.Emitter().LogEvent(telemetry.Event{Name: "test.stdout.log", Body: "immediate-log-marker"})

	// No ForceFlush, no wait: SimpleProcessor exports inline from OnEmit.
	if !strings.Contains(buf.String(), "immediate-log-marker") {
		t.Fatalf("log record not visible synchronously on stdout; buffer = %q", buf.String())
	}
}

// TestNewProvider_StdoutSpanIsImmediate is the trace-side counterpart: a
// span's End() must flush to the writer synchronously via
// sdktrace.NewSimpleSpanProcessor, no ForceFlush/Shutdown required.
func TestNewProvider_StdoutSpanIsImmediate(t *testing.T) {
	var buf syncBuffer
	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, telemetry.Options{
		ServiceName:    "t384",
		Protocol:       "stdout",
		StdoutWriter:   &buf,
		TracingEnabled: true,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.Shutdown(shutdownCtx)
	}()

	_, span := p.Tracer().Start(ctx, "immediate-span-marker")
	span.End()

	if !strings.Contains(buf.String(), "immediate-span-marker") {
		t.Fatalf("span not visible synchronously on stdout; buffer = %q", buf.String())
	}
}

// TestNewProvider_StdoutConcurrentWritesStaySerialized drives logs and spans
// concurrently and asserts the shared lockedWriter still serializes them —
// this matters MORE than before #384, since Simple processors write on every
// single OnEmit/OnEnd rather than once per batch interval, so interleaving
// would be far more likely to surface under concurrency than with the old
// Batch processors. -race is what actually proves "no data race on the
// writer"; the content check here just proves no corrupted/interleaved bytes.
func TestNewProvider_StdoutConcurrentWritesStaySerialized(t *testing.T) {
	var buf syncBuffer
	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, telemetry.Options{
		ServiceName:    "t384",
		Protocol:       "stdout",
		StdoutWriter:   &buf,
		TracingEnabled: true,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	var wg sync.WaitGroup
	const n = 50
	wg.Add(n * 2)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			p.Emitter().LogEvent(telemetry.Event{Name: "test.concurrent.log", Body: "log"})
		}(i)
		go func(i int) {
			defer wg.Done()
			_, span := p.Tracer().Start(ctx, "concurrent-span")
			span.End()
		}(i)
	}
	wg.Wait()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Shutdown(shutdownCtx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}

	out := buf.String()
	logCount := strings.Count(out, "test.concurrent.log")
	spanCount := strings.Count(out, "concurrent-span")
	if logCount != n {
		t.Errorf("log event count in output = %d, want %d (interleaved/corrupted write?)", logCount, n)
	}
	if spanCount != n {
		t.Errorf("span name count in output = %d, want %d (interleaved/corrupted write?)", spanCount, n)
	}
}

// TestNewProvider_StdoutMetricIntervalDefaultsShort proves the metric
// PeriodicReader uses the short stdout default (not the production 60s
// default) when neither Options.MetricInterval nor Options.Stdout.MetricInterval
// is set. Uses testing/synctest's fake clock (per this repo's convention for
// interval-dependent tests) so the test advances virtual time instead of
// sleeping for real.
func TestNewProvider_StdoutMetricIntervalDefaultsShort(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var buf syncBuffer
		ctx := context.Background()
		p, err := telemetry.NewProvider(ctx, telemetry.Options{
			ServiceName:  "t384",
			Protocol:     "stdout",
			StdoutWriter: &buf,
			// MetricInterval and Stdout.MetricInterval both left unset.
		})
		if err != nil {
			t.Fatalf("NewProvider: %v", err)
		}
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = p.Shutdown(shutdownCtx)
		}()

		p.Emitter().Counter("test.stdout.counter", "1", "", 1, nil)

		// Advance well past the stdout short default (5s) but nowhere near the
		// production default (60s); synctest.Wait lets every goroutine reach a
		// durable blocking point on the advanced fake clock before we check.
		time.Sleep(6 * time.Second)
		synctest.Wait()

		if !strings.Contains(buf.String(), "test.stdout.counter") {
			t.Fatalf("metric not exported within 6s fake-clock time; stdout metric interval is not using the short default")
		}
	})
}
