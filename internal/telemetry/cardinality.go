package telemetry

import (
	"sort"
	"strconv"
	"sync"

	"github.com/rknightion/tailscale2otel/internal/semconv"
)

// seriesActiveMetric is the self-observability metric name for the per-source-
// metric active-time-series count. It is excluded from tracking so the tracker
// never measures itself (and Report->Gauge->Observe cannot recurse).
const seriesActiveMetric = "tailscale2otel.series.active"

// defaultSeriesCap bounds the distinct fingerprints tracked per source metric.
// Once a metric reaches the cap the reported value pins at the cap (a visible
// signal that the true cardinality is at least this high) and further distinct
// series for that metric are not counted, bounding memory.
const defaultSeriesCap = 10000

// seriesSet is the distinct fingerprint set for one source metric within the
// current export interval. capped records whether the per-metric cap was hit so
// the reported value can be pinned at the cap.
type seriesSet struct {
	fps    map[uint64]struct{}
	capped bool
}

// SeriesCount is the distinct active-series count for one source metric during
// the last completed export interval. Capped is true when the per-metric cap was
// hit, in which case Count is pinned at defaultSeriesCap.
type SeriesCount struct {
	Metric string
	Count  int
	Capped bool
}

// CardinalityTracker counts the EXACT number of distinct attribute combinations
// (time series) emitted per source metric within an export interval. Observe is
// called from the emit hot path for every metric data point; Report snapshots
// the per-metric distinct counts, resets the sets, and emits the
// tailscale2otel.series.active gauge once per source metric. The same per-metric
// counts are retained for the most recent interval and exposed via Snapshot for
// in-process introspection (e.g. the admin status page).
//
// All methods are safe for concurrent use and are no-ops on a nil receiver.
type CardinalityTracker struct {
	mu   sync.Mutex
	sets map[string]*seriesSet
	last []SeriesCount // counts from the most recent Report; nil before the first
}

// NewCardinalityTracker returns an empty tracker.
func NewCardinalityTracker() *CardinalityTracker {
	return &CardinalityTracker{sets: map[string]*seriesSet{}}
}

// Observe records one emitted data point for the source metric name with the
// given attributes. It is a no-op on a nil tracker and for the self-metric
// itself (self-exclusion, which also prevents Report->Gauge->Observe
// recursion). Once a metric reaches defaultSeriesCap distinct series, further
// distinct combinations are dropped (the metric is marked capped).
func (t *CardinalityTracker) Observe(name string, attrs Attrs) {
	if t == nil || name == seriesActiveMetric {
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
	if len(s.fps) >= defaultSeriesCap {
		s.capped = true
		return
	}
	s.fps[fp] = struct{}{}
}

// Report emits one tailscale2otel.series.active gauge per source metric observed
// since the previous Report, carrying the EXACT distinct-series count (pinned at
// defaultSeriesCap when the cap was hit), then resets all sets so the next
// interval measures active-per-interval cardinality afresh (a source metric that
// stops emitting drops out rather than lingering at a stale value). It is a
// no-op on a nil tracker.
func (t *CardinalityTracker) Report(e Emitter) {
	if t == nil {
		return
	}

	t.mu.Lock()
	last := make([]SeriesCount, 0, len(t.sets))
	for name, s := range t.sets {
		last = append(last, SeriesCount{Metric: name, Count: len(s.fps), Capped: s.capped})
	}
	// Replace (rather than clear) so the next interval starts empty and metrics
	// that stopped emitting are dropped.
	t.sets = map[string]*seriesSet{}
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

	for _, en := range last {
		e.Gauge(docSeriesActive.Name, docSeriesActive.Unit, docSeriesActive.Description,
			float64(en.Count), Attrs{semconv.AttrMetricName: en.Metric})
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
