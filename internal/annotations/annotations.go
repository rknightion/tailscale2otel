// Package annotations publishes a curated, closed set of tailnet events into
// Grafana as annotations, so a dashboard can answer "what changed at 14:00"
// from tailscale2otel itself rather than from an external automation (#518).
//
// # It is the one outbound WRITE, and it is opt-in
//
// Everything else this process does is read-only polling plus OTLP push.
// Setting grafana_annotations.url is the whole opt-in: unset, nothing here is
// constructed, no goroutine runs and no line is logged. When it IS set, this
// package speaks exactly one HTTP call to exactly one destination —
// POST /api/annotations — and the path is a package constant rather than a
// parameter, so a caller cannot point the client somewhere else. The token
// needs one Grafana action, annotations:create, and nothing here asks for,
// uses, or would benefit from another.
//
// # It publishes what collectors already emit; it polls nothing
//
// There is no Tailscale client in this package and no new API call anywhere in
// it. Annotations are derived by teeing the telemetry.Emitter the collectors
// already emit through (see Tee), which is the only boundary with nothing
// behind it — so a collector added later is covered for free and cannot escape
// it.
//
// # Failure is isolated, except at startup
//
// Nothing here can block or fail collection: Publish never blocks, a full queue
// drops and counts, and a Grafana outage becomes a counter and the
// tailscale2otel.annotation.degraded gauge rather than a poll error. The ONE
// place failure is fatal is startup, where preflight writes the lifecycle
// marker synchronously before any collector runs. A token that cannot write is
// then reported at deploy time instead of being discovered during an incident,
// when the context an operator came looking for was never there.
//
// # No suppressed attribute and no secret reaches Grafana
//
// Annotation text is built from a per-rule ALLOW-LIST of attribute keys over
// the PII-REDACTED view of the record, never from the whole attribute map. Both
// halves matter: the allow-list stops a field added to a source record later
// from silently riding out, and the redaction is required because this tee
// wraps OUTSIDE otelEmitter, where pii_filter is applied — so the records it
// observes are raw. See Recorder.
package annotations

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Category is the closed set of annotation categories. Two are curated domain
// categories; Lifecycle is tailscale2otel's own start marker, which doubles as
// the startup write probe.
type Category string

const (
	// CategoryConfigChange is "what changed at 14:00": the curated,
	// security-and-lifecycle-relevant subset of the Tailscale configuration
	// audit log — ACL edits, device approval and churn, key lifecycle, user
	// role changes, DNS and tailnet settings.
	CategoryConfigChange Category = "config_change"
	// CategoryExpiry is a node key or auth key entering its expiry warning
	// window. It is the annotation that explains a device count stepping down.
	CategoryExpiry Category = "expiry"
	// CategoryLifecycle is tailscale2otel's own process start marker.
	CategoryLifecycle Category = "lifecycle"
)

// Categories returns every category in a stable order, so a test can enumerate
// the closed set instead of restating it.
func Categories() []Category {
	return []Category{CategoryConfigChange, CategoryExpiry, CategoryLifecycle}
}

// Annotation is one annotation ready to publish.
type Annotation struct {
	// Category is the curated category this annotation belongs to.
	Category Category
	// Tailnet is the tailnet label the annotated event belongs to. Empty in
	// single-tailnet deployments, where there is nothing to disambiguate.
	Tailnet string
	// RuleID names the curated rule that produced it. It is part of the dedupe
	// key and is published as a tag, so an operator can tell two rules of one
	// category apart without parsing the text.
	RuleID string
	// Time is the annotation's event time — the SOURCE record's timestamp
	// wherever it carries one, never arrival time. An annotation whose whole
	// job is lining up against a metric step is worthless misdated.
	Time time.Time
	// TimeEnd, when non-zero, makes this a REGION annotation spanning
	// [Time, TimeEnd]. Rollups use it so the marker covers the interval it
	// summarizes rather than pretending everything happened at one instant.
	TimeEnd time.Time
	// Text is the annotation body, built from a rule's Detail allow-list over
	// the PII-redacted attribute view.
	Text string
	// DedupeKey is the stable identity of the underlying occurrence. Two
	// deliveries of one event derive the same key, so the second is dropped.
	DedupeKey string
	// Severity is an optional bounded severity, published as a tag when set.
	Severity string
}

// The annotation tag contract. These are consumed by a Grafana dashboard's
// annotation query, so they are a published interface: renaming one silently
// stops every existing annotation query from matching, which on a dashboard
// looks exactly like "tailscale2otel stopped publishing annotations".
const (
	// TagRoot is on EVERY annotation this process writes and is the selector a
	// dashboard annotation query should use.
	TagRoot = "tailscale2otel"
	// TagTailnetPrefix + the tailnet label. Omitted in single-tailnet mode,
	// where every annotation would carry the same value.
	TagTailnetPrefix = "tailnet:"
	// TagCategoryPrefix + one of Categories().
	TagCategoryPrefix = "category:"
	// TagRulePrefix + a curated rule id.
	TagRulePrefix = "rule:"
	// TagSeverityPrefix + a bounded severity from the source record.
	TagSeverityPrefix = "severity:"
	// TagRollup marks an interval rollup rather than a single occurrence.
	TagRollup = "rollup"
)

// Tags returns the annotation's Grafana tag set — the public contract a
// dashboard annotation query selects on.
//
// It is deliberately a small, closed, low-cardinality set. Grafana indexes
// annotation tags, so a tag carrying a device name, an address or a hash would
// grow the tag store forever without ever being queried. Everything
// identifying goes in the text, which is not indexed.
func (a Annotation) Tags() []string {
	tags := []string{TagRoot}
	if a.Tailnet != "" {
		tags = append(tags, TagTailnetPrefix+a.Tailnet)
	}
	tags = append(tags,
		TagCategoryPrefix+string(a.Category),
		TagRulePrefix+a.RuleID,
	)
	if a.Severity != "" {
		tags = append(tags, TagSeverityPrefix+a.Severity)
	}
	if !a.TimeEnd.IsZero() {
		tags = append(tags, TagRollup)
	}
	return tags
}

// dedupeDomain domain-separates and VERSIONS the dedupe hash: a key can never
// collide with a digest computed elsewhere, and changing what the key covers is
// a breaking change for a deployment mid-flight. Bumping /v1 makes every key
// move once — republishing recent annotations exactly once, which is the honest
// cost of redefining identity.
const dedupeDomain = "tailscale2otel/annotation-dedupe/v1\x00"

// dedupeHexLen is how many hex characters of the digest form the key. 32 hex
// characters is 128 bits: collision-free at any rate a tailnet can produce, and
// short enough that the persisted set stays small.
const dedupeHexLen = 32

// DedupeKey derives the stable identity of one annotatable occurrence.
//
// It is a pure function of (tailnet, rule, source identity) and NOTHING else —
// no clock, no counter, no process identity. That is the whole property being
// bought: the same record re-delivered by an overlapping poll window, or
// re-observed on the next snapshot tick, or seen again after a restart, derives
// the same key and is dropped. A key that varied with arrival time would make
// every duplicate look like a second real event, which once it is in Grafana is
// indistinguishable from the truth.
//
// Identity values are hashed, never published, which is why callers pass the
// RAW attribute values here while rendering text from the redacted view.
func DedupeKey(tailnet, ruleID string, identity ...string) string {
	h := sha256.New()
	h.Write([]byte(dedupeDomain))
	// Length-prefix every component so ("a","bc") and ("ab","c") cannot collide.
	for _, part := range append([]string{tailnet, ruleID}, identity...) {
		_, _ = fmt.Fprintf(h, "%d:", len(part))
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:dedupeHexLen]
}

// summaryLimit bounds how many distinct titles a rolled-up annotation names.
// Beyond it the text says how many more there were: a rollup exists to keep a
// dashboard readable, and an unbounded list in the tooltip defeats it.
const summaryLimit = 5

// summarize renders a bounded "N x title" summary from a title histogram, in
// descending count order with the title as a stable tiebreak.
func summarize(counts map[string]int) string {
	type entry struct {
		title string
		n     int
	}
	entries := make([]entry, 0, len(counts))
	for title, n := range counts {
		entries = append(entries, entry{title: title, n: n})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].n != entries[j].n {
			return entries[i].n > entries[j].n
		}
		return entries[i].title < entries[j].title
	})

	var b strings.Builder
	for i, e := range entries {
		if i == summaryLimit {
			_, _ = fmt.Fprintf(&b, "; +%d more", len(entries)-summaryLimit)
			break
		}
		if i > 0 {
			b.WriteString("; ")
		}
		if e.n > 1 {
			b.WriteString(strconv.Itoa(e.n) + "x ")
		}
		b.WriteString(e.title)
	}
	return b.String()
}
