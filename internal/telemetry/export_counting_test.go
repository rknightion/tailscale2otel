package telemetry

import (
	"context"
	"testing"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestCountingMetricExporterCountsDataPoints(t *testing.T) {
	inner := &fakeMetricExporter{}
	c := newCountingMetricExporter(inner)

	rm := &metricdata.ResourceMetrics{ScopeMetrics: []metricdata.ScopeMetrics{{
		Metrics: []metricdata.Metrics{
			{Data: metricdata.Sum[float64]{DataPoints: make([]metricdata.DataPoint[float64], 3)}},
			{Data: metricdata.Gauge[int64]{DataPoints: make([]metricdata.DataPoint[int64], 2)}},
			{Data: metricdata.Histogram[float64]{DataPoints: make([]metricdata.HistogramDataPoint[float64], 1)}},
		},
	}}}
	if err := c.Export(context.Background(), rm); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if err := c.Export(context.Background(), rm); err != nil { // cumulative
		t.Fatalf("Export: %v", err)
	}
	if got := c.count(); got != 12 { // (3+2+1) * 2
		t.Errorf("datapoints = %d, want 12", got)
	}
	if inner.calls != 2 {
		t.Errorf("inner.Export calls = %d, want 2 (must delegate)", inner.calls)
	}
}

func TestCountingLogExporterCountsRecords(t *testing.T) {
	inner := &fakeLogExporter{}
	c := newCountingLogExporter(inner)
	if err := c.Export(context.Background(), make([]sdklog.Record, 4)); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if got := c.count(); got != 4 {
		t.Errorf("log records = %d, want 4", got)
	}
	if inner.calls != 1 {
		t.Errorf("inner.Export calls = %d, want 1", inner.calls)
	}
}

// fakeMetricExporter implements sdkmetric.Exporter; only Export is exercised.
type fakeMetricExporter struct{ calls int }

func (f *fakeMetricExporter) Temporality(k sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}
func (f *fakeMetricExporter) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(k)
}
func (f *fakeMetricExporter) Export(context.Context, *metricdata.ResourceMetrics) error {
	f.calls++
	return nil
}
func (f *fakeMetricExporter) ForceFlush(context.Context) error { return nil }
func (f *fakeMetricExporter) Shutdown(context.Context) error   { return nil }

type fakeLogExporter struct{ calls int }

func (f *fakeLogExporter) Export(context.Context, []sdklog.Record) error { f.calls++; return nil }
func (f *fakeLogExporter) ForceFlush(context.Context) error              { return nil }
func (f *fakeLogExporter) Shutdown(context.Context) error                { return nil }
