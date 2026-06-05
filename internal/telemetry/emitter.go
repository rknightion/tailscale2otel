package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"

	"github.com/rknightion/tailscale2otel/internal/metricdoc"
)

// otelEmitter implements Emitter on top of the OpenTelemetry Go SDK.
type otelEmitter struct {
	meter  metric.Meter
	logger log.Logger
	// card counts distinct time series per source metric for the
	// tailscale2otel.series.active self-metric. Nil disables tracking; Observe is
	// nil-safe so the emit path needs no guard.
	card *CardinalityTracker

	// reserved holds Prometheus label names that Grafana Cloud promotes from the
	// OTEL Resource onto every series (instance/job/service_*). A data-point
	// attribute whose normalized name lands here is dropped before export so it
	// cannot duplicate the promoted label (which Mimir rejects as
	// otlp_parse_error). Nil means none are reserved.
	reserved map[string]struct{}
	// diag logs label-collision resolutions (nil = silent). collisionSeen bounds
	// that logging to once per distinct (metric, dropped-key) so a steady-state
	// collision does not log on every export.
	diag          *slog.Logger
	collisionSeen sync.Map

	mu       sync.Mutex
	counters map[string]metric.Float64Counter
	gauges   map[string]metric.Float64Gauge
	updowns  map[string]metric.Float64UpDownCounter
}

// NewEmitter returns an Emitter that records to the given meter and logger,
// without cardinality self-tracking, a reserved-label set, or collision logging.
func NewEmitter(meter metric.Meter, logger log.Logger) Emitter {
	return newOtelEmitter(meter, logger, nil, nil, nil)
}

// newOtelEmitter returns an *otelEmitter wired to the given meter, logger,
// (optional) cardinality tracker, reserved promoted-label set, and diagnostic
// logger. A nil card disables series.active tracking; a nil reserved set and nil
// diag disable reserved-label dropping and collision logging respectively (the
// intra-attribute collision guard still runs).
func newOtelEmitter(meter metric.Meter, logger log.Logger, card *CardinalityTracker, reserved map[string]struct{}, diag *slog.Logger) *otelEmitter {
	return &otelEmitter{
		meter:    meter,
		logger:   logger,
		card:     card,
		reserved: reserved,
		diag:     diag,
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
	e.card.Observe(name, attrs)
	if c != nil {
		c.Add(context.Background(), add, metric.WithAttributes(e.buildAttrs(name, attrs)...))
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
	e.card.Observe(name, attrs)
	if g != nil {
		g.Record(context.Background(), value, metric.WithAttributes(e.buildAttrs(name, attrs)...))
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
	e.card.Observe(name, attrs)
	if u != nil {
		u.Add(context.Background(), value, metric.WithAttributes(e.buildAttrs(name, attrs)...))
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
	// The log SDK exposes a native EventName field (log v0.20.0+); use it instead
	// of carrying the event type as a separate "event.name" attribute.
	if ev.Name != "" {
		r.SetEventName(ev.Name)
	}
	r.AddAttributes(toLogKV(ev.Attrs)...)
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

// buildAttrs converts attrs to OTEL metric attributes, first resolving any
// OTLP→Prometheus label-name collisions so Grafana Cloud cannot reject the sample
// for a duplicate label. The common, no-collision path is a plain toKV.
func (e *otelEmitter) buildAttrs(metricName string, attrs Attrs) []attribute.KeyValue {
	if len(attrs) == 0 {
		return nil
	}
	keep, drops := resolveLabelCollisions(attrs, e.reserved)
	if len(drops) == 0 {
		return toKV(attrs)
	}
	e.logCollisions(metricName, drops)
	kvs := make([]attribute.KeyValue, 0, len(keep))
	for k := range keep {
		kvs = append(kvs, kvFor(k, attrs[k]))
	}
	return kvs
}

// logCollisions warns about each resolved collision at most once per distinct
// (metric, dropped key), so a steady-state collision does not log every export.
func (e *otelEmitter) logCollisions(metricName string, drops []labelDrop) {
	if e.diag == nil {
		return
	}
	for _, d := range drops {
		if _, dup := e.collisionSeen.LoadOrStore(metricName+"\x00"+d.key, struct{}{}); dup {
			continue
		}
		reason := "duplicate label after OTLP->Prometheus normalization"
		if d.winner == "" {
			reason = "collides with a promoted resource label"
		}
		e.diag.Warn("dropped colliding metric label to avoid otlp_parse_error",
			"metric", metricName,
			"dropped_key", d.key,
			"prometheus_label", d.prom,
			"kept_key", d.winner,
			"reason", reason,
		)
	}
}

// labelDrop records one attribute key removed by the collision guard.
type labelDrop struct {
	key    string // the dropped source attribute key
	prom   string // the Prometheus label name both keys normalize to
	winner string // the key kept instead; "" when dropped as a reserved label
}

// resolveLabelCollisions returns the set of attribute keys to emit. A key is
// dropped when its Prometheus-normalized name (a) is a reserved promoted label
// (resource wins), or (b) collides with another key's — keeping one deterministic
// winner via preferLabelKey. When nothing collides, keep holds every key and
// drops is empty (the caller then takes the cheap toKV path).
func resolveLabelCollisions(attrs Attrs, reserved map[string]struct{}) (keep map[string]struct{}, drops []labelDrop) {
	chosen := make(map[string]string, len(attrs)) // prom label name -> winning source key
	for k := range attrs {
		pl := metricdoc.PromLabelName(k)
		if _, isReserved := reserved[pl]; isReserved {
			drops = append(drops, labelDrop{key: k, prom: pl})
			continue
		}
		cur, ok := chosen[pl]
		if !ok {
			chosen[pl] = k
			continue
		}
		win := preferLabelKey(cur, k, pl)
		chosen[pl] = win
		lose := k
		if win == k {
			lose = cur
		}
		drops = append(drops, labelDrop{key: lose, prom: pl, winner: win})
	}
	keep = make(map[string]struct{}, len(chosen))
	for _, k := range chosen {
		keep[k] = struct{}{}
	}
	return keep, drops
}

// preferLabelKey chooses which of two keys that normalize to pl to keep: the
// semantic OTEL key (not already in Prometheus form, e.g. dotted) beats an
// already-normalized untrusted passthrough/scraped key; ties break lexically.
func preferLabelKey(a, b, pl string) string {
	switch aNorm, bNorm := a == pl, b == pl; {
	case aNorm && !bNorm:
		return b
	case bNorm && !aNorm:
		return a
	case a <= b:
		return a
	default:
		return b
	}
}

// toKV converts an Attrs map to OTEL metric attributes (no collision handling).
func toKV(attrs Attrs) []attribute.KeyValue {
	if len(attrs) == 0 {
		return nil
	}
	kvs := make([]attribute.KeyValue, 0, len(attrs))
	for k, v := range attrs {
		kvs = append(kvs, kvFor(k, v))
	}
	return kvs
}

// kvFor converts a single Attrs value to an OTEL attribute, mirroring the value
// types documented on Attrs.
func kvFor(k string, v any) attribute.KeyValue {
	switch val := v.(type) {
	case string:
		return attribute.String(k, val)
	case bool:
		return attribute.Bool(k, val)
	case int:
		return attribute.Int(k, val)
	case int64:
		return attribute.Int64(k, val)
	case float64:
		return attribute.Float64(k, val)
	case []string:
		return attribute.StringSlice(k, val)
	default:
		return attribute.String(k, fmt.Sprint(val))
	}
}
