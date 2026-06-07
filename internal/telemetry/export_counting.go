package telemetry

import (
	"context"
	"sync/atomic"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// ExportStats is the cumulative count of data points and log records handed to
// the OTLP exporters since process start. Zero-valued when self-observability is
// off (no counting wrappers installed).
type ExportStats struct {
	Datapoints int64
	LogRecords int64
}

// countingMetricExporter decorates an sdkmetric.Exporter, tallying the data
// points in every exported batch. All other methods delegate unchanged.
type countingMetricExporter struct {
	sdkmetric.Exporter
	datapoints atomic.Int64
}

func newCountingMetricExporter(inner sdkmetric.Exporter) *countingMetricExporter {
	return &countingMetricExporter{Exporter: inner}
}

func (c *countingMetricExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	c.datapoints.Add(countDataPoints(rm))
	return c.Exporter.Export(ctx, rm)
}

func (c *countingMetricExporter) count() int64 { return c.datapoints.Load() }

// countingLogExporter decorates an sdklog.Exporter, tallying exported records.
type countingLogExporter struct {
	sdklog.Exporter
	records atomic.Int64
}

func newCountingLogExporter(inner sdklog.Exporter) *countingLogExporter {
	return &countingLogExporter{Exporter: inner}
}

func (c *countingLogExporter) Export(ctx context.Context, recs []sdklog.Record) error {
	c.records.Add(int64(len(recs)))
	return c.Exporter.Export(ctx, recs)
}

func (c *countingLogExporter) count() int64 { return c.records.Load() }

// countDataPoints sums the data points across every instrument in rm, handling
// each aggregation shape the SDK can produce (int64 + float64 variants).
func countDataPoints(rm *metricdata.ResourceMetrics) int64 {
	var n int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch d := m.Data.(type) {
			case metricdata.Gauge[int64]:
				n += int64(len(d.DataPoints))
			case metricdata.Gauge[float64]:
				n += int64(len(d.DataPoints))
			case metricdata.Sum[int64]:
				n += int64(len(d.DataPoints))
			case metricdata.Sum[float64]:
				n += int64(len(d.DataPoints))
			case metricdata.Histogram[int64]:
				n += int64(len(d.DataPoints))
			case metricdata.Histogram[float64]:
				n += int64(len(d.DataPoints))
			case metricdata.ExponentialHistogram[int64]:
				n += int64(len(d.DataPoints))
			case metricdata.ExponentialHistogram[float64]:
				n += int64(len(d.DataPoints))
			case metricdata.Summary:
				n += int64(len(d.DataPoints))
			}
		}
	}
	return n
}
