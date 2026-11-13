package telemetry

import (
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
)

// Self-cardinality metric names. The tracker never measures any metric in the
// tailscale2otel.series.* family — both to avoid measuring itself and to break
// the Report -> Gauge -> Observe recursion (Report emits these from inside the
// emit hot path that calls Observe).
const (
	seriesSelfPrefix     = "tailscale2otel.series."
	seriesActiveMetric   = seriesSelfPrefix + "active"
	seriesLimitMetric    = seriesSelfPrefix + "limit"
	seriesOverflowMetric = seriesSelfPrefix + "overflowing"
)

// defaultSeriesCap bounds the distinct fingerprints tracked per source metric.
// Once a metric reaches the cap the reported value pins at the cap (a visible
// signal that the true cardinality is at least this high) and further distinct
// series for that metric are not counted, bounding memory.
const defaultSeriesCap = 10000

// defaultLabelValueCap bounds the distinct VALUES retained per (metric,label)
// for the status page's label-cardinality views. It is deliberately small: the
// values are only needed as examples plus a distinct count, and an unbounded set
// on a high-cardinality label (e.g. a per-flow IP) would defeat the point.
const defaultLabelValueCap = 100

// labelValueSet is the bounded distinct-value set for one (metric,label) within
// the current export interval. capped records whether the per-label value cap
// was hit so the reported distinct count can be pinned and examples truncated.
type labelValueSet struct {
	values map[string]struct{}
	capped bool
}

// seriesSet holds bounded interval fingerprints and label samples. Cumulative
// synchronous instruments also retain life fingerprints and lifeCapped across
// reports; observable snapshots and delta instruments use only interval state.
type seriesSet struct {
	fps        map[uint64]struct{}
	capped     bool
	labels     map[string]*labelValueSet
	life       map[uint64]struct{}
	lifeCapped bool
	sync       bool
}

// SeriesCount is the latest distinct-series count for one source metric.
// Cumulative synchronous counts include the process lifetime. Capped is true
// when the per-metric cap was hit, in which case Count is pinned at that cap.
type SeriesCount struct {
	Metric string
	Count  int
	Capped bool
}

// LabelStat is the distinct-value cardinality for one (metric, label) during the
// last completed export interval. Distinct is pinned at the per-label value cap
// when Capped is true, in which case Examples is a truncated sample.
type LabelStat struct {
	Metric   string
	Label    string   // attribute key
	Distinct int      // distinct values seen; pinned at the cap when Capped
	Capped   bool     // the per-(metric,label) value cap was hit
	Examples []string // sorted sample values (len == Distinct; truncated when Capped)
}

// CardinalityTracker counts the EXACT number of distinct attribute combinations
// (time series) emitted per source metric. Providers select lifetime tracking
// for cumulative synchronous instruments; constructors otherwise use interval
// tracking. Observe runs on the emit hot path; Report emits the
// tailscale2otel.series.active gauge once per source metric. The same per-metric
// counts are retained for the most recent interval and exposed via Snapshot for
// in-process introspection (e.g. the admin status page).
//
// All methods are safe for concurrent use and are no-ops on a nil receiver.
type CardinalityTracker struct {
	lifetime        bool // synchronous instruments retain attribute sets under cumulative temporality
	mu              sync.Mutex
	sets            map[string]*seriesSet
	seriesCap       int           // per-source-metric distinct-series cap (pins the reported count)
	labelValueCap   int           // per-(metric,label) distinct-VALUE cap (0 disables label capture)
	configuredLimit int           // raw cardinality.metric_limit (<=0 means "unlimited"; suppresses series.limit/overflowing)
	last            []SeriesCount // counts from the most recent Report; nil before the first
	lastLabels      []LabelStat   // per-(metric,label) distinct values from the most recent Report; nil before the first
}

// NewCardinalityTracker returns an empty tracker using the package default
// per-metric cap (defaultSeriesCap) and per-label value cap (defaultLabelValueCap).
func NewCardinalityTracker() *CardinalityTracker {
	return NewCardinalityTrackerWithLimits(defaultSeriesCap, defaultLabelValueCap)
}

// NewCardinalityTrackerWithCap returns an empty tracker that pins each source
// metric's distinct-series count at seriesCap, using the default per-label value
// cap. Retained for callers that only tune the series cap.
func NewCardinalityTrackerWithCap(seriesCap int) *CardinalityTracker {
	return NewCardinalityTrackerWithLimits(seriesCap, defaultLabelValueCap)
}

// NewCardinalityTrackerWithLimits returns an empty tracker that pins each source
// metric's distinct-series count at seriesCap and retains up to labelValueCap
// distinct VALUES per (metric,label) for the status page's label-cardinality
// views. Pass the configured OTLP cardinality limit as seriesCap so
// tailscale2otel.series.active pins exactly when a metric reaches the limit (and
// overflows into otel_metric_overflow). A non-positive seriesCap (the "unlimited
// OTLP limit" case) falls back to defaultSeriesCap as a memory guard so the
// tracker never grows unboundedly. A labelValueCap of 0 disables label-value
// capture entirely (the label-cardinality views then show nothing).
func NewCardinalityTrackerWithLimits(seriesCap, labelValueCap int) *CardinalityTracker {
	configured := seriesCap
	if seriesCap <= 0 {
		seriesCap = defaultSeriesCap
	}
	if labelValueCap < 0 {
		labelValueCap = 0
	}
	return &CardinalityTracker{
		sets:            map[string]*seriesSet{},
		seriesCap:       seriesCap,
		labelValueCap:   labelValueCap,
		configuredLimit: configured,
	}
}

// Observe records one emitted data point for the source metric name with the
// given attributes. It is a no-op on a nil tracker and for the self-metric
// itself (self-exclusion, which also prevents Report->Gauge->Observe
// recursion). Once a metric reaches the tracker's per-metric cap, further
// distinct combinations are dropped (the metric is marked capped).
func (t *CardinalityTracker) Observe(name string, attrs Attrs) {
	t.observe(name, attrs, true)
}

// ObserveSnapshot records a current observable-gauge point without retaining lifetime state.
func (t *CardinalityTracker) ObserveSnapshot(name string, attrs Attrs) {
	t.observe(name, attrs, false)
}

func (t *CardinalityTracker) observe(name string, attrs Attrs, synchronous bool) {
	if t == nil || strings.HasPrefix(name, seriesSelfPrefix) {
		return
	}
	fp := fingerprint(attrs)
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.sets[name]
	if s == nil {
		s = &seriesSet{fps: make(map[uint64]struct{})}
		t.sets[name] = s
	}

	if synchronous {
		s.sync = true
		if t.lifetime {
			if s.life == nil {
				s.life = make(map[uint64]struct{})
			}
			if len(s.life) < t.seriesCap {
				s.life[fp] = struct{}{}
			}
			// The SDK reserves one slot for overflow, so the cap-th distinct set
			// already represents the overflow point rather than another ordinary series.
			if len(s.life) >= t.seriesCap {
				s.lifeCapped = true
			}
		}
	}
	// Label-value capture is independent of the per-metric series cap: even once a
	// metric stops counting new series, its already-seen labels' value sets keep
	// their samples. It is gated on labelValueCap>0 and short-circuits per key
	// BEFORE rendering the value, so a saturated high-cardinality label costs
	// almost nothing per point.
	if t.labelValueCap > 0 {
		t.observeLabels(s, attrs)
	}
	if len(s.fps) >= t.seriesCap {
		s.capped = true
		return
	}
	s.fps[fp] = struct{}{}
}

// observeLabels records the per-key distinct values for one data point. Called
// under t.mu with labelValueCap>0. For each attribute key whose value set is
// already capped it skips before rendering the value (the hot-path guard); only
// a not-yet-capped key pays the valueString cost.
func (t *CardinalityTracker) observeLabels(s *seriesSet, attrs Attrs) {
	if len(attrs) == 0 {
		return
	}
	if s.labels == nil {
		s.labels = make(map[string]*labelValueSet, len(attrs))
	}
	for k, v := range attrs {
		lv := s.labels[k]
		if lv == nil {
			lv = &labelValueSet{values: make(map[string]struct{})}
			s.labels[k] = lv
		}
		if lv.capped {
			continue // saturated: skip the value render entirely
		}
		val := valueString(v)
		if _, ok := lv.values[val]; ok {
			continue
		}
		if len(lv.values) >= t.labelValueCap {
			lv.capped = true
			continue
		}
		lv.values[val] = struct{}{}
	}
}

// Report emits the distinct-series count and overflow state for each metric.
// Cumulative synchronous instruments retain lifetime membership; observable
// snapshots and delta instruments report interval membership. Label samples
// always reset each interval. A nil tracker is a no-op.
func (t *CardinalityTracker) Report(e Emitter) {
	if t == nil {
		return
	}

	t.mu.Lock()
	limit := t.configuredLimit
	last := make([]SeriesCount, 0, len(t.sets))
	next := make(map[string]*seriesSet)
	var labels []LabelStat
	for name, s := range t.sets {
		count, capped := len(s.fps), s.capped
		if t.lifetime && s.sync {
			count, capped = len(s.life), s.lifeCapped
			next[name] = &seriesSet{fps: make(map[uint64]struct{}), life: s.life, lifeCapped: s.lifeCapped, sync: true}
		}
		last = append(last, SeriesCount{Metric: name, Count: count, Capped: capped})
		for key, lv := range s.labels {
			labels = append(labels, labelStat(name, key, lv))
		}
	}
	// Interval observations and label samples reset; cumulative synchronous
	// membership survives quiet intervals because the SDK still exports it.
	t.sets = next
	// Stable presentation order: highest distinct first, then metric, then label.
	sort.Slice(labels, func(i, j int) bool {
		if labels[i].Distinct != labels[j].Distinct {
			return labels[i].Distinct > labels[j].Distinct
		}
		if labels[i].Metric != labels[j].Metric {
			return labels[i].Metric < labels[j].Metric
		}
		return labels[i].Label < labels[j].Label
	})
	t.lastLabels = labels
	// Stable, presentation-friendly order: highest cardinality first, then name.
	// Retained for Snapshot; emission order is irrelevant.
	sort.Slice(last, func(i, j int) bool {
		if last[i].Count != last[j].Count {
			return last[i].Count > last[j].Count
		}
		return last[i].Metric < last[j].Metric
	})
	t.last = last
	t.mu.Unlock()

	// A configured limit <=0 means the SDK is unlimited (no real otel_metric_overflow):
	// suppress series.limit and force overflowing to 0 even if the memory-guard cap was hit.
	limited := limit > 0

	for _, en := range last {
		e.Gauge(docSeriesActive.Name, docSeriesActive.Unit, docSeriesActive.Description,
			float64(en.Count), Attrs{semconv.AttrMetricName: en.Metric})
		overflowing := 0.0
		if limited && en.Capped {
			overflowing = 1
		}
		e.Gauge(docSeriesOverflowing.Name, docSeriesOverflowing.Unit, docSeriesOverflowing.Description,
			overflowing, Attrs{semconv.AttrMetricName: en.Metric})
	}
	if limited {
		e.Gauge(docSeriesLimit.Name, docSeriesLimit.Unit, docSeriesLimit.Description, float64(limit), nil)
	}
}

// LabelSnapshot returns the per-(metric,label) distinct-value stats from the
// last completed export interval, sorted by distinct desc then metric then
// label. It returns nil before the first Report and is a no-op (nil) on a nil
// receiver. The returned slice (and its Examples) is a copy the caller may
// retain or mutate.
func (t *CardinalityTracker) LabelSnapshot() []LabelStat {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.lastLabels == nil {
		return nil
	}
	out := make([]LabelStat, len(t.lastLabels))
	for i, ls := range t.lastLabels {
		out[i] = ls
		out[i].Examples = append([]string(nil), ls.Examples...)
	}
	return out
}

// labelStat snapshots one (metric,label) value set into a LabelStat with sorted
// example values.
func labelStat(metric, label string, lv *labelValueSet) LabelStat {
	examples := make([]string, 0, len(lv.values))
	for v := range lv.values {
		examples = append(examples, v)
	}
	sort.Strings(examples)
	return LabelStat{
		Metric:   metric,
		Label:    label,
		Distinct: len(lv.values),
		Capped:   lv.capped,
		Examples: examples,
	}
}

// Snapshot returns the per-source-metric active-series counts from the last
// completed export interval (the most recent Report), sorted by count desc then
// metric name. It returns nil before the first Report and is a no-op (nil) on a
// nil receiver. The returned slice is a copy the caller may retain or mutate.
func (t *CardinalityTracker) Snapshot() []SeriesCount {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.last == nil {
		return nil
	}
	out := make([]SeriesCount, len(t.last))
	copy(out, t.last)
	return out
}

// valueString renders an attribute value to the string form used as a distinct
// label-value key. It mirrors the type handling of fingerprint so the two agree
// on what counts as a distinct value. Unknown types render as "".
func valueString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case int:
		return strconv.FormatInt(int64(t), 10)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	case []string:
		return strings.Join(t, ",")
	default:
		return ""
	}
}

// fingerprint computes a deterministic, low-allocation 64-bit hash of an
// attribute set. Map iteration order is randomized, so the keys are sorted
// first; the value is then folded in with an inline FNV-1a 64-bit hash using
// per-field (0x1f) and per-pair (0x1e) separators to keep distinct attribute
// sets from colliding via concatenation.
func fingerprint(attrs Attrs) uint64 {
	const (
		offset uint64 = 1469598103934665603
		prime  uint64 = 1099511628211
	)
	h := offset
	if len(attrs) == 0 {
		return h
	}

	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	writeString := func(s string) {
		for i := 0; i < len(s); i++ {
			h ^= uint64(s[i])
			h *= prime
		}
	}
	writeByte := func(b byte) {
		h ^= uint64(b)
		h *= prime
	}

	for _, k := range keys {
		writeString(k)
		writeByte(0x1f)
		switch v := attrs[k].(type) {
		case string:
			writeString(v)
		case bool:
			if v {
				writeByte('1')
			} else {
				writeByte('0')
			}
		case int:
			writeString(strconv.FormatInt(int64(v), 10))
		case int64:
			writeString(strconv.FormatInt(v, 10))
		case float64:
			writeString(strconv.FormatFloat(v, 'g', -1, 64))
		case []string:
			for i, s := range v {
				if i > 0 {
					writeByte(0x1f)
				}
				writeString(s)
			}
		}
		writeByte(0x1e)
	}
	return h
}
