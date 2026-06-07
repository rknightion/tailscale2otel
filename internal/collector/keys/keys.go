// Package keys is a snapshot collector that reports Tailscale auth/API key
// inventory: per-key expiry time, aggregate counts grouped by type and auth
// sub-kind (plus revoked/invalid state), and a warning log event for keys
// nearing expiry.
package keys

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

// Compile-time assertion that *Collector is a SnapshotCollector.
var _ collector.SnapshotCollector = (*Collector)(nil)

// Metric and event names emitted by this collector.
const (
	MetricKeyExpiry        = "tailscale.key.expiry"
	MetricKeysCount        = "tailscale.keys.count"
	MetricKeyScopes        = "tailscale.key.scopes"
	MetricKeyPreauthorized = "tailscale.key.preauthorized"
	EventExpiring          = "tailscale.key.expiring"
)

// Attribute keys emitted by this collector.
const (
	attrID          = "tailscale.key.id"
	attrType        = "tailscale.key.type"
	attrAuthKind    = "tailscale.key.auth_kind"
	attrDescription = "tailscale.key.description"
	attrRevoked     = "tailscale.key.revoked"
	attrInvalid     = "tailscale.key.invalid"
	attrExpiresIn   = "tailscale.key.expires_in_seconds"
)

// keyType values mirror the API's keyType enum (federated is out of scope; see
// roadmap A5). "unknown" is a defensive fallback for an unrecognized/empty type
// with no auth capabilities.
const (
	typeAuth    = "auth"
	typeClient  = "client"
	typeAPI     = "api"
	typeUnknown = "unknown"
)

// auth_kind sub-classifies auth keys (preserved from the pre-A1 tailscale.key.type
// values). Non-auth credentials report "none".
const (
	authKindEphemeral = "ephemeral"
	authKindReusable  = "reusable"
	authKindOneOff    = "onetime"
	authKindNone      = "none"
)

const defaultInterval = 300 * time.Second

// lister is the narrow client surface this collector needs. It is satisfied by
// *tsapi.Client.
type lister interface {
	KeysRich(ctx context.Context) ([]tsapi.Key, error)
}

// Collector reports Tailscale key inventory on each tick.
type Collector struct {
	api        lister
	interval   time.Duration
	expiryWarn time.Duration
	now        func() time.Time
	perEntity  bool
}

// Option configures optional Collector behavior.
type Option func(*Collector)

// WithPerEntity controls whether the per-key gauges (tailscale.key.expiry,
// tailscale.key.scopes, tailscale.key.preauthorized) are emitted. The default
// is true; false (cardinality.per_entity.key) emits only the aggregate
// tailscale.keys.count rollup. The expiry-warning log event is unaffected (it
// always fires within expiryWarn).
func WithPerEntity(enabled bool) Option {
	return func(c *Collector) { c.perEntity = enabled }
}

// New returns a keys Collector. A non-positive interval falls back to the
// default (300s) via DefaultInterval. now defaults to time.Now when nil
// (inject a fixed clock for deterministic tests). expiryWarn is the lead time
// within which an upcoming key expiry triggers a warning log event. Per-key
// gauges are emitted by default; pass WithPerEntity(false) to emit only counts.
func New(api lister, interval, expiryWarn time.Duration, now func() time.Time, opts ...Option) *Collector {
	if now == nil {
		now = time.Now
	}
	c := &Collector{
		api:        api,
		interval:   interval,
		expiryWarn: expiryWarn,
		now:        now,
		perEntity:  true,
	}
	for _, o := range opts {
		o(c)
	}
	return c
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
	typ      string
	authKind string
	revoked  bool
	invalid  bool
}

// Collect fetches the current keys and emits the inventory metrics and any
// expiry warnings.
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	ks, err := c.api.KeysRich(ctx)
	if err != nil {
		return fmt.Errorf("keys: list: %w", err)
	}

	now := c.now()
	counts := make(map[countKey]int)

	for i := range ks {
		k := ks[i]
		typ, authKind := classify(k)
		revoked := !k.Revoked.IsZero()

		counts[countKey{typ: typ, authKind: authKind, revoked: revoked, invalid: k.Invalid}]++

		if !k.Expires.IsZero() {
			// Per-key gauges (one series per key) are gated by
			// cardinality.per_entity.key; the expiry-warning log below always
			// fires regardless, so operators never lose the warning.
			if c.perEntity {
				e.Gauge(docKeyExpiry.Name, docKeyExpiry.Unit, docKeyExpiry.Description,
					float64(k.Expires.Unix()), telemetry.Attrs{
						attrID:          k.ID,
						attrType:        typ,
						attrAuthKind:    authKind,
						attrDescription: k.Description,
					})
			}

			if c.expiryWarn > 0 {
				until := k.Expires.Sub(now)
				if until > 0 && until <= c.expiryWarn {
					e.LogEvent(telemetry.Event{
						Name:     docKeyExpiring.Name,
						Severity: telemetry.SeverityWarn,
						Body: fmt.Sprintf("Tailscale key %q (%s/%s) expires in %s",
							keyLabel(k), typ, authKind, until.Round(time.Second)),
						Attrs: telemetry.Attrs{
							attrID:          k.ID,
							attrType:        typ,
							attrAuthKind:    authKind,
							attrDescription: k.Description,
							attrExpiresIn:   strconv.Itoa(int(until.Seconds())),
						},
					})
				}
			}
		}

		// Per-credential scope-sprawl signal (OAuth clients / API tokens only;
		// auth keys carry no scopes). Per-key -> gated by cardinality.per_entity.key.
		if c.perEntity && len(k.Scopes) > 0 {
			e.Gauge(docKeyScopes.Name, docKeyScopes.Unit, docKeyScopes.Description,
				float64(len(k.Scopes)), telemetry.Attrs{
					attrID:          k.ID,
					attrType:        typ,
					attrDescription: k.Description,
				})
		}

		// Per-key preauthorized flag (auth keys only). Per-key -> gated by
		// cardinality.per_entity.key.
		if c.perEntity && typ == typeAuth {
			pa := 0.0
			if k.Preauthorized {
				pa = 1.0
			}
			e.Gauge(docKeyPreauthorized.Name, docKeyPreauthorized.Unit, docKeyPreauthorized.Description,
				pa, telemetry.Attrs{
					attrID:          k.ID,
					attrType:        typ,
					attrDescription: k.Description,
				})
		}
	}

	for key, n := range counts {
		e.Gauge(docKeysCount.Name, docKeysCount.Unit, docKeysCount.Description,
			float64(n), telemetry.Attrs{
				attrType:     key.typ,
				attrAuthKind: key.authKind,
				attrRevoked:  key.revoked,
				attrInvalid:  key.invalid,
			})
	}

	return nil
}

// classify returns the credential's keyType and (for auth keys) its sub-kind.
// The API populates keyType for every credential; an empty keyType is treated
// defensively as an auth key (the only type the pre-A1 collector ever saw).
func classify(k tsapi.Key) (typ, authKind string) {
	switch k.Type {
	case typeClient:
		return typeClient, authKindNone
	case typeAPI:
		return typeAPI, authKindNone
	case typeAuth, "":
		return typeAuth, authKindOf(k)
	default:
		return typeUnknown, authKindNone
	}
}

// authKindOf sub-classifies an auth key by its device-create capabilities.
func authKindOf(k tsapi.Key) string {
	switch {
	case k.Ephemeral:
		return authKindEphemeral
	case k.Reusable:
		return authKindReusable
	default:
		return authKindOneOff
	}
}

// keyLabel returns a human-friendly identifier for log bodies, preferring the
// description and falling back to the ID.
func keyLabel(k tsapi.Key) string {
	if k.Description != "" {
		return k.Description
	}
	return k.ID
}
