package telemetry

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// These exporters deliberately violate the SDK context contract in the hung
// case, as a blocked stdout writer can. The cooperative case honors it.
type ingressExportBlock struct {
	entered     chan struct{}
	release     chan struct{}
	cooperative bool
}

func (b *ingressExportBlock) export(ctx context.Context) error {
	b.entered <- struct{}{}
	if b.cooperative {
		select {
		case <-b.release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	<-b.release
	return nil
}

type hungMetricExporter struct {
	scheduledTestExporter
	block *ingressExportBlock
}

func (e *hungMetricExporter) Export(ctx context.Context, _ *metricdata.ResourceMetrics) error {
	return e.block.export(ctx)
}

type hungLogExporter struct{ block *ingressExportBlock }

func (e *hungLogExporter) Export(ctx context.Context, _ []sdklog.Record) error {
	return e.block.export(ctx)
}
func (*hungLogExporter) ForceFlush(context.Context) error { return nil }
func (*hungLogExporter) Shutdown(context.Context) error   { return nil }

func ingressBlockedProvider(t *testing.T, signal string, block *ingressExportBlock) *Provider {
	t.Helper()
	var exp sdkmetric.Exporter = &scheduledTestExporter{}
	if signal == "metrics" {
		exp = &hungMetricExporter{block: block}
	}
	reader, err := newScheduledMetricReader(exp, Options{MetricInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	p := &Provider{mp: mp, metricReader: reader, metricsExcluded: signal != "metrics", logsExcluded: signal != "logs", logBatchSize: 1, logTimeout: time.Minute}
	p.serialLogs = newSerialLogExporter(&hungLogExporter{block: block})
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(&ingressLogProcessor{owner: p, next: sdklog.NewSimpleProcessor(p.serialLogs)}))
	p.lp = lp
	p.emitter = NewEmitter(mp.Meter("hung-fixture"), lp.Logger("hung-fixture"))
	reader.emitter = p.emitter
	reader.Start()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.runIngressLogs(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = mp.Shutdown(ctx)
		_ = lp.Shutdown(ctx)
	})
	return p
}

func testIngressBlockedExport(t *testing.T, signal string, cooperative bool) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		block := &ingressExportBlock{entered: make(chan struct{}, 1), release: make(chan struct{}), cooperative: cooperative}
		p := ingressBlockedProvider(t, signal, block)
		// Register after provider cleanup so a failed assertion still unblocks
		// the worker before shutdown. Future terminal exports pass too.
		defer close(block.release)
		work := scheduledTestWork(1)
		if signal == "logs" {
			work = IngressWork{Bounds: WorkBounds{Logs: LogBounds{Records: 1, Bytes: 4096}}, Apply: func(ctx context.Context, e Emitter) error {
				e.LogEventCtx(ctx, Event{Name: "fixture.hung", Body: "record"})
				return nil
			}}
		}
		receipt, err := p.ReserveIngress(work.Bounds)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.ApplyIngress(context.Background(), receipt, work); err != nil {
			t.Fatal(err)
		}
		p.SealIngress()
		if signal == "metrics" {
			if err := CollectSlotForTest(context.Background(), p); err != nil {
				t.Fatal(err)
			}
		}
		<-block.entered
		synctest.Wait()
		time.Sleep(p.metricReader.timeout - time.Nanosecond)
		synctest.Wait()
		if p.IngressDeliveryRetrying() {
			t.Fatal("export younger than reader timeout reports retrying")
		}
		if !cooperative {
			time.Sleep(time.Nanosecond)
			synctest.Wait()
			if p.IngressDeliveryRetrying() {
				t.Fatal("export exactly at reader timeout reports retrying")
			}
			time.Sleep(time.Nanosecond)
			synctest.Wait()
			if !p.IngressDeliveryRetrying() {
				t.Error("hung export older than reader timeout does not report retrying")
			}
		}
		block.release <- struct{}{}
		synctest.Wait()
		if p.IngressDeliveryRetrying() {
			t.Error("successful return did not clear retrying")
		}
		if !receipt.Eligible() {
			t.Error("successful return did not acknowledge ingress receipt")
		}
	})
}

type permanentScriptMetrics struct {
	scheduledTestExporter
	errors []error
	calls  atomic.Int64
}

func (e *permanentScriptMetrics) Export(ctx context.Context, data *metricdata.ResourceMetrics) error {
	i := int(e.calls.Add(1) - 1)
	if i < len(e.errors) && e.errors[i] != nil {
		return e.errors[i]
	}
	return e.scheduledTestExporter.Export(ctx, data)
}

type permanentScriptLogs struct {
	errors   []error
	calls    atomic.Int64
	mu       sync.Mutex
	accepted []sdklog.Record
}

func (e *permanentScriptLogs) Export(_ context.Context, records []sdklog.Record) error {
	i := int(e.calls.Add(1) - 1)
	if i < len(e.errors) && e.errors[i] != nil {
		return e.errors[i]
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, r := range records {
		e.accepted = append(e.accepted, r.Clone())
	}
	return nil
}
func (e *permanentScriptMetrics) acceptedMetrics() []metricdata.ResourceMetrics {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]metricdata.ResourceMetrics(nil), e.accepted...)
}
func (e *permanentScriptLogs) acceptedCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.accepted)
}
func (*permanentScriptLogs) ForceFlush(context.Context) error { return nil }
func (*permanentScriptLogs) Shutdown(context.Context) error   { return nil }

func permanentScriptProvider(t *testing.T, signal string, m *permanentScriptMetrics, l *permanentScriptLogs, limit int) *Provider {
	t.Helper()
	reader, err := newScheduledMetricReader(m, Options{MetricInterval: time.Hour, MaxPermanentRejections: limit, OnPermanentDrop: func(e Emitter, signal string) { e.Counter("fixture.loss", "1", "loss", 1, Attrs{"signal": signal}) }})
	if err != nil {
		t.Fatal(err)
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	p := &Provider{mp: mp, metricReader: reader, metricsExcluded: signal != "metrics", logsExcluded: signal != "logs", logBatchSize: 1, logTimeout: time.Minute}
	p.serialLogs = newSerialLogExporter(l)
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(&ingressLogProcessor{owner: p, next: sdklog.NewSimpleProcessor(p.serialLogs)}))
	p.lp = lp
	p.emitter = NewEmitter(mp.Meter("permanent-fixture"), lp.Logger("permanent-fixture"))
	reader.emitter = p.emitter
	reader.Start()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.runIngressLogs(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = mp.Shutdown(ctx)
		_ = lp.Shutdown(ctx)
	})
	return p
}
func permanentScriptApply(t *testing.T, p *Provider, signal string, value int, records int) *IngressReceipt {
	t.Helper()
	work := scheduledTestWork(float64(value))
	if signal == "logs" {
		work = IngressWork{Bounds: WorkBounds{Logs: LogBounds{Records: int64(records), Bytes: int64(records) * 4096}}, Apply: func(ctx context.Context, e Emitter) error {
			for range records {
				e.LogEventCtx(ctx, Event{Name: "fixture.loss", Body: "fixture"})
			}
			return nil
		}}
	}
	r, err := p.ReserveIngress(work.Bounds)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ApplyIngress(context.Background(), r, work); err != nil {
		t.Fatal(err)
	}
	p.SealIngress()
	if signal == "metrics" {
		if err := CollectSlotForTest(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	synctest.Wait()
	return r
}

func TestIngressPermanentDrop_NthAndNextHealthy(t *testing.T) {
	for _, signal := range []string{"metrics", "logs"} {
		t.Run(signal, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				permanent := &httpExportRejection{code: 422, signal: signal}
				m := &permanentScriptMetrics{errors: []error{permanent, permanent, permanent}}
				l := &permanentScriptLogs{errors: []error{permanent, permanent, permanent}}
				if signal == SignalLogs {
					m.errors = nil
				} else {
					l.errors = nil
				}
				p := permanentScriptProvider(t, signal, m, l, 3)
				r := permanentScriptApply(t, p, signal, 1, 1)
				if r.Eligible() {
					t.Fatal("first rejection retired work")
				}
				time.Sleep(100 * time.Millisecond)
				synctest.Wait()
				if r.Eligible() {
					t.Fatal("second rejection retired work before N")
				}
				time.Sleep(200 * time.Millisecond)
				synctest.Wait()
				if !r.Eligible() || r.Phase() == Poisoned {
					t.Fatal("Nth rejection must resolve, not poison, members")
				}
				if p.IngressDeliveryRetrying() {
					t.Fatal("drop left stale retrying health")
				}
				stats := p.CollectionStats()
				if signal == "metrics" && (stats.PermanentMetricsDropped != 1 || stats.RequiredSnapshotsAcknowledged != 0) {
					t.Fatalf("drop stats %+v", stats)
				}
				if signal == "logs" && stats.PermanentLogBatchesDropped != 1 {
					t.Fatalf("drop stats %+v", stats)
				}
				if err := p.ReleaseIngress(r); err != nil {
					t.Fatal(err)
				}
				next := permanentScriptApply(t, p, signal, 2, 1)
				if !next.Eligible() {
					t.Fatal("next healthy unit blocked")
				}
				if signal == "metrics" {
					points := scheduledTestPoints(m.acceptedMetrics(), "fixture.unique")
					if len(points) != 1 || points[0].Value != 2 {
						t.Fatalf("next healthy export points=%v", points)
					}
				} else if l.acceptedCount() != 1 {
					t.Fatalf("healthy logs=%d", l.acceptedCount())
				}
				// Collect only in this package-level reader test to inspect the real
				// monotonic counter. The app test proves normal public schedule/WAL.
				if signal == "logs" {
					if err := CollectSlotForTest(context.Background(), p); err != nil {
						t.Fatal(err)
					}
					synctest.Wait()
				}
				points := scheduledTestPoints(m.acceptedMetrics(), "fixture.loss")
				if len(points) != 1 || points[0].Value != 1 {
					t.Fatalf("loss counter points=%v", points)
				}
			})
		})
	}
}

func TestIngressPermanentDrop_OtherClassesBreakRun(t *testing.T) {
	for _, signal := range []string{"metrics", "logs"} {
		for _, code := range []int{401, 403, 408, 429, 500, 503, 599} {
			t.Run(signal+"/"+strconv.Itoa(code), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					permanent := &httpExportRejection{code: 400, signal: signal}
					other := &httpExportRejection{code: code, signal: signal}
					// A transient breaks the run, even after limit-1 permanent errors.
					seq := []error{permanent, permanent, other, permanent, permanent, other, other, other, nil}
					m := &permanentScriptMetrics{errors: seq}
					l := &permanentScriptLogs{errors: seq}
					p := permanentScriptProvider(t, signal, m, l, 3)
					r := permanentScriptApply(t, p, signal, 1, 1)
					for i := 1; i < len(seq); i++ {
						if r.Eligible() {
							t.Fatalf("rejection %d retired work", i)
						}
						time.Sleep(deliveryRetryDelay(i))
						synctest.Wait()
					}
					if !r.Eligible() {
						t.Fatal("success did not resolve receipt")
					}
					if stats := p.CollectionStats(); stats.PermanentMetricsDropped != 0 || stats.PermanentLogBatchesDropped != 0 {
						t.Fatalf("transient dropped work: %+v", stats)
					}
				})
			})
		}
	}
}

func TestIngressPermanentDrop_LogBatchSuccessResets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rejection := &httpExportRejection{code: 410, signal: SignalLogs}
		// First batch succeeds after limit-1 errors; the next batch gets its
		// own entire allowance. Only the second batch drops.
		l := &permanentScriptLogs{errors: []error{rejection, nil, rejection, rejection}}
		p := permanentScriptProvider(t, SignalLogs, &permanentScriptMetrics{}, l, 2)
		r := permanentScriptApply(t, p, SignalLogs, 1, 2)
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		if r.Eligible() || l.calls.Load() != 3 {
			t.Fatalf("next batch allowance lost: eligible=%v calls=%d", r.Eligible(), l.calls.Load())
		}
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		if !r.Eligible() || p.CollectionStats().PermanentLogBatchesDropped != 1 || l.acceptedCount() != 1 {
			t.Fatal("second log batch failed to drop independently")
		}
	})
}

func TestIngressPermanentDrop_UnknownBreaksRun(t *testing.T) {
	if permanentExportRejection(errors.New("failed to upload metrics: 400 backend-chosen text")) {
		t.Fatal("text selected destructive loss")
	}
}

func TestIngressDeliveryRetrying_HungExport(t *testing.T) {
	t.Setenv("OTEL_METRIC_EXPORT_TIMEOUT", "2300")
	for _, signal := range []string{"metrics", "logs"} {
		t.Run(signal, func(t *testing.T) { testIngressBlockedExport(t, signal, false) })
	}
}

func TestIngressDeliveryRetrying_YoungerCooperativeExport(t *testing.T) {
	t.Setenv("OTEL_METRIC_EXPORT_TIMEOUT", "2300")
	for _, signal := range []string{"metrics", "logs"} {
		t.Run(signal, func(t *testing.T) { testIngressBlockedExport(t, signal, true) })
	}
}
