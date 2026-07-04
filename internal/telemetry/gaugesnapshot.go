package telemetry

// GaugeSnapshotBuilder accumulates the current collection tick's GaugePoints per
// gauge and flushes each via Emitter.GaugeSnapshot, so churning per-entity
// gauges drop departed series instead of ghosting (#55). It is the
// collector-side companion to GaugeSnapshot: a collector holds one across its
// lifetime, Adds points during each Collect, then Flushes at the end.
//
// Crucially, Flush emits EVERY gauge the builder has ever seen — not only those
// Added this tick — passing an empty snapshot for any that produced no points,
// so a series that stops being emitted (its entity left the tailnet, or a
// per-entity condition no longer holds) is cleared rather than left to ghost.
// The builder retains only the (small, bounded) set of metric NAMES it has
// seen, never their attribute sets, so its memory does not grow with entity
// churn.
//
// Not safe for concurrent use. A collector's Collect runs on a single scheduler
// goroutine (collectors never run concurrently with themselves), so no
// synchronization is needed; a single builder per collector is the intended use.
type GaugeSnapshotBuilder struct {
	order []string // metric names in first-seen order, for deterministic flush
	meta  map[string]gaugeMeta
	pts   map[string][]GaugePoint // reset each Flush
}

type gaugeMeta struct{ unit, desc string }

// NewGaugeSnapshotBuilder returns an empty builder ready for the first Collect.
func NewGaugeSnapshotBuilder() *GaugeSnapshotBuilder {
	return &GaugeSnapshotBuilder{
		meta: map[string]gaugeMeta{},
		pts:  map[string][]GaugePoint{},
	}
}

// Add appends one series (value + attrs) to the named gauge for the current
// tick, registering the gauge with its unit/description the first time the name
// is seen. Pass the same unit/desc for a given name on every call — they are
// honored on first registration only (the underlying observable instrument is
// created once).
func (b *GaugeSnapshotBuilder) Add(name, unit, desc string, value float64, attrs Attrs) {
	if _, ok := b.meta[name]; !ok {
		b.order = append(b.order, name)
		b.meta[name] = gaugeMeta{unit: unit, desc: desc}
	}
	b.pts[name] = append(b.pts[name], GaugePoint{Value: value, Attrs: attrs})
}

// Flush emits a GaugeSnapshot for every gauge the builder has ever seen — an
// empty snapshot for any with no points this tick, which clears its prior
// series — then resets the per-tick points for the next Collect. Call it once
// at the end of each Collect after all Add calls.
func (b *GaugeSnapshotBuilder) Flush(e Emitter) {
	for _, name := range b.order {
		m := b.meta[name]
		e.GaugeSnapshot(name, m.unit, m.desc, b.pts[name])
	}
	b.pts = make(map[string][]GaugePoint, len(b.order))
}
