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

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
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

	// policy, when set, receives the raw document so the flow view can
	// reconcile observed traffic against it.
	policy PolicySink

	// validator, when non-nil, is called each Collect to validate the
	// tailnet's currently active ACL policy (#428). A nil validator (the
	// default — no WithValidator option applied) is a clean no-op: no
	// tailscale.acl.validation.* signal and no tailscale2otel.api.availability
	// entry for validateAndTestPolicyFile are ever emitted. This keeps the
	// dependency optional and Headscale-safe: provider.ControlPlane (shared
	// with the Headscale adapter, which has no validate endpoint) never gains
	// a ValidatePolicyFile method.
	validator Validator
	// validate gates emission when a validator IS wired (config.AclCollector.
	// Validate, default true — the off switch for an otherwise-capable
	// deployment). Defaults to true in New so a validator wired without an
	// explicit WithValidate call still validates.
	validate bool
	// tracker records the validate operation's latest availability state for the
	// admin status page and the configuration-to-capability matrix (#430). A nil
	// tracker is a no-op, so the collector still works without one.
	tracker *apistate.Tracker
}

// New returns an ACL collector. A non-positive interval resolves to the default
// (600s) via DefaultInterval. A nil now defaults to time.Now.
func New(a api, interval time.Duration, now func() time.Time, opts ...Option) *Collector {
	if now == nil {
		now = time.Now
	}
	c := &Collector{api: a, interval: interval, now: now, validate: true}
	for _, o := range opts {
		o(c)
	}
	return c
}

// PolicySink receives the raw HuJSON policy document each collect, so the flow
// view can reconcile observed traffic against it. It is the narrow slice of
// aclpolicy.Store this collector needs.
type PolicySink interface {
	SetDocument([]byte) error
}

// Option configures optional Collector behavior.
type Option func(*Collector)

// WithPolicySink publishes the collected policy document to sink.
func WithPolicySink(sink PolicySink) Option {
	return func(c *Collector) { c.policy = sink }
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

	e.Gauge(docACLLastChanged.Name, docACLLastChanged.Unit, docACLLastChanged.Description,
		float64(c.lastChanged.Unix()), nil)

	// Trivial presence/size signal: bytes of the raw HuJSON policy document.
	e.Gauge(docACLSize.Name, docACLSize.Unit, docACLSize.Description,
		float64(len(raw.HuJSON)), nil)

	if c.policy != nil {
		// A compile failure is swallowed on purpose: the evaluator is a side
		// consumer and a malformed policy must not fail the acl collect, which
		// still has useful signals to emit. The sink retains the error and keeps
		// serving the last policy that compiled; the status page reports it.
		_ = c.policy.SetDocument([]byte(raw.HuJSON))
	}

	// Per-section counts and risk scores both need the policy parsed. Parse
	// once. If parsing fails (malformed document) both are skipped, but size
	// and last_changed are still emitted above and the collect does not error.
	top, ok := standardizeTop(raw.HuJSON)
	if !ok {
		return nil
	}
	c.emitRuleCounts(e, top)
	c.emitRiskScores(e, top)

	c.collectValidation(ctx, e, raw.HuJSON)

	return nil
}

// standardizeTop standardizes the HuJSON policy and unmarshals its top level
// into a section map. It returns ok=false on any HuJSON/JSON parse failure so
// callers can skip parsed-data signals while still emitting size/last_changed.
func standardizeTop(hujsonDoc string) (map[string]json.RawMessage, bool) {
	std, err := hujson.Standardize([]byte(hujsonDoc))
	if err != nil {
		return nil, false
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(std, &top); err != nil {
		return nil, false
	}
	return top, true
}

// emitRuleCounts emits one tailscale.acl.rules gauge per recognized section
// present in the parsed policy. A section's value is the element count when it
// is a JSON array, or the key count when it is a JSON object. Sections that are
// absent or encoded as a scalar are skipped.
func (c *Collector) emitRuleCounts(e telemetry.Emitter, top map[string]json.RawMessage) {
	for _, section := range recognizedSections {
		raw, ok := top[section]
		if !ok {
			continue
		}
		size, ok := sectionSize(raw)
		if !ok {
			continue
		}
		e.Gauge(docACLRules.Name, docACLRules.Unit, docACLRules.Description,
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
