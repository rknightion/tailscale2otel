// Package webhooks is a snapshot collector for the tailnet's configured webhook
// ENDPOINTS — an inventory of where Tailscale posts event notifications. It is
// distinct from internal/webhook (the HMAC receiver that ingests those posts);
// hence the tailscale.webhook_endpoint(s).* namespace, kept separate from the
// receiver's tailscale.webhook.* metrics. Endpoint URLs, secrets and creator
// login names are never emitted.
package webhooks

import (
	"context"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/v2/internal/collector"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
)

// Compile-time assertion: *Collector is a SnapshotCollector.
var _ collector.SnapshotCollector = (*Collector)(nil)

const defaultInterval = 600 * time.Second

const (
	metricEndpointsCount = "tailscale.webhook_endpoints.count"
	metricEndpointSubs   = "tailscale.webhook_endpoint.subscriptions"
)

const (
	attrEndpointID       = "tailscale.webhook_endpoint.id"
	attrEndpointProvider = "tailscale.webhook_endpoint.provider"
)

// api is the narrow slice of the Tailscale client this collector needs. It is
// satisfied by *tsapi.Client.
type api interface {
	Webhooks(ctx context.Context) ([]tsclient.Webhook, error)
}

// Collector implements collector.SnapshotCollector for the webhook-endpoint inventory.
type Collector struct {
	api       api
	interval  time.Duration
	perEntity bool
}

// Option configures optional Collector behavior.
type Option func(*Collector)

// WithPerEntity controls whether the per-endpoint subscriptions gauge is emitted
// (default true); false (cardinality.per_entity.webhook) keeps only the
// aggregate webhook_endpoints.count.
func WithPerEntity(enabled bool) Option {
	return func(c *Collector) { c.perEntity = enabled }
}

// New returns a webhooks collector. A non-positive interval resolves to 600s.
func New(a api, interval time.Duration, opts ...Option) *Collector {
	c := &Collector{api: a, interval: interval, perEntity: true}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Name returns the stable collector identifier.
func (c *Collector) Name() string { return "webhooks" }

// DefaultInterval returns the configured interval, or 600s when unset.
func (c *Collector) DefaultInterval() time.Duration {
	if c.interval > 0 {
		return c.interval
	}
	return defaultInterval
}

// Collect lists webhook endpoints and emits the endpoint count plus, when
// per-entity is enabled, a per-endpoint subscriptions gauge. The endpoint URL,
// secret and creator login are deliberately never emitted.
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	hooks, err := c.api.Webhooks(ctx)
	if err != nil {
		return err
	}

	e.Gauge(docEndpointsCount.Name, docEndpointsCount.Unit, docEndpointsCount.Description,
		float64(len(hooks)), nil)

	if !c.perEntity {
		return nil
	}
	for i := range hooks {
		h := &hooks[i]
		e.Gauge(docEndpointSubs.Name, docEndpointSubs.Unit, docEndpointSubs.Description,
			float64(len(h.Subscriptions)), telemetry.Attrs{
				attrEndpointID:       h.EndpointID,
				attrEndpointProvider: string(h.ProviderType),
			})
	}
	return nil
}
