// Package contacts is a snapshot collector for the tailnet's account/support/
// security contacts. It emits one gauge per contact type indicating whether the
// contact email still needs verification (an unverified security contact means
// security mail may not be delivered). The email address itself is never emitted.
package contacts

import (
	"context"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// Compile-time assertion: *Collector is a SnapshotCollector.
var _ collector.SnapshotCollector = (*Collector)(nil)

const defaultInterval = 600 * time.Second

// metricNeedsVerification is the per-contact-type verification gauge.
const metricNeedsVerification = "tailscale.contact.needs_verification"

// attrContactType labels a point with the contact type (account/support/security).
const attrContactType = "tailscale.contact.type"

// api is the narrow slice of the Tailscale client this collector needs. It is
// satisfied by *tsapi.Client.
type api interface {
	Contacts(ctx context.Context) (*tsclient.Contacts, error)
}

// Collector implements collector.SnapshotCollector for tailnet contacts.
type Collector struct {
	api      api
	interval time.Duration
}

// New returns a contacts collector. A non-positive interval resolves to the
// default (600s) via DefaultInterval.
func New(a api, interval time.Duration) *Collector {
	return &Collector{api: a, interval: interval}
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
