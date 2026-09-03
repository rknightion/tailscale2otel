package annotations

import (
	"fmt"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry/pii"
)

// Sink accepts annotations the Recorder decided to publish. Publisher is the
// production implementation; a test uses a slice.
//
// Publish MUST NOT block and MUST NOT return an error. That is not a
// convenience: the caller is a collector goroutine mid-poll, and an annotation
// writer that can make a Tailscale poll wait on Grafana has turned a dashboard
// nicety into a collection dependency.
type Sink interface {
	Publish(Annotation)
	// Duplicate reports one occurrence suppressed because its dedupe key was
	// already claimed. It is counted rather than logged: on a snapshot source
	// the steady state is every record being a duplicate.
	Duplicate(tailnet string)
}

// CategoryConfig is one category's gate.
type CategoryConfig struct {
	Enabled bool
	Rollup  bool
}

// RecorderConfig is the Recorder's resolved behavior.
type RecorderConfig struct {
	// Categories gates each curated category. A category absent from the map
	// is OFF: a category added to the code but not to config defaults would
	// otherwise start publishing on upgrade without anyone asking for it.
	Categories map[Category]CategoryConfig
	// RollupInterval is the bucket width for rolled-up categories.
	RollupInterval time.Duration
}

// Recorder turns emitted records into annotations: it matches the curated
// rules, applies dedupe, and buckets the categories configured to roll up. It
// is safe for concurrent use — every collector goroutine for every tailnet
// emits through it.
type Recorder struct {
	cfg    RecorderConfig
	sink   Sink
	now    func() time.Time
	dedupe *dedupeStore

	// redactor applies the operator's pii_filter to the attributes before they
	// are rendered into annotation text.
	//
	// This is NOT belt-and-braces. The tee wraps OUTSIDE otelEmitter, which is
	// where pii_filter is applied (internal/telemetry/emitter.go, LogEventCtx),
	// so the records this Recorder observes are RAW. Without this, an operator
	// who disabled a category would still see those values published to
	// Grafana — a suppression that silently only covers OTLP.
	//
	// Match and Identity deliberately read the raw attributes instead: identity
	// values are hashed into the dedupe key and never published, and matching
	// on a redacted view would silently stop annotating whenever an unrelated
	// category was suppressed.
	redactor *pii.Redactor

	// byEvent indexes the closed rule set by source event name, so a record
	// that is not annotatable costs one map lookup on the emit path.
	byEvent map[string][]Rule

	mu sync.Mutex
	// buckets holds the open rollup buckets.
	buckets map[bucketKey]*bucket
}

// RecorderOptions are the Recorder's dependencies. Now defaults to time.Now.
type RecorderOptions struct {
	Config   RecorderConfig
	Sink     Sink
	Dedupe   *dedupeStore
	Redactor *pii.Redactor
	Now      func() time.Time
}

// NewRecorder builds a Recorder over the curated rule set.
func NewRecorder(opts RecorderOptions) *Recorder {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	redactor := opts.Redactor
	if redactor == nil {
		// pii.New(nil) is the all-categories-on no-op fast path, matching what
		// an emitter built without a filter does.
		redactor = pii.New(nil)
	}
	byEvent := map[string][]Rule{}
	for _, rule := range Rules() {
		byEvent[rule.EventName] = append(byEvent[rule.EventName], rule)
	}
	return &Recorder{
		cfg:      opts.Config,
		sink:     opts.Sink,
		now:      now,
		dedupe:   opts.Dedupe,
		redactor: redactor,
		byEvent:  byEvent,
		buckets:  map[bucketKey]*bucket{},
	}
}

// enabled reports whether a category is switched on.
func (r *Recorder) enabled(category Category) bool {
	// Lifecycle has no gate: it is the startup write probe, and a toggle's only
	// real effect would be a deployment whose markers silently stop.
	if category == CategoryLifecycle {
		return true
	}
	return r.cfg.Categories[category].Enabled
}

// rollsUp reports whether a category aggregates per interval.
func (r *Recorder) rollsUp(category Category) bool {
	if category == CategoryLifecycle {
		return false
	}
	return r.cfg.Categories[category].Rollup && r.cfg.RollupInterval > 0
}

// ObserveEvent offers one emitted log record to the curated rule set.
//
// It never mutates ev, never returns an error and never drops the record from
// the telemetry pipeline — the caller (Tee) has already forwarded it. The worst
// case here is no annotation.
func (r *Recorder) ObserveEvent(tailnet string, ev telemetry.Event) {
	rules, ok := r.byEvent[ev.Name]
	if !ok {
		return
	}
	eventTime := ev.Timestamp
	if eventTime.IsZero() {
		// A snapshot source carries no event time (the SDK stamps arrival).
		// Observation time is then the only honest answer, and unlike a log
		// record it cannot misdate anything a query joins on — an annotation
		// with no time cannot exist at all.
		eventTime = r.now()
	}

	// Redacted once per record rather than per rule: two rules on one event
	// name would otherwise pay for it twice.
	var redacted telemetry.Attrs

	for _, rule := range rules {
		if !r.enabled(rule.Category) {
			continue
		}
		if rule.Match != nil && !rule.Match(ev.Attrs) {
			continue
		}
		key := DedupeKey(tailnet, rule.ID, rule.Identity(ev.Attrs, eventTime)...)
		if !r.dedupe.Claim(rule.ID, key, eventTime) {
			r.sink.Duplicate(tailnet)
			continue
		}
		if redacted == nil {
			redacted = telemetry.Attrs(r.redactor.Log(ev.Attrs))
		}
		r.emit(Annotation{
			Category:  rule.Category,
			Tailnet:   tailnet,
			RuleID:    rule.ID,
			Time:      eventTime,
			Text:      renderText(rule, redacted),
			DedupeKey: key,
			Severity:  attrString(redacted, rule.SeverityAttr),
		}, rule.Title(redacted))
	}
}

// emit routes an annotation either straight to the sink or into its category's
// rollup bucket.
func (r *Recorder) emit(a Annotation, title string) {
	if !r.rollsUp(a.Category) {
		r.sink.Publish(a)
		return
	}
	start := a.Time.Truncate(r.cfg.RollupInterval).UTC()
	k := bucketKey{tailnet: a.Tailnet, category: a.Category, start: start}
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.buckets[k]
	if !ok {
		b = &bucket{titles: map[string]int{}}
		r.buckets[k] = b
	}
	b.count++
	if title == "" {
		title = a.RuleID
	}
	b.titles[title]++
}

// Flush publishes every rollup bucket whose interval has closed by now. It is
// driven by the publisher's ticker.
func (r *Recorder) Flush(now time.Time) { r.flush(now, false) }

// FlushAll publishes every open bucket regardless of whether its interval has
// closed, so a shutdown does not silently discard the open one.
func (r *Recorder) FlushAll() { r.flush(r.now(), true) }

func (r *Recorder) flush(now time.Time, all bool) {
	interval := r.cfg.RollupInterval
	if interval <= 0 {
		return
	}
	r.mu.Lock()
	type due struct {
		key bucketKey
		b   *bucket
	}
	var ready []due
	for k, b := range r.buckets {
		if all || !now.Before(k.start.Add(interval)) {
			ready = append(ready, due{key: k, b: b})
			delete(r.buckets, k)
		}
	}
	r.mu.Unlock()

	for _, d := range ready {
		if d.b.count == 0 {
			continue
		}
		ruleID := RollupRuleID(d.key.category)
		key := DedupeKey(d.key.tailnet, ruleID, d.key.start.UTC().Format(time.RFC3339))
		if !r.dedupe.Claim(ruleID, key, d.key.start) {
			r.sink.Duplicate(d.key.tailnet)
			continue
		}
		r.sink.Publish(Annotation{
			Category:  d.key.category,
			Tailnet:   d.key.tailnet,
			RuleID:    ruleID,
			Time:      d.key.start,
			TimeEnd:   d.key.start.Add(interval),
			Text:      fmt.Sprintf("%d %s events: %s", d.b.count, d.key.category, summarize(d.b.titles)),
			DedupeKey: key,
		})
	}
}

// RollupRuleID is the rule id a rolled-up annotation carries. It is distinct
// from every member rule's id so a dashboard query can select either the
// per-event markers or the interval summaries, and so a rollup's dedupe key can
// never collide with a member's.
func RollupRuleID(category Category) string { return string(category) + ".rollup" }

type bucketKey struct {
	tailnet  string
	category Category
	start    time.Time
}

type bucket struct {
	count  int
	titles map[string]int
}
