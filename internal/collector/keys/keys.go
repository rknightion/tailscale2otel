// Package keys is a snapshot collector that reports Tailscale auth/API key
// inventory: per-key expiry time, aggregate counts grouped by derived type
// (plus revoked/invalid state), and a warning log event for keys nearing
// expiry.
package keys

import (
	"context"
	"fmt"
	"strconv"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// Compile-time assertion that *Collector is a SnapshotCollector.
var _ collector.SnapshotCollector = (*Collector)(nil)

// Metric and event names emitted by this collector.
const (
	MetricKeyExpiry = "tailscale.key.expiry"
	MetricKeysCount = "tailscale.keys.count"
	EventExpiring   = "tailscale.key.expiring"
)

// Attribute keys emitted by this collector.
const (
	attrID          = "tailscale.key.id"
	attrType        = "tailscale.key.type"
	attrDescription = "tailscale.key.description"
	attrRevoked     = "tailscale.key.revoked"
	attrInvalid     = "tailscale.key.invalid"
	attrExpiresIn   = "tailscale.key.expires_in_seconds"
)

// Derived key-type values. tsclient.Key has no KeyType field, so we classify
// keys by their device-create capabilities.
const (
	typeEphemeral = "ephemeral"
	typeReusable  = "reusable"
	typeOneOff    = "onetime"
)

const defaultInterval = 300 * time.Second

// lister is the narrow client surface this collector needs. It is satisfied by
// *tsapi.Client.
type lister interface {
	Keys(ctx context.Context) ([]tsclient.Key, error)
}

// Collector reports Tailscale key inventory on each tick.
type Collector struct {
	api        lister
	interval   time.Duration
	expiryWarn time.Duration
	now        func() time.Time
}

// New returns a keys Collector. A non-positive interval falls back to the
// default (300s) via DefaultInterval. now defaults to time.Now when nil
// (inject a fixed clock for deterministic tests). expiryWarn is the lead time
// within which an upcoming key expiry triggers a warning log event.
func New(api lister, interval, expiryWarn time.Duration, now func() time.Time) *Collector {
	if now == nil {
		now = time.Now
	}
	return &Collector{
		api:        api,
		interval:   interval,
		expiryWarn: expiryWarn,
		now:        now,
	}
}

// Name returns the stable collector identifier.
func (c *Collector) Name() string { return "keys" }

// DefaultInterval returns the configured interval, or 300s if non-positive.
func (c *Collector) DefaultInterval() time.Duration {
	if c.interval > 0 {
		return c.interval
	}
	return defaultInterval
}

// countKey groups keys for aggregate counting.
type countKey struct {
	typ     string
	revoked bool
	invalid bool
}

// Collect fetches the current keys and emits the inventory metrics and any
// expiry warnings.
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	ks, err := c.api.Keys(ctx)
	if err != nil {
		return fmt.Errorf("keys: list: %w", err)
	}

	now := c.now()
	counts := make(map[countKey]int)

	for i := range ks {
		k := ks[i]
		typ := keyType(k)
		revoked := !k.Revoked.IsZero()

		counts[countKey{typ: typ, revoked: revoked, invalid: k.Invalid}]++

		if !k.Expires.IsZero() {
			e.Gauge(docKeyExpiry.Name, docKeyExpiry.Unit, docKeyExpiry.Description,
				float64(k.Expires.Unix()), telemetry.Attrs{
					attrID:          k.ID,
					attrType:        typ,
					attrDescription: k.Description,
				})

			if c.expiryWarn > 0 {
				until := k.Expires.Sub(now)
				if until > 0 && until <= c.expiryWarn {
					e.LogEvent(telemetry.Event{
						Name:     docKeyExpiring.Name,
						Severity: telemetry.SeverityWarn,
						Body: fmt.Sprintf("Tailscale key %q (%s) expires in %s",
							keyLabel(k), typ, until.Round(time.Second)),
						Attrs: telemetry.Attrs{
							attrID:          k.ID,
							attrType:        typ,
							attrDescription: k.Description,
							attrExpiresIn:   strconv.Itoa(int(until.Seconds())),
						},
					})
				}
			}
		}
	}

	for k, n := range counts {
		e.Gauge(docKeysCount.Name, docKeysCount.Unit, docKeysCount.Description,
			float64(n), telemetry.Attrs{
				attrType:    k.typ,
				attrRevoked: k.revoked,
				attrInvalid: k.invalid,
			})
	}

	return nil
}

// keyType classifies a key by its device-create capabilities, since the
// tsclient.Key struct exposes no explicit type field.
func keyType(k tsclient.Key) string {
	create := k.Capabilities.Devices.Create
	switch {
	case create.Ephemeral:
		return typeEphemeral
	case create.Reusable:
		return typeReusable
	default:
		return typeOneOff
	}
}

// keyLabel returns a human-friendly identifier for log bodies, preferring the
// description and falling back to the ID.
func keyLabel(k tsclient.Key) string {
	if k.Description != "" {
		return k.Description
	}
	return k.ID
}
