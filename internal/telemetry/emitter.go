package telemetry

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
)

// otelEmitter implements Emitter on top of the OpenTelemetry Go SDK.
type otelEmitter struct {
	meter  metric.Meter
	logger log.Logger

	mu       sync.Mutex
	counters map[string]metric.Float64Counter
	gauges   map[string]metric.Float64Gauge
	updowns  map[string]metric.Float64UpDownCounter
}

// NewEmitter returns an Emitter that records to the given meter and logger.
func NewEmitter(meter metric.Meter, logger log.Logger) Emitter {
	return &otelEmitter{
		meter:    meter,
		logger:   logger,
		counters: map[string]metric.Float64Counter{},
		gauges:   map[string]metric.Float64Gauge{},
		updowns:  map[string]metric.Float64UpDownCounter{},
	}
}

func (e *otelEmitter) Counter(name, unit, desc string, add float64, attrs Attrs) {
	e.mu.Lock()
	c, ok := e.counters[name]
	if !ok {
		var err error
		c, err = e.meter.Float64Counter(name, metric.WithUnit(unit), metric.WithDescription(desc))
		if err != nil {
			otel.Handle(err)
		}
		e.counters[name] = c
	}
	e.mu.Unlock()
	if c != nil {
		c.Add(context.Background(), add, metric.WithAttributes(toKV(attrs)...))
	}
}

func (e *otelEmitter) Gauge(name, unit, desc string, value float64, attrs Attrs) {
	e.mu.Lock()
	g, ok := e.gauges[name]
	if !ok {
		var err error
		g, err = e.meter.Float64Gauge(name, metric.WithUnit(unit), metric.WithDescription(desc))
		if err != nil {
			otel.Handle(err)
		}
		e.gauges[name] = g
	}
	e.mu.Unlock()
	if g != nil {
		g.Record(context.Background(), value, metric.WithAttributes(toKV(attrs)...))
	}
}

func (e *otelEmitter) UpDownCounter(name, unit, desc string, value float64, attrs Attrs) {
	e.mu.Lock()
	u, ok := e.updowns[name]
	if !ok {
		var err error
		u, err = e.meter.Float64UpDownCounter(name, metric.WithUnit(unit), metric.WithDescription(desc))
		if err != nil {
			otel.Handle(err)
		}
		e.updowns[name] = u
	}
	e.mu.Unlock()
	if u != nil {
		u.Add(context.Background(), value, metric.WithAttributes(toKV(attrs)...))
	}
}

func (e *otelEmitter) LogEvent(ev Event) {
	var r log.Record
	if !ev.Timestamp.IsZero() {
		r.SetTimestamp(ev.Timestamp)
	}
	r.SetSeverity(toLogSeverity(ev.Severity))
	r.SetSeverityText(ev.Severity.String())
	r.SetBody(log.StringValue(ev.Body))
	kvs := toLogKV(ev.Attrs)
	// The log SDK has no dedicated EventName field yet (v0.6.0); carry it as the
	// conventional "event.name" attribute.
	if ev.Name != "" {
		kvs = append(kvs, log.String("event.name", ev.Name))
	}
	r.AddAttributes(kvs...)
	e.logger.Emit(context.Background(), r)
}

func toLogSeverity(s Severity) log.Severity {
	switch s {
	case SeverityWarn:
		return log.SeverityWarn
	case SeverityError:
		return log.SeverityError
	default:
		return log.SeverityInfo
	}
}

// toLogKV converts an Attrs map to OTEL log attributes.
func toLogKV(attrs Attrs) []log.KeyValue {
	if len(attrs) == 0 {
		return nil
	}
	kvs := make([]log.KeyValue, 0, len(attrs)+1)
	for k, v := range attrs {
		switch val := v.(type) {
		case string:
			kvs = append(kvs, log.String(k, val))
		case bool:
			kvs = append(kvs, log.Bool(k, val))
		case int:
			kvs = append(kvs, log.Int64(k, int64(val)))
		case int64:
			kvs = append(kvs, log.Int64(k, val))
		case float64:
			kvs = append(kvs, log.Float64(k, val))
		case []string:
			kvs = append(kvs, log.String(k, strings.Join(val, ",")))
		default:
			kvs = append(kvs, log.String(k, fmt.Sprint(val)))
		}
	}
	return kvs
}

// toKV converts an Attrs map to OTEL metric attributes.
func toKV(attrs Attrs) []attribute.KeyValue {
	if len(attrs) == 0 {
		return nil
	}
	kvs := make([]attribute.KeyValue, 0, len(attrs))
	for k, v := range attrs {
		switch val := v.(type) {
		case string:
			kvs = append(kvs, attribute.String(k, val))
		case bool:
			kvs = append(kvs, attribute.Bool(k, val))
		case int:
			kvs = append(kvs, attribute.Int(k, val))
		case int64:
			kvs = append(kvs, attribute.Int64(k, val))
		case float64:
			kvs = append(kvs, attribute.Float64(k, val))
		case []string:
			kvs = append(kvs, attribute.StringSlice(k, val))
		default:
			kvs = append(kvs, attribute.String(k, fmt.Sprint(val)))
		}
	}
	return kvs
}
