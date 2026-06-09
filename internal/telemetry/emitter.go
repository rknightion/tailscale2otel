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
	"github.com/rknightion/tailscale2otel/internal/telemetry/pii"
)

// otelEmitter implements Emitter on top of the OpenTelemetry Go SDK.
type otelEmitter struct {
	meter  metric.Meter
	logger log.Logger
	// card counts distinct time series per source metric for the
	// tailscale2otel.series.active self-metric. Nil disables tracking; Observe is
	// nil-safe so the emit path needs no guard.
	card *CardinalityTracker
	// redactor applies PII category filtering at every emit site. Never nil (New
	// with a nil map is a no-op fast path).
	redactor *pii.Redactor

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

	mu         sync.Mutex
	counters   map[string]metric.Float64Counter
	gauges     map[string]metric.Float64Gauge
	updowns    map[string]metric.Float64UpDownCounter
	histograms map[string]metric.Float64Histogram

	// constAttrs are provider-scoped attributes (tailscale.tailnet,
	// tailscale2otel.provider) stamped onto every metric data point and log
	// record. Built once in NewProvider from Options; nil for emitters that have
	// no such dimension (e.g. the telemetrytest Recorder). Roadmap item L: these
	// used to be Resource attributes; they are now signal-scoped on every backend.
	constAttrs []attribute.KeyValue
}

// NewEmitter returns an Emitter that records to the given meter and logger,
// without cardinality self-tracking, a reserved-label set, collision logging,
// PII filtering, or provider-scoped constant attributes.
func NewEmitter(meter metric.Meter, logger log.Logger) Emitter {
	return newOtelEmitter(meter, logger, nil, nil, nil, nil, nil)
}

// NewEmitterWithPII returns an Emitter like NewEmitter but applies the given PII
// categories at every emit site. A nil categories map is equivalent to all-on
// (no redaction, fast path).
func NewEmitterWithPII(meter metric.Meter, logger log.Logger, cats pii.Categories) Emitter {
	return newOtelEmitter(meter, logger, nil, nil, nil, cats, nil)
}

// newOtelEmitter returns an *otelEmitter wired to the given meter, logger,
// (optional) cardinality tracker, reserved promoted-label set, diagnostic logger,
// PII categories, and provider-scoped constant attributes. A nil card disables
// series.active tracking; a nil reserved set and nil diag disable reserved-label
// dropping and collision logging respectively (the intra-attribute collision guard
// still runs). A nil cats map disables PII filtering (all-on fast path). A nil
// constAttrs slice means no provider-scoped attributes are stamped.
func newOtelEmitter(meter metric.Meter, logger log.Logger, card *CardinalityTracker, reserved map[string]struct{}, diag *slog.Logger, cats pii.Categories, constAttrs []attribute.KeyValue) *otelEmitter {
	return &otelEmitter{
		meter:      meter,
		logger:     logger,
		card:       card,
		reserved:   reserved,
		diag:       diag,
		redactor:   pii.New(cats),
		constAttrs: constAttrs,
		counters:   map[string]metric.Float64Counter{},
		gauges:     map[string]metric.Float64Gauge{},
		updowns:    map[string]metric.Float64UpDownCounter{},
		histograms: map[string]metric.Float64Histogram{},
	}
}

func (e *otelEmitter) Counter(name, unit, desc string, add float64, attrs Attrs) {
	attrs = Attrs(e.redactor.Merge(attrs))
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
	filtered, suppress := e.redactor.Identity(attrs)
	if suppress {
		return
	}
	attrs = Attrs(filtered)
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
	filtered, suppress := e.redactor.Identity(attrs)
	if suppress {
		return
	}
	attrs = Attrs(filtered)
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

func (e *otelEmitter) Histogram(name, unit, desc string, value float64, bounds []float64, attrs Attrs) {
	e.HistogramCtx(context.Background(), name, unit, desc, value, bounds, attrs)
}

func (e *otelEmitter) HistogramCtx(ctx context.Context, name, unit, desc string, value float64, bounds []float64, attrs Attrs) {
	attrs = Attrs(e.redactor.Merge(attrs))
	e.mu.Lock()
	h, ok := e.histograms[name]
	if !ok {
		var err error
		h, err = e.meter.Float64Histogram(name,
			metric.WithUnit(unit), metric.WithDescription(desc),
			metric.WithExplicitBucketBoundaries(bounds...))
		if err != nil {
			otel.Handle(err)
		}
		e.histograms[name] = h
	}
	e.mu.Unlock()
	e.card.Observe(name, attrs)
	if h != nil {
		h.Record(ctx, value, metric.WithAttributes(e.buildAttrs(name, attrs)...))
	}
}

func (e *otelEmitter) LogEvent(ev Event) {
	ev.Attrs = Attrs(e.redactor.Log(ev.Attrs))
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
	if len(e.constAttrs) > 0 {
		r.AddAttributes(constAttrsToLogKV(e.constAttrs)...)
	}
	e.logger.Emit(context.Background(), r)
}

// constAttrsToLogKV converts provider-scoped const attrs (string-valued) to log
// key-values. The const attrs are always attribute.String, so a direct conversion
// suffices.
func constAttrsToLogKV(attrs []attribute.KeyValue) []log.KeyValue {
	kvs := make([]log.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		kvs = append(kvs, log.String(string(a.Key), a.Value.AsString()))
	}
	return kvs
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
// OTLP->Prometheus label-name collisions so Grafana Cloud cannot reject the sample
// for a duplicate label, then appending the provider-scoped const attrs (tailnet/
// provider). The const attrs are appended after the collision guard: no collector
// emits tailscale.tailnet / tailscale2otel.provider as a data-point attr, and
// neither is a reserved promoted label, so a plain append is safe.
func (e *otelEmitter) buildAttrs(metricName string, attrs Attrs) []attribute.KeyValue {
	if len(attrs) == 0 {
		if len(e.constAttrs) == 0 {
			return nil
		}
		return append([]attribute.KeyValue(nil), e.constAttrs...)
	}
	// When there are no const attrs, the no-collision path can return toKV's slice
	// directly (no extra append). Otherwise size the slice for kept attrs + const
	// attrs up front so the hot path allocates exactly once.
	if len(e.constAttrs) == 0 {
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
	keep, drops := resolveLabelCollisions(attrs, e.reserved)
	if len(drops) > 0 {
		e.logCollisions(metricName, drops)
	}
	kvs := make([]attribute.KeyValue, 0, len(keep)+len(e.constAttrs))
	for k := range keep {
		kvs = append(kvs, kvFor(k, attrs[k]))
	}
	return append(kvs, e.constAttrs...)
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
