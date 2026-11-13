package telemetry

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
)

func TestCardinalityTracker_LifetimeMatchesCumulativeSDK(t *testing.T) {
	for _, batchSize := range []int{2, 4} {
		for _, temporality := range []string{"cumulative", "delta"} {
			t.Run(fmt.Sprintf("%s/batch%d", temporality, batchSize), func(t *testing.T) {
				ctx := context.Background()
				p, err := NewProvider(ctx, Options{Protocol: "stdout", StdoutWriter: io.Discard,
					SelfObsEnabled: true, CardinalityLimit: 10, MetricTemporality: temporality, MetricInterval: time.Hour})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := p.Shutdown(ctx); err != nil {
						t.Error(err)
					}
				})
				reader := sdkmetric.NewManualReader(sdkmetric.WithTemporalitySelector(metricTemporalitySelector(temporality)))
				opts := append(metricProviderOptions(resource.Empty(), 10, false), sdkmetric.WithReader(reader))
				mp := sdkmetric.NewMeterProvider(opts...)
				t.Cleanup(func() {
					if err := mp.Shutdown(ctx); err != nil {
						t.Error(err)
					}
				})
				e := newOtelEmitter(mp.Meter("test"), noop.NewLoggerProvider().Logger("test"), p.card, nil, nil, nil, nil)
				for interval := 0; interval < 7; interval++ {
					if interval < 6 {
						for j := 0; j < batchSize; j++ {
							e.Counter("synthetic.counter", "1", "counter", 1, Attrs{"synthetic.key": interval*batchSize + j})
						}
					}
					size := 3
					if interval > 0 {
						size = 1
					}
					points := make([]GaugePoint, size)
					for j := range points {
						points[j] = GaugePoint{Value: float64(j + 1), Attrs: Attrs{"synthetic.key": j}}
					}
					e.GaugeSnapshot("synthetic.snapshot", "1", "snapshot", points)
					p.card.Report(e)
					var rm metricdata.ResourceMetrics
					if err := reader.Collect(ctx, &rm); err != nil {
						t.Fatal(err)
					}
					metrics := map[string]metricdata.Metrics{}
					for _, scope := range rm.ScopeMetrics {
						for _, m := range scope.Metrics {
							metrics[m.Name] = m
						}
					}
					active := map[string]float64{}
					overflow := map[string]float64{}
					for name, dst := range map[string]map[string]float64{seriesActiveMetric: active, seriesOverflowMetric: overflow} {
						for _, dp := range metrics[name].Data.(metricdata.Gauge[float64]).DataPoints {
							metric, _ := dp.Attributes.Value(attribute.Key("metric.name"))
							dst[metric.AsString()] = dp.Value
						}
					}
					if got := active["synthetic.snapshot"]; got != float64(size) {
						t.Fatalf("interval %d snapshot=%v want %d", interval, got, size)
					}
					if got := len(metrics["synthetic.snapshot"].Data.(metricdata.Gauge[float64]).DataPoints); got != size {
						t.Fatalf("SDK snapshot size=%d want%d", got, size)
					}
					if temporality == "delta" && interval == 6 {
						continue
					}
					sdkPoints := metrics["synthetic.counter"].Data.(metricdata.Sum[float64]).DataPoints
					sdkOverflow := false
					for _, dp := range sdkPoints {
						if v, ok := dp.Attributes.Value("otel.metric.overflow"); ok && v.AsBool() {
							sdkOverflow = true
						}
					}
					want := min((interval+1)*batchSize, 10)
					if temporality == "delta" {
						want = batchSize
					}
					if len(sdkPoints) != want || active["synthetic.counter"] != float64(want) {
						t.Fatalf("interval %d SDK=%d tracker=%v want%d", interval, len(sdkPoints), active["synthetic.counter"], want)
					}
					if (overflow["synthetic.counter"] == 1) != sdkOverflow {
						t.Fatalf("interval%d SDK overflow=%v tracker=%v", interval, sdkOverflow, overflow["synthetic.counter"])
					}
					for _, entry := range p.card.Snapshot() {
						if entry.Metric == "synthetic.counter" && entry.Count != want {
							t.Fatalf("snapshot=%+v want%d", entry, want)
						}
					}
				}
			})
		}
	}
}
