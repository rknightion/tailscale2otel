package telemetry

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type deltaScheduledExporter struct{ scheduledTestExporter }

func (*deltaScheduledExporter) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.DeltaTemporality
}

// deltaScheduledProvider drives slots only through CollectSlotForTest: its
// interval never elapses inside a test.
func deltaScheduledProvider(t *testing.T, exp *deltaScheduledExporter, producers ...sdkmetric.Producer) *Provider {
	t.Helper()
	reader, err := newMetricReader(exp, Options{MetricInterval: 24 * time.Hour, MetricProducers: producers})
	if err != nil {
		t.Fatal(err)
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	lp := sdklog.NewLoggerProvider()
	emitter := NewEmitter(mp.Meter("delta-fixture"), lp.Logger("delta-fixture"))
	p := &Provider{mp: mp, lp: lp, emitter: emitter, metricReader: reader, logsExcluded: true}
	reader.emitter = emitter
	reader.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = mp.Shutdown(ctx)
		_ = lp.Shutdown(ctx)
	})
	return p
}

// deltaApplyGroup applies every value into one sealed ingress group.
func deltaApplyGroup(t *testing.T, p *Provider, values ...float64) []*IngressReceipt {
	t.Helper()
	receipts := make([]*IngressReceipt, 0, len(values))
	for _, value := range values {
		work := scheduledTestWork(value)
		receipt, err := p.ReserveIngress(work.Bounds)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.ApplyIngress(context.Background(), receipt, work); err != nil {
			t.Fatal(err)
		}
		receipts = append(receipts, receipt)
	}
	p.SealIngress()
	return receipts
}

type oneShotDeltaProducer struct {
	mu     sync.Mutex
	fail   bool
	during func()
}

func (p *oneShotDeltaProducer) Produce(context.Context) ([]metricdata.ScopeMetrics, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.fail {
		return nil, nil
	}
	p.fail = false
	if p.during != nil {
		p.during()
	}
	return nil, errors.New("fixture one-shot Producer error")
}

func deltaAccepted(exp *deltaScheduledExporter, name string) []metricdata.DataPoint[float64] {
	exp.mu.Lock()
	defer exp.mu.Unlock()
	return scheduledTestPoints(exp.accepted, name)
}

func assertDeltaReceiptsPoisoned(t *testing.T, p *Provider, exp *deltaScheduledExporter, receipts []*IngressReceipt) {
	t.Helper()
	required := deltaAccepted(exp, "fixture.required")
	for i, receipt := range receipts {
		if receipt.Phase() == Delivered {
			t.Fatalf("false cover: receipt %d reached Delivered with %d required points exported", i, len(required))
		}
		if receipt.Phase() != Poisoned || receipt.Eligible() {
			t.Fatalf("receipt %d: phase=%d eligible=%v, want poisoned and not eligible", i, receipt.Phase(), receipt.Eligible())
		}
		if err := receipt.Wait(context.Background()); !errors.Is(err, errIngressPoison) {
			t.Fatalf("receipt %d Wait = %v, want poison", i, err)
		}
		if err := p.ReleaseIngress(receipt); !errors.Is(err, errIngressPoison) {
			t.Fatalf("receipt %d released for WAL retirement: %v", i, err)
		}
	}
	if len(required) != 0 {
		t.Fatalf("required points exported after failed Collect: %+v", required)
	}
	if err := p.IngressFailure(); !errors.Is(err, ErrIngressTerminal) {
		t.Fatalf("IngressFailure = %v, want terminal", err)
	}
	if _, err := p.ReserveIngress(scheduledTestWork(1).Bounds); !errors.Is(err, ErrIngressTerminal) {
		t.Fatalf("new work admitted after poison: %v", err)
	}
}

func TestScheduledMetrics_DeltaCollectFailureNeverFalseCovers(t *testing.T) {
	for name, arm := range map[string]func(*testing.T, *deltaScheduledExporter) *Provider{
		"producer": func(t *testing.T, exp *deltaScheduledExporter) *Provider {
			return deltaScheduledProvider(t, exp, &oneShotDeltaProducer{fail: true})
		},
		"callback": func(t *testing.T, exp *deltaScheduledExporter) *Provider {
			p := deltaScheduledProvider(t, exp)
			fail := true
			_, err := p.mp.Meter("delta-fixture").Float64ObservableGauge("fixture.callback",
				metric.WithFloat64Callback(func(context.Context, metric.Float64Observer) error {
					if fail {
						fail = false
						return errors.New("fixture one-shot callback error")
					}
					return nil
				}))
			if err != nil {
				t.Fatal(err)
			}
			return p
		},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				exp := &deltaScheduledExporter{}
				p := arm(t, exp)
				receipts := deltaApplyGroup(t, p, 2, 3)
				if err := CollectSlotForTest(context.Background(), p); err == nil {
					t.Fatal("injected Collect failure did not surface")
				}
				if err := CollectSlotForTest(context.Background(), p); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				assertDeltaReceiptsPoisoned(t, p, exp, receipts)
			})
		})
	}
}

// A measurement made after the failed Collect reset belongs to the next
// collection: poisoning the required group must not take it down too.
func TestScheduledMetrics_DeltaFailureKeepsPostResetOrdinaryMeasurement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		exp := &deltaScheduledExporter{}
		producer := &oneShotDeltaProducer{fail: true}
		p := deltaScheduledProvider(t, exp, producer)
		producer.during = func() { p.Emitter().Counter("fixture.ordinary", "1", "ordinary", 5, nil) }
		receipts := deltaApplyGroup(t, p, 2)
		if err := CollectSlotForTest(context.Background(), p); err == nil {
			t.Fatal("injected Collect failure did not surface")
		}
		if err := CollectSlotForTest(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		assertDeltaReceiptsPoisoned(t, p, exp, receipts)
		ordinary := deltaAccepted(exp, "fixture.ordinary")
		if len(ordinary) != 1 || ordinary[0].Value != 5 {
			t.Fatalf("post-reset ordinary measurement = %+v, want one point of 5", ordinary)
		}
	})
}

// Run with -race: writers overlap both the failed and the following Collect.
func TestScheduledMetrics_DeltaFailureWithConcurrentOrdinaryMeasurements(t *testing.T) {
	exp := &deltaScheduledExporter{}
	p := deltaScheduledProvider(t, exp, &oneShotDeltaProducer{fail: true})
	receipts := deltaApplyGroup(t, p, 2)
	const writers, adds = 4, 500
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range writers {
		wg.Go(func() {
			<-start
			for range adds {
				p.Emitter().Counter("fixture.concurrent", "1", "concurrent", 1, nil)
			}
		})
	}
	close(start)
	if err := CollectSlotForTest(context.Background(), p); err == nil {
		t.Fatal("injected Collect failure did not surface")
	}
	if err := CollectSlotForTest(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if err := CollectSlotForTest(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.metricReader.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	assertDeltaReceiptsPoisoned(t, p, exp, receipts)
	var exported float64
	for _, point := range deltaAccepted(exp, "fixture.concurrent") {
		exported += point.Value
	}
	if exported > writers*adds {
		t.Fatalf("ordinary measurements duplicated: exported %v of %d", exported, writers*adds)
	}
}

// Cancellation before produce resets nothing, so the receipt must survive and
// be covered by the next slot exactly once.
func TestScheduledMetrics_DeltaPreProduceCancellationDoesNotPoison(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		exp := &deltaScheduledExporter{}
		p := deltaScheduledProvider(t, exp)
		receipts := deltaApplyGroup(t, p, 2)
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		if err := CollectSlotForTest(canceled, p); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Collect = %v", err)
		}
		if got := receipts[0].Phase(); got != Ready {
			t.Fatalf("phase after cancellation = %d, want Ready", got)
		}
		if err := p.IngressFailure(); err != nil {
			t.Fatalf("cancellation failed the provider: %v", err)
		}
		if err := CollectSlotForTest(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if !receipts[0].Eligible() {
			t.Fatal("next slot did not cover the receipt")
		}
		required := deltaAccepted(exp, "fixture.required")
		if len(required) != 1 || required[0].Value != 2 {
			t.Fatalf("required points = %+v, want one point of 2", required)
		}
	})
}

// Cumulative state survives a failed Collect, so it retries and covers.
func TestScheduledMetrics_CumulativeCollectFailureRetriesAndCovers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		exp := &scheduledTestExporter{}
		p := scheduledTestProvider(t, exp, 24*time.Hour, &scheduledFailProducer{fail: true})
		receipt := scheduledTestApply(t, p, 2)
		if err := CollectSlotForTest(context.Background(), p); err == nil {
			t.Fatal("injected Collect failure did not surface")
		}
		if got := receipt.Phase(); got != Ready {
			t.Fatalf("phase after cumulative failure = %d, want Ready", got)
		}
		if err := CollectSlotForTest(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if !receipt.Eligible() {
			t.Fatal("retry did not cover the receipt")
		}
		exp.mu.Lock()
		required := scheduledTestPoints(exp.accepted, "fixture.required")
		exp.mu.Unlock()
		if len(required) != 1 || required[0].Value != 2 {
			t.Fatalf("required points = %+v, want one point of 2", required)
		}
	})
}

// With no required member nothing can be falsely covered, so an ordinary-only
// delta failure must not fail the provider for ingress.
func TestScheduledMetrics_DeltaOrdinaryOnlyFailureLeavesIngressOpen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		exp := &deltaScheduledExporter{}
		p := deltaScheduledProvider(t, exp, &oneShotDeltaProducer{fail: true})
		ordinary, stage := NewCollectionProgram()
		ordinary.Counter("fixture.ordinary", "1", "ordinary", 1, nil)
		first := true
		if err := p.SetBeforeCollect(func() CollectionStage {
			if first {
				first = false
				return stage
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := CollectSlotForTest(context.Background(), p); err == nil {
			t.Fatal("injected Collect failure did not surface")
		}
		if p.metricReader.stage == nil {
			t.Fatal("fixture did not fail with an ordinary stage in flight")
		}
		if err := p.IngressFailure(); err != nil {
			t.Fatalf("ordinary failure closed ingress: %v", err)
		}
	})
}
