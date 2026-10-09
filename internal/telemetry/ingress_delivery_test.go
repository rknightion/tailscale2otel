package telemetry

import (
	"context"
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
