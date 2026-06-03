// Package telemetrytest provides in-memory test helpers for asserting the
// OpenTelemetry output produced through the internal/telemetry Emitter.
//
// A Recorder wires a telemetry.Emitter to in-memory metric and log readers so
// collector tests can assert exactly which metric points and log records were
// emitted. The helpers return plain data structures; callers perform their own
// assertions (this package deliberately does not import the testing package).
package telemetrytest

import (
	"context"
	"sort"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// MetricPoint is a single recorded metric data point, flattened for assertions.
type MetricPoint struct {
	Name      string
	Unit      string
	Kind      string // "sum" or "gauge"
	Value     float64
	Monotonic bool // only meaningful for sums
	Attrs     map[string]string
}

// LogRecord is a single captured log record, flattened for assertions.
type LogRecord struct {
	Body         string
	SeverityText string
	EventName    string // the OTLP LogRecord EventName field (native, log v0.20.0+)
	Severity     int    // OTEL log severity value
	Attrs        map[string]string
}

// Recorder wires a telemetry.Emitter to in-memory readers.
type Recorder struct {
	reader  *sdkmetric.ManualReader
	exp     *recordingLogExporter
	lp      *sdklog.LoggerProvider
	emitter telemetry.Emitter
}

// New returns a Recorder backed by an in-memory metric reader and log exporter.
func New() *Recorder {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	exp := &recordingLogExporter{}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exp)))

	e := telemetry.NewEmitter(mp.Meter("test"), lp.Logger("test"))

	return &Recorder{
		reader:  reader,
		exp:     exp,
		lp:      lp,
		emitter: e,
	}
}

// Emitter returns the telemetry.Emitter under test.
func (r *Recorder) Emitter() telemetry.Emitter {
	return r.emitter
}

// MetricPoints collects current metrics and returns one MetricPoint per data
// point of the metric named name. Unknown names yield nil.
func (r *Recorder) MetricPoints(name string) []MetricPoint {
	var rm metricdata.ResourceMetrics
	if err := r.reader.Collect(context.Background(), &rm); err != nil {
		return nil
	}

	var out []MetricPoint
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			out = append(out, metricPoints(m)...)
		}
	}
	return out
}

// MetricNames collects current metrics and returns the sorted, de-duplicated
// names of every recorded metric.
func (r *Recorder) MetricNames() []string {
	var rm metricdata.ResourceMetrics
	if err := r.reader.Collect(context.Background(), &rm); err != nil {
		return nil
	}

	seen := map[string]struct{}{}
	var names []string
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if _, ok := seen[m.Name]; ok {
				continue
			}
			seen[m.Name] = struct{}{}
			names = append(names, m.Name)
		}
	}
	sort.Strings(names)
	return names
}

// LogRecords returns the captured log records, flattened for assertions.
func (r *Recorder) LogRecords() []LogRecord {
	recs := r.exp.all()
	out := make([]LogRecord, 0, len(recs))
	for i := range recs {
		out = append(out, flattenLogRecord(recs[i]))
	}
	return out
}

// metricPoints flattens a single metricdata.Metrics into MetricPoints, handling
// both float64 and int64 Sum/Gauge data.
func metricPoints(m metricdata.Metrics) []MetricPoint {
	switch d := m.Data.(type) {
	case metricdata.Sum[float64]:
		out := make([]MetricPoint, 0, len(d.DataPoints))
		for _, dp := range d.DataPoints {
			out = append(out, MetricPoint{
				Name:      m.Name,
				Unit:      m.Unit,
				Kind:      "sum",
				Value:     dp.Value,
				Monotonic: d.IsMonotonic,
				Attrs:     attrMap(dp.Attributes),
			})
		}
		return out
	case metricdata.Sum[int64]:
		out := make([]MetricPoint, 0, len(d.DataPoints))
		for _, dp := range d.DataPoints {
			out = append(out, MetricPoint{
				Name:      m.Name,
				Unit:      m.Unit,
				Kind:      "sum",
				Value:     float64(dp.Value),
				Monotonic: d.IsMonotonic,
				Attrs:     attrMap(dp.Attributes),
			})
		}
		return out
	case metricdata.Gauge[float64]:
		out := make([]MetricPoint, 0, len(d.DataPoints))
		for _, dp := range d.DataPoints {
			out = append(out, MetricPoint{
				Name:  m.Name,
				Unit:  m.Unit,
				Kind:  "gauge",
				Value: dp.Value,
				Attrs: attrMap(dp.Attributes),
			})
		}
		return out
	case metricdata.Gauge[int64]:
		out := make([]MetricPoint, 0, len(d.DataPoints))
		for _, dp := range d.DataPoints {
			out = append(out, MetricPoint{
				Name:  m.Name,
				Unit:  m.Unit,
				Kind:  "gauge",
				Value: float64(dp.Value),
				Attrs: attrMap(dp.Attributes),
			})
		}
		return out
	default:
		return nil
	}
}

// attrMap converts a metric attribute.Set to a string-keyed map.
func attrMap(set attribute.Set) map[string]string {
	out := map[string]string{}
	for it := set.Iter(); it.Next(); {
		kv := it.Attribute()
		out[string(kv.Key)] = kv.Value.String()
	}
	return out
}

// flattenLogRecord converts a captured sdklog.Record to a LogRecord.
func flattenLogRecord(rec sdklog.Record) LogRecord {
	attrs := map[string]string{}
	rec.WalkAttributes(func(kv log.KeyValue) bool {
		attrs[string(kv.Key)] = kv.Value.AsString()
		return true
	})
	return LogRecord{
		Body:         rec.Body().AsString(),
		SeverityText: rec.SeverityText(),
		EventName:    rec.EventName(),
		Severity:     int(rec.Severity()),
		Attrs:        attrs,
	}
}

// recordingLogExporter captures emitted log records for later inspection. It
// implements the sdklog.Exporter interface.
type recordingLogExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (e *recordingLogExporter) Export(_ context.Context, recs []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range recs {
		e.records = append(e.records, recs[i].Clone())
	}
	return nil
}

func (e *recordingLogExporter) Shutdown(context.Context) error   { return nil }
func (e *recordingLogExporter) ForceFlush(context.Context) error { return nil }

func (e *recordingLogExporter) all() []sdklog.Record {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]sdklog.Record(nil), e.records...)
}
