// Package acl is a snapshot collector for the tailnet ACL policy file. It is
// stateful: it remembers the last-seen ETag so it can report when the policy
// last changed (tailscale.acl.last_changed, Unix seconds). It also reports the
// raw document size (tailscale.acl.size) and per-section rule counts
// (tailscale.acl.rules) obtained by standardizing the HuJSON policy and
// counting each recognized section.
package acl

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tailscale/hujson"
	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

const defaultInterval = 600 * time.Second

// Metric names emitted by this collector.
const (
	metricLastChanged = "tailscale.acl.last_changed"
	metricSize        = "tailscale.acl.size"
	metricRules       = "tailscale.acl.rules"
)

// attrSection is the attribute key carrying the ACL policy section name on the
// tailscale.acl.rules metric (e.g. "acls", "grants", "tagOwners").
const attrSection = "tailscale.acl.section"

// recognizedSections lists the top-level ACL policy sections for which a
// per-section rule count is emitted. Sections may be encoded as a JSON array
// (counted by element) or a JSON object (counted by key); both forms are
// handled. Order is fixed for deterministic emission.
var recognizedSections = []string{
	"acls",
	"grants",
	"ssh",
	"tests",
	"postures",
	"autoApprovers",
	"tagOwners",
	"hosts",
	"groups",
	"nodeAttrs",
}

// api is the narrow slice of the Tailscale client this collector needs. It is
// satisfied by *tsapi.Client.
type api interface {
	PolicyFileRaw(ctx context.Context) (*tsclient.RawACL, error)
}

// Collector implements collector.SnapshotCollector for the ACL policy file.
// It keeps state across ticks: the last-seen ETag and the wall-clock time at
// which that ETag was first observed.
type Collector struct {
	api      api
	interval time.Duration
	now      func() time.Time

	lastETag    string
	haveETag    bool
	lastChanged time.Time
}

// New returns an ACL collector. A non-positive interval resolves to the default
// (600s) via DefaultInterval. A nil now defaults to time.Now.
func New(a api, interval time.Duration, now func() time.Time) *Collector {
	if now == nil {
		now = time.Now
	}
	return &Collector{api: a, interval: interval, now: now}
}

// Name returns the stable collector identifier.
func (c *Collector) Name() string { return "acl" }

// DefaultInterval returns the configured interval, or 600s when unset.
func (c *Collector) DefaultInterval() time.Duration {
	if c.interval > 0 {
		return c.interval
	}
	return defaultInterval
}

// Collect fetches the raw ACL and emits the last-changed timestamp. When the
// ETag differs from the previously stored one (including the first observation)
// it records now() as the change time; otherwise it keeps the prior value.
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	raw, err := c.api.PolicyFileRaw(ctx)
	if err != nil {
		return err
	}

	if !c.haveETag || raw.ETag != c.lastETag {
		c.lastETag = raw.ETag
		c.haveETag = true
		c.lastChanged = c.now()
	}

	e.Gauge(metricLastChanged, semconv.UnitSeconds, "Unix time the ACL policy last changed (by ETag)",
		float64(c.lastChanged.Unix()), nil)

	// Trivial presence/size signal: bytes of the raw HuJSON policy document.
	e.Gauge(metricSize, semconv.UnitBytes, "size of the raw HuJSON ACL document in bytes",
		float64(len(raw.HuJSON)), nil)

	// Per-section rule counts require parsing the HuJSON policy. If parsing
	// fails (malformed document) the rule counts are skipped, but size and
	// last_changed are still emitted above and the collect does not error.
	c.emitRuleCounts(e, raw.HuJSON)

	return nil
}

// emitRuleCounts standardizes the HuJSON policy and emits one
// tailscale.acl.rules gauge per recognized section that is present. A section's
// value is the element count when it is a JSON array, or the key count when it
// is a JSON object. Sections that are absent or encoded as a scalar are
// skipped. Parse failures are silently ignored so the rest of the collect
// remains intact.
func (c *Collector) emitRuleCounts(e telemetry.Emitter, hujsonDoc string) {
	std, err := hujson.Standardize([]byte(hujsonDoc))
	if err != nil {
		return
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(std, &top); err != nil {
		return
	}

	for _, section := range recognizedSections {
		raw, ok := top[section]
		if !ok {
			continue
		}
		size, ok := sectionSize(raw)
		if !ok {
			continue
		}
		e.Gauge(metricRules, semconv.UnitDimensionless,
			"number of rules/entries in an ACL policy section",
			float64(size), telemetry.Attrs{attrSection: section})
	}
}

// sectionSize returns the size of a top-level ACL section and whether it is a
// countable container. JSON arrays return their length; JSON objects return
// their key count. Any other JSON value (scalar, null) reports ok=false.
func sectionSize(raw json.RawMessage) (int, bool) {
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		return len(arr), true
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err == nil {
		return len(obj), true
	}
	return 0, false
}
