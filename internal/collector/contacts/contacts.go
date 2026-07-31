// Package contacts is a snapshot collector for the tailnet's account/support/
// security contacts. It emits one gauge per contact type indicating whether the
// contact email still needs verification (an unverified security contact means
// security mail may not be delivered). The email address itself is never emitted.
package contacts

import (
	"context"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
)

// Compile-time assertion: *Collector is a SnapshotCollector.
var _ collector.SnapshotCollector = (*Collector)(nil)

const defaultInterval = 600 * time.Second

// metricNeedsVerification is the per-contact-type verification gauge.
const metricNeedsVerification = "tailscale.contact.needs_verification"

// attrContactType labels a point with the contact type (account/support/security).
const attrContactType = "tailscale.contact.type"

// opGetContacts is the upstream operationId of the contacts fetch call.
const opGetContacts = "getContacts"

// contactsDisposition is the DEFAULT disposition (#420/#524): 403 stays
// scope_denied. Contacts are available on every plan, so upstream has no
// reason to answer 403 as a feature gate here — a 403 on this path means the
// credential is missing the contacts read scope, and reading it as "disabled"
// would hide exactly that.
var contactsDisposition = apistate.Disposition{}

// api is the narrow slice of the Tailscale client this collector needs. It is
// satisfied by *tsapi.Client.
type api interface {
	Contacts(ctx context.Context) (*tsclient.Contacts, error)
}

// Collector implements collector.SnapshotCollector for tailnet contacts.
type Collector struct {
	api      api
	interval time.Duration
	// tracker records this collector's per-operation availability for the admin
	// status page and the capability matrix (#430/#524). A nil tracker is a no-op.
	tracker *apistate.Tracker
	// now is the clock, injectable from tests.
	now func() time.Time
}

// Option configures optional Collector behavior.
type Option func(*Collector)

// WithAPIState wires the shared per-operation availability tracker (#420).
// Availability METRICS are emitted regardless; the tracker is the in-process
// introspection copy the admin status page reads. A nil tracker is a no-op.
func WithAPIState(t *apistate.Tracker) Option { return func(c *Collector) { c.tracker = t } }

// WithClock overrides the collector's clock (for deterministic last-probe
// timestamp tests); the default is time.Now.
func WithClock(now func() time.Time) Option {
	return func(c *Collector) { c.now = now }
}

// New returns a contacts collector. A non-positive interval resolves to the
// default (600s) via DefaultInterval.
func New(a api, interval time.Duration, opts ...Option) *Collector {
	c := &Collector{api: a, interval: interval, now: time.Now}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Name returns the stable collector identifier.
func (c *Collector) Name() string { return "contacts" }

// DefaultInterval returns the configured interval, or 600s when unset.
func (c *Collector) DefaultInterval() time.Duration {
	if c.interval > 0 {
		return c.interval
	}
	return defaultInterval
}

// Collect fetches the contacts and emits needs_verification (0/1) per contact
// type. The contact email is deliberately never emitted (PII).
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	cs, err := c.api.Contacts(ctx)
	apistate.Observe(e, c.tracker, c.Name(), opGetContacts, contactsDisposition, err, c.now())
	if err != nil {
		return err
	}

	for _, ct := range []struct {
		typ     string
		contact tsclient.Contact
	}{
		{"account", cs.Account},
		{"support", cs.Support},
		{"security", cs.Security},
	} {
		e.Gauge(docNeedsVerification.Name, docNeedsVerification.Unit, docNeedsVerification.Description,
			boolValue(ct.contact.NeedsVerification), telemetry.Attrs{attrContactType: ct.typ})
	}
	return nil
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
