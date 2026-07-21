package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"

	"github.com/rknightion/tailscale2otel/v2/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry/pii"
)

// collisionSeenCap is the maximum number of distinct (metric, dropped-key) pairs
// tracked in collisionSeen. Once reached, no new entries are inserted and a single
// saturation warning is logged. Already-stored keys continue to suppress duplicate
// logs as before. The cap prevents unbounded growth when attacker-controlled labels
// (e.g. node-metrics scrape labels) produce many distinct collision pairs.
const collisionSeenCap = 10_000

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
	// collision does not log on every export. collisionCount tracks the number of
	// distinct entries stored; once it reaches collisionSeenCap no new entries are
	// inserted and a single saturation warning is emitted.
	diag             *slog.Logger
	collisionSeen    sync.Map
	collisionCount   atomic.Int64
	collisionSatOnce sync.Once

	// emittedPoints/emittedLogs are the emit-boundary throughput counters read by
	// EmitStats (see emit_counting.go). They are plain atomic adds — no lock, no
	// allocation — so they stay on the hot path unconditionally, independent of
	// self-observability. A point is counted where it is actually recorded, i.e.
	// after any PII identity suppression has had its say.
	emittedPoints atomic.Uint64
	emittedLogs   atomic.Uint64

	mu          sync.Mutex
	counters    map[string]metric.Float64Counter
	gauges      map[string]metric.Float64Gauge
	updowns     map[string]metric.Float64UpDownCounter
	histograms  map[string]metric.Float64Histogram
	observables map[string]*observableGauge // GaugeSnapshot instruments, by name

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
	// The const attrs (tailscale.tailnet / tailscale2otel.provider) are appended
	// AFTER the collision guard, so a data-point attr whose normalized Prometheus
	// name equals one of theirs (e.g. a node_metrics passthrough label literally
	// named "tailscale_tailnet") would slip a duplicate label past the guard and
	// get the whole sample rejected as otlp_parse_error. Reserve the normalized
	// names of the const attrs actually present so such a passthrough is
	// dropped-and-logged instead, the same as job/instance/service_* (#91).
	r := make(map[string]struct{}, len(reserved)+len(constAttrs))
	for k := range reserved {
		r[k] = struct{}{}
	}
	for _, kv := range constAttrs {
		r[metricdoc.PromLabelName(string(kv.Key))] = struct{}{}
	}
	return &otelEmitter{
		meter:       meter,
		logger:      logger,
		card:        card,
		reserved:    r,
		diag:        diag,
		redactor:    pii.New(cats),
		constAttrs:  constAttrs,
		counters:    map[string]metric.Float64Counter{},
		gauges:      map[string]metric.Float64Gauge{},
		updowns:     map[string]metric.Float64UpDownCounter{},
		histograms:  map[string]metric.Float64Histogram{},
		observables: map[string]*observableGauge{},
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
	e.emittedPoints.Add(1)
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
	e.emittedPoints.Add(1)
	if g != nil {
		g.Record(context.Background(), value, metric.WithAttributes(e.buildAttrs(name, attrs)...))
	}
}

// observableGauge holds the mutable snapshot behind one GaugeSnapshot
// instrument. The registered observable callback ranges points under mu;
// GaugeSnapshot replaces points under mu. Each point's key-values are fully
// resolved (PII + collision + const attrs) at snapshot time so the callback —
// which runs on the SDK's collection goroutine — only observes.
type observableGauge struct {
	mu     sync.Mutex
	points []obsPoint
}

type obsPoint struct {
	value float64
	kvs   []attribute.KeyValue
}

func (e *otelEmitter) GaugeSnapshot(name, unit, desc string, points []GaugePoint) {
	// Resolve every point through the same PII-identity + collision + const-attr
	// pipeline the synchronous Gauge path uses, dropping identity-suppressed
	// points, so the observable path is indistinguishable from Gauge except for
	// its drop-out semantics. Done outside e.mu / the callback so collection is
	// never blocked on attribute resolution.
	resolved := make([]obsPoint, 0, len(points))
	for _, p := range points {
		filtered, suppress := e.redactor.Identity(p.Attrs)
		if suppress {
			continue
		}
		attrs := Attrs(filtered)
		e.card.Observe(name, attrs)
		resolved = append(resolved, obsPoint{value: p.Value, kvs: e.buildAttrs(name, attrs)})
	}
	e.emittedPoints.Add(uint64(len(resolved)))

	e.mu.Lock()
	og, ok := e.observables[name]
	if !ok {
		og = &observableGauge{}
		e.observables[name] = og
		// Register the observable gauge once. Its callback reports exactly the
		// current snapshot; under cumulative temporality an observable gauge uses
		// the SDK's precomputed-last-value aggregation, which reports only the
		// sets observed this cycle — so a series dropped from a later snapshot
		// disappears from the export instead of ghosting (#55).
		_, err := e.meter.Float64ObservableGauge(name,
			metric.WithUnit(unit), metric.WithDescription(desc),
			metric.WithFloat64Callback(func(_ context.Context, o metric.Float64Observer) error {
				og.mu.Lock()
				defer og.mu.Unlock()
				for i := range og.points {
					o.Observe(og.points[i].value, metric.WithAttributes(og.points[i].kvs...))
				}
				return nil
			}))
		if err != nil {
			otel.Handle(err)
		}
	}
	e.mu.Unlock()

	og.mu.Lock()
	og.points = resolved
	og.mu.Unlock()
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
	e.emittedPoints.Add(1)
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
	e.emittedPoints.Add(1)
	if h != nil {
		h.Record(ctx, value, metric.WithAttributes(e.buildAttrs(name, attrs)...))
	}
}

func (e *otelEmitter) LogEvent(ev Event) {
	// Redact the body BEFORE the attrs — the body scrub reads the original attr
	// values to know what to strip (a disabled category's value must not survive
	// in the body just because bodies bypass the attribute filter, #197).
	body := e.redactor.RedactBody(ev.Body, ev.BodyPII, ev.Attrs)
	ev.Attrs = Attrs(e.redactor.Log(ev.Attrs))
	var r log.Record
	if !ev.Timestamp.IsZero() {
		r.SetTimestamp(ev.Timestamp)
	}
	r.SetSeverity(toLogSeverity(ev.Severity))
	r.SetSeverityText(ev.Severity.String())
	r.SetBody(log.StringValue(body))
	// The log SDK exposes a native EventName field (log v0.20.0+); use it instead
	// of carrying the event type as a separate "event.name" attribute.
	if ev.Name != "" {
		r.SetEventName(ev.Name)
	}
	r.AddAttributes(toLogKV(ev.Attrs)...)
	if len(e.constAttrs) > 0 {
		r.AddAttributes(constAttrsToLogKV(e.constAttrs)...)
	}
	e.emittedLogs.Add(1)
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
// provider). The const attrs are appended after the collision guard, which is safe
// because their normalized names are added to e.reserved at construction (#91): a
// data-point attr colliding with one is dropped-and-logged by the guard, so the
// appended const attr is never a duplicate.
//
// #86: the overwhelming common case is that attrs contains no collision at all,
// so hasLabelCollision (allocation-free) is checked first; only when it reports a
// real collision/reservation does the function fall back to resolveLabelCollisions,
// which allocates the two bookkeeping maps needed to actually resolve winners.
func (e *otelEmitter) buildAttrs(metricName string, attrs Attrs) []attribute.KeyValue {
	if len(attrs) == 0 {
		if len(e.constAttrs) == 0 {
			return nil
		}
		return append([]attribute.KeyValue(nil), e.constAttrs...)
	}
	if !hasLabelCollision(attrs, e.reserved) {
		// Fast path: every attribute survives unchanged. One allocation (the
		// output slice) regardless of whether const attrs are present.
		kvs := make([]attribute.KeyValue, 0, len(attrs)+len(e.constAttrs))
		for k, v := range attrs {
			kvs = append(kvs, kvFor(k, v))
		}
		return append(kvs, e.constAttrs...)
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
// New distinct (metric, key) pairs are tracked until collisionSeenCap entries are
// stored; once the cap is reached a single saturation warning is logged and further
// new collisions are silently ignored. Already-stored keys continue to suppress
// duplicate logs. The fast path is: one atomic load (cap check) + one sync.Map
// LoadOrStore per key; no extra locking is introduced.
func (e *otelEmitter) logCollisions(metricName string, drops []labelDrop) {
	if e.diag == nil {
		return
	}
	for _, d := range drops {
		cacheKey := metricName + "\x00" + d.key

		// Fast path: already seen — suppress duplicate.
		if _, dup := e.collisionSeen.Load(cacheKey); dup {
			continue
		}

		// Cap check: once saturated, emit exactly one saturation warning and stop.
		if e.collisionCount.Load() >= collisionSeenCap {
			e.collisionSatOnce.Do(func() {
				e.diag.Warn("label-collision diagnostics saturated; further distinct collisions will not be logged",
					"cap", collisionSeenCap,
				)
			})
			continue
		}

		// New entry: try to store. Another goroutine may have raced us to the same
		// key — LoadOrStore is the atomic gate; only the winner increments the count.
		if _, dup := e.collisionSeen.LoadOrStore(cacheKey, struct{}{}); dup {
			continue
		}
		e.collisionCount.Add(1)

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

// hasLabelCollision reports whether attrs, combined with reserved, contains at
// least one Prometheus label-name collision: an attribute key whose normalized
// name is reserved, or two attribute keys that normalize to the same name. It
// is the allocation-free pre-check buildAttrs uses to skip resolveLabelCollisions
// (and the two maps it allocates) entirely in the common no-collision case.
// Both attrs and reserved are small in practice (a handful of entries), so the
// O(n^2) pairwise scan costs nothing but stays allocation-free — unlike
// resolveLabelCollisions, which must build the chosen/keep maps because it also
// needs to report *which* key wins.
func hasLabelCollision(attrs Attrs, reserved map[string]struct{}) bool {
	for k := range attrs {
		for rk := range reserved {
			if normalizedEqual(k, rk) {
				return true
			}
		}
	}
	for k1 := range attrs {
		for k2 := range attrs {
			if k1 == k2 {
				continue
			}
			if normalizedEqual(k1, k2) {
				return true
			}
		}
	}
	return false
}

// promLabelStream yields, one rune at a time and without allocating, the
// sequence metricdoc.PromLabelName(s) would write. It exists so normalizedEqual
// can compare two keys' normalized forms without ever materializing either
// normalized string. It mirrors PromLabelName's rules exactly: letters and
// underscore pass through; a digit is passed through except at the very start
// of the string, where it is preceded by a synthetic '_' (two output runes for
// that one input rune); anything else becomes '_'.
type promLabelStream struct {
	s       string
	i       int  // next byte offset into s
	queued  rune // a rune already decided but not yet returned
	hasNext bool
}

// next returns the next output rune and true, or (0, false) at end of stream.
func (p *promLabelStream) next() (rune, bool) {
	if p.hasNext {
		p.hasNext = false
		return p.queued, true
	}
	if p.i >= len(p.s) {
		return 0, false
	}
	first := p.i == 0
	r, size := utf8.DecodeRuneInString(p.s[p.i:])
	p.i += size
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		return r, true
	case r >= '0' && r <= '9':
		if first {
			p.queued = r
			p.hasNext = true
			return '_', true
		}
		return r, true
	default:
		return '_', true
	}
}

// normalizedEqual reports whether metricdoc.PromLabelName(a) ==
// metricdoc.PromLabelName(b), without allocating either normalized string.
func normalizedEqual(a, b string) bool {
	if a == b {
		return true
	}
	sa, sb := promLabelStream{s: a}, promLabelStream{s: b}
	for {
		ra, oka := sa.next()
		rb, okb := sb.next()
		if oka != okb || ra != rb {
			return false
		}
		if !oka {
			return true
		}
	}
}

// resolveLabelCollisions returns the set of attribute keys to emit. A key is
// dropped when its Prometheus-normalized name (a) is a reserved promoted label
// (resource wins), or (b) collides with another key's — keeping one deterministic
// winner via preferLabelKey. When nothing collides, keep holds every key and
// drops is empty. buildAttrs only reaches this function once hasLabelCollision
// (allocation-free) has already confirmed a real collision/reservation exists —
// see buildAttrs's fast path for the common case where this is skipped entirely.
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
