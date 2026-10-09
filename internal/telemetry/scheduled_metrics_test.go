package telemetry

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// Observe actual SDK-produced snapshots, never a replacement reader or a count
// of HTTP requests. Failed attempts keep their original serialized point data.
type scheduledTestExporter struct {
	mu            sync.Mutex
	failed        bool
	attempts      []metricdata.ResourceMetrics
	accepted      []metricdata.ResourceMetrics
	shutdown      bool
	afterShutdown int
}

func (*scheduledTestExporter) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}
func (*scheduledTestExporter) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(k)
}
func (e *scheduledTestExporter) Export(_ context.Context, data *metricdata.ResourceMetrics) error {
	copy, err := cloneResourceMetrics(data)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.shutdown {
		e.afterShutdown++
		return errors.New("export after shutdown")
	}
	e.attempts = append(e.attempts, copy)
	if e.failed {
		return errors.New("fixture delivery unavailable")
	}
	e.accepted = append(e.accepted, copy)
	return nil
}
func (*scheduledTestExporter) ForceFlush(context.Context) error { return nil }
func (e *scheduledTestExporter) Shutdown(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.shutdown = true
	return nil
}
func (e *scheduledTestExporter) recoverDelivery() { e.mu.Lock(); e.failed = false; e.mu.Unlock() }
func scheduledTestProvider(t *testing.T, exp *scheduledTestExporter, interval time.Duration, producers ...sdkmetric.Producer) *Provider {
	t.Helper()
	reader, err := newMetricReader(exp, Options{MetricInterval: interval, MetricProducers: producers})
	if err != nil {
		t.Fatal(err)
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	lp := sdklog.NewLoggerProvider()
	emitter := NewEmitter(mp.Meter("scheduled-fixture"), lp.Logger("scheduled-fixture"))
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
func scheduledTestWork(value float64) IngressWork {
	return IngressWork{Bounds: WorkBounds{InputBytes: 1, MetricOps: 2, MetricBytes: 4096}, Apply: func(_ context.Context, e Emitter) error {
		e.Counter("fixture.required", "1", "required work", value, nil)
		e.Gauge("fixture.unique", "1", "last value", value, nil)
		return nil
	}}
}
func scheduledTestApply(t *testing.T, p *Provider, value float64) *IngressReceipt {
	t.Helper()
	work := scheduledTestWork(value)
	receipt, err := p.ReserveIngress(work.Bounds)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.ApplyIngress(context.Background(), receipt, work); err != nil {
		t.Fatal(err)
	}
	p.SealIngress()
	return receipt
}
func scheduledTestPoints(data []metricdata.ResourceMetrics, name string) []metricdata.DataPoint[float64] {
	var result []metricdata.DataPoint[float64]
	for _, rm := range data {
		for _, scope := range rm.ScopeMetrics {
			for _, metric := range scope.Metrics {
				if metric.Name != name {
					continue
				}
				switch a := metric.Data.(type) {
				case metricdata.Gauge[float64]:
					result = append(result, a.DataPoints...)
				case metricdata.Sum[float64]:
					result = append(result, a.DataPoints...)
				}
			}
		}
	}
	return result
}
func TestScheduledMetrics_OriginalSlotsAndNonCollectingForceFlush(t *testing.T) {
	for _, interval := range []time.Duration{60 * time.Second, 15 * time.Second} {
		t.Run(interval.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				exp := &scheduledTestExporter{}
				p := scheduledTestProvider(t, exp, interval)
				epoch := time.Now()
				p.Emitter().Gauge("fixture.stable", "1", "stable", 3, nil)
				time.Sleep(2 * time.Second)
				receipt := scheduledTestApply(t, p, 1)
				if err := p.ForceFlush(context.Background()); err != nil {
					t.Fatal(err)
				}
				if receipt.Eligible() {
					t.Fatal("uncollected receipt became eligible")
				}
				synctest.Wait()
				if got := p.CollectionStats().ScheduledAttempts; got != 0 {
					t.Fatalf("early collection: %d", got)
				}
				time.Sleep(120*time.Second - 2*time.Second)
				synctest.Wait()
				exp.mu.Lock()
				points := scheduledTestPoints(exp.accepted, "fixture.stable")
				exp.mu.Unlock()
				if len(points) != int(120*time.Second/interval) {
					t.Fatalf("samples=%d, want %d", len(points), 120*time.Second/interval)
				}
				for i, point := range points {
					if point.Time.Sub(epoch) != time.Duration(i+1)*interval || point.Value != 3 {
						t.Fatalf("sample %d: time=%s value=%v", i, point.Time.Sub(epoch), point.Value)
					}
				}
				if !receipt.Eligible() {
					t.Fatal("normal covering collection did not ACK receipt")
				}
			})
		})
	}
}

type scheduledFailProducer struct{ fail bool }

func (p *scheduledFailProducer) Produce(context.Context) ([]metricdata.ScopeMetrics, error) {
	if p.fail {
		p.fail = false
		return nil, errors.New("fixture one-shot Collect error")
	}
	return nil, nil
}
func TestScheduledMetrics_FailedCollectKeepsOriginalMembers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		exp := &scheduledTestExporter{}
		producer := &scheduledFailProducer{fail: true}
		p := scheduledTestProvider(t, exp, 15*time.Second, producer)
		ordinary, stage := NewCollectionProgram()
		ordinary.Counter("fixture.polled", "By", "polled", 7, nil)
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
		time.Sleep(15 * time.Second)
		synctest.Wait()
		if p.CollectionStats().CollectFailures != 1 {
			t.Fatal("did not observe injected SDK Collect failure")
		}
		time.Sleep(time.Second)
		receipt := scheduledTestApply(t, p, 2)
		time.Sleep(14 * time.Second)
		synctest.Wait()
		if receipt.Phase() != Ready || receipt.Eligible() {
			t.Fatalf("old failed stage incorrectly covered later work: phase=%v", receipt.Phase())
		}
		time.Sleep(15 * time.Second)
		synctest.Wait()
		if !receipt.Eligible() {
			t.Fatal("later original slot did not cover required program")
		}
		exp.mu.Lock()
		polled := scheduledTestPoints(exp.accepted, "fixture.polled")
		required := scheduledTestPoints(exp.accepted, "fixture.required")
		exp.mu.Unlock()
		if len(polled) != 2 || polled[0].Value != 7 || polled[1].Value != 7 {
			t.Fatalf("failed Collect re-added counters: %+v", polled)
		}
		if len(required) != 1 || required[0].Value != 2 || required[0].Time != polled[1].Time {
			t.Fatalf("later program has incorrect first coverage: %+v", required)
		}
	})
}
func TestScheduledMetrics_SaturationCollectsAndRetriesImmutablePins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		exp := &scheduledTestExporter{failed: true}
		p := scheduledTestProvider(t, exp, 15*time.Second)
		epoch := time.Now()
		var collected []CollectionObservation
		if err := p.SetCollectionObserver(func(o CollectionObservation) { o.Data = nil; collected = append(collected, o) }); err != nil {
			t.Fatal(err)
		}
		p.Emitter().Gauge("fixture.stable", "1", "stable", 3, nil)
		first := scheduledTestApply(t, p, 1)
		time.Sleep(15 * time.Second)
		synctest.Wait()
		second := scheduledTestApply(t, p, 2)
		time.Sleep(15 * time.Second)
		synctest.Wait()
		if _, err := p.ReserveIngress(scheduledTestWork(3).Bounds); !errors.Is(err, ErrIngressBackpressure) {
			t.Fatalf("third retention credit admitted: %v", err)
		}
		time.Sleep(5 * 15 * time.Second)
		synctest.Wait()
		stats := p.CollectionStats()
		if stats.ScheduledAttempts != 7 || stats.RetainedCredits != 2 || stats.ReservedCredits != 0 || stats.BestEffortDiscardedFull != 5 {
			t.Fatalf("saturation stats: %+v", stats)
		}
		if len(collected) != 7 {
			t.Fatalf("observed SDK collections=%d", len(collected))
		}
		for i, o := range collected {
			if o.SlotAt.Sub(epoch) != time.Duration(i+1)*15*time.Second || o.Discarded != (i >= 2) {
				t.Fatalf("slot %d: %+v", i, o)
			}
		}
		if first.Eligible() || second.Eligible() {
			t.Fatal("failed pins ACKed")
		}
		exp.mu.Lock()
		attempts := scheduledTestPoints(exp.attempts, "fixture.unique")
		exp.mu.Unlock()
		for _, point := range attempts {
			if point.Value != 1 || point.Time.Sub(epoch) != 15*time.Second {
				t.Fatalf("retry mutated pinned gauge: %+v", point)
			}
		}
		time.Sleep(time.Second)
		exp.recoverDelivery()
		time.Sleep(5 * time.Second)
		synctest.Wait()
		if p.CollectionStats().ScheduledAttempts != 7 {
			t.Fatal("recovery triggered an out-of-schedule collection")
		}
		if !first.Eligible() || !second.Eligible() {
			t.Fatal("unchanged pin retry did not acknowledge exact receipts")
		}
		exp.mu.Lock()
		accepted := scheduledTestPoints(exp.accepted, "fixture.unique")
		exp.mu.Unlock()
		if len(accepted) != 2 || accepted[0].Value != 1 || accepted[1].Value != 2 || accepted[0].Time.Sub(epoch) != 15*time.Second || accepted[1].Time.Sub(epoch) != 30*time.Second {
			t.Fatalf("first covering snapshots changed: %+v", accepted)
		}
	})
}

// CollectSlotForTest runs the provider's next ORIGINAL normal slot
// synchronously: the same collectSlot call the scheduled clock makes, with the
// same slot accounting. It exists only in this package's test build so the
// external wire/transport suites can drive exactly one real SDK collection
// deterministically, now that ForceFlush is a non-collecting delivery barrier.
// It is never cadence proof.
func CollectSlotForTest(ctx context.Context, p *Provider) error {
	r := p.metricReader
	r.mu.Lock()
	r.slot++
	id := collectionID{epoch: r.epoch, slot: r.slot}
	r.mu.Unlock()
	return r.collectSlot(ctx, id)
}

// CollectAndFlushForTest is the external suites' replacement for the former
// collecting ForceFlush: one real slot collection, then the provider's
// non-collecting delivery barrier over metrics and logs.
func CollectAndFlushForTest(ctx context.Context, p *Provider) error {
	return errors.Join(CollectSlotForTest(ctx, p), p.ForceFlush(ctx))
}
