// Package webhooks is a snapshot collector for the tailnet's configured webhook
// ENDPOINTS — an inventory of where Tailscale posts event notifications. It is
// distinct from internal/webhook (the HMAC receiver that ingests those posts);
// hence the tailscale.webhook_endpoint(s).* namespace, kept separate from the
// receiver's tailscale.webhook.* metrics. Endpoint URLs, secrets and creator
// login names are never emitted.
//
// Beyond the endpoint inventory it reports EVENT-LEVEL subscription coverage
// (#417): which of the documented event categories any endpoint is subscribed
// to, zero-seeded across the whole vocabulary so an uncovered category reads as
// an explicit 0. A bare subscription COUNT cannot answer the question operators
// actually have — "is anything listening for nodeNeedsApproval?" — because two
// endpoints subscribed to the same three events look identical to two endpoints
// covering six.
package webhooks

import (
	"context"
	"fmt"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/entityage"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
)

// Compile-time assertion: *Collector is a SnapshotCollector.
var _ collector.SnapshotCollector = (*Collector)(nil)

const defaultInterval = 600 * time.Second

const (
	metricEndpointsCount     = "tailscale.webhook_endpoints.count"
	metricEndpointSubs       = "tailscale.webhook_endpoint.subscriptions"
	metricEventSubscriptions = "tailscale.webhook_endpoints.event_subscriptions"
	metricDesiredCovered     = "tailscale.webhook_endpoints.event_desired_covered"
	metricDesiredUnrecognize = "tailscale.webhook_endpoints.desired_unrecognized"
	metricEndpointAge        = "tailscale.webhook_endpoint.age"
	logEventMismatch         = "tailscale.webhook_endpoints.event_mismatch"
)

const (
	attrEndpointID       = "tailscale.webhook_endpoint.id"
	attrEndpointProvider = "tailscale.webhook_endpoint.provider"
	// attrEvent carries a value from the bounded event vocabulary (see
	// events.go). Registered in the PII registry as a non-identifier.
	attrEvent = "tailscale.webhook.event"
)

// opListWebhooks is the upstream operationId of the list call.
const opListWebhooks = "listWebhooks"

// webhooksDisposition is the DEFAULT disposition (#420): 403 stays scope_denied.
// Webhooks are available on every plan, so upstream has no reason to answer 403
// as a feature gate here — a 403 on this path means the credential is missing
// the webhooks read scope, and reading it as "disabled" would hide exactly that.
var webhooksDisposition = apistate.Disposition{}

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
	// desiredEvents is the operator's optional expectation set. Empty means "no
	// expectation" and suppresses every mismatch signal.
	desiredEvents []string
	// tracker records per-operation availability for the admin status page
	// (#420). A nil *apistate.Tracker is a no-op.
	tracker *apistate.Tracker
	// now is the clock, injectable from tests.
	now func() time.Time
}

// Option configures optional Collector behavior.
type Option func(*Collector)

// WithPerEntity controls whether the per-endpoint subscriptions gauge is emitted
// (default true); false (cardinality.per_entity.webhook) keeps only the
// aggregate webhook_endpoints.count.
//
// It does NOT gate the event-coverage signals: those are tailnet-level
// aggregates over a closed 19-value vocabulary, so they carry no per-endpoint
// cardinality for the toggle to control.
func WithPerEntity(enabled bool) Option {
	return func(c *Collector) { c.perEntity = enabled }
}

// WithDesiredEvents sets the optional expectation set (config
// collectors.webhooks.desired_events, #417): the event categories this tailnet
// is expected to have a subscriber for. Empty means no expectation.
func WithDesiredEvents(events []string) Option {
	return func(c *Collector) { c.desiredEvents = events }
}

// WithAPIState wires the shared per-operation availability tracker (#420).
// Availability METRICS are emitted regardless; the tracker is the in-process
// introspection copy the admin status page reads.
func WithAPIState(t *apistate.Tracker) Option {
	return func(c *Collector) { c.tracker = t }
}

// New returns a webhooks collector. A non-positive interval resolves to 600s.
func New(a api, interval time.Duration, opts ...Option) *Collector {
	c := &Collector{api: a, interval: interval, perEntity: true, now: time.Now}
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

// Collect lists webhook endpoints and emits the endpoint count, the per-endpoint
// subscriptions gauge (when per-entity is enabled), event-level coverage over
// the bounded vocabulary, the optional desired-event mismatch signals, and the
// fleet age distribution. The endpoint URL, secret and creator login are
// deliberately never emitted.
//
// Failures are CLASSIFIED rather than blanket-propagated (#420). A 404 means the
// tailnet exposes no webhook surface, so it emits count=0 and stays idle. Every
// other error is returned AND emits its distinct availability state, and
// deliberately emits no count: being denied tells us nothing about how many
// endpoints exist.
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	hooks, err := c.api.Webhooks(ctx)
	state := apistate.Observe(e, c.tracker, c.Name(), opListWebhooks, webhooksDisposition, err, c.now())
	if err != nil {
		if state == apistate.StateDisabled {
			e.Gauge(docEndpointsCount.Name, docEndpointsCount.Unit, docEndpointsCount.Description, 0, nil)
			return nil
		}
		return err
	}

	e.Gauge(docEndpointsCount.Name, docEndpointsCount.Unit, docEndpointsCount.Description,
		float64(len(hooks)), nil)

	c.emitEventCoverage(e, hooks)
	c.emitAge(e, hooks)

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

// emitEventCoverage writes the zero-seeded per-category coverage gauge and, when
// a desired set is configured, the mismatch signals (#417).
//
// The value is ENDPOINTS SUBSCRIBED, not subscriptions: an endpoint listing the
// same category twice still counts once, so the number answers "how many places
// would this event be delivered to". Zero-seeding the whole vocabulary is what
// makes an uncovered category an explicit 0 instead of a missing series, and the
// series count is fixed at len(EventVocabulary()) regardless of tailnet size.
func (c *Collector) emitEventCoverage(e telemetry.Emitter, hooks []tsclient.Webhook) {
	endpointsPerEvent := make(map[string]int, len(eventVocabulary)+1)
	for i := range hooks {
		// Per endpoint, count each folded category at most once — two unknown
		// values on one endpoint must not make "other" read as two endpoints.
		seen := make(map[string]struct{}, len(hooks[i].Subscriptions))
		for _, sub := range hooks[i].Subscriptions {
			ev := foldEvent(string(sub))
			if _, dup := seen[ev]; dup {
				continue
			}
			seen[ev] = struct{}{}
			endpointsPerEvent[ev]++
		}
	}
	for _, ev := range EventVocabulary() {
		e.Gauge(docEventSubscriptions.Name, docEventSubscriptions.Unit, docEventSubscriptions.Description,
			float64(endpointsPerEvent[ev]), telemetry.Attrs{attrEvent: ev})
	}

	if len(c.desiredEvents) == 0 {
		return
	}
	known, unknown := splitDesired(c.desiredEvents)

	for _, ev := range known {
		covered := 0.0
		if endpointsPerEvent[ev] > 0 {
			covered = 1
		}
		e.Gauge(docDesiredCovered.Name, docDesiredCovered.Unit, docDesiredCovered.Description,
			covered, telemetry.Attrs{attrEvent: ev})
		if covered == 0 {
			c.logMismatch(e, ev, fmt.Sprintf(
				"desired webhook event %q is uncovered: no configured endpoint subscribes to it", ev))
		}
	}

	// A typo in desired_events is a CONFIG fault, not a coverage gap. Folding it
	// into "other" would leave it reading as permanently uncovered with nothing
	// naming the cause, so it gets its own count and its own reason.
	e.Gauge(docDesiredUnrecognized.Name, docDesiredUnrecognized.Unit, docDesiredUnrecognized.Description,
		float64(len(unknown)), nil)
	for _, raw := range unknown {
		c.logMismatch(e, eventOther, fmt.Sprintf(
			"desired webhook event %q is unrecognized: not one of the documented subscription categories", raw))
	}
}

// logMismatch emits one bounded mismatch event. The body names the reason and,
// for the unrecognized case, the operator's own configured string — which is
// their config, never tailnet data. No endpoint URL, creator or secret can reach
// it: this function is only ever handed vocabulary values and config strings.
func (c *Collector) logMismatch(e telemetry.Emitter, event, body string) {
	e.LogEvent(telemetry.Event{
		Name:     docMismatchLog.Name,
		Severity: telemetry.SeverityWarn,
		Body:     body,
		Attrs:    telemetry.Attrs{attrEvent: event},
	})
}

// emitAge records the fleet age distribution of webhook endpoints (#426).
//
// Endpoints with no Created timestamp are skipped rather than recorded as age 0,
// and a fleet where none is known emits nothing at all — a histogram of "every
// endpoint is brand new" is a materially wrong answer, not a conservative one.
// The distribution carries NO attributes on purpose: a per-endpoint or
// per-provider histogram would multiply eight buckets by an unbounded dimension
// for a signal whose whole point is the fleet shape.
func (c *Collector) emitAge(e telemetry.Emitter, hooks []tsclient.Webhook) {
	now := c.now()
	var ages []float64
	for i := range hooks {
		if age, ok := entityage.Seconds(hooks[i].Created, now); ok {
			ages = append(ages, age)
		}
	}
	if len(ages) == 0 {
		return
	}
	bounds := entityage.BucketsSeconds()
	for _, age := range ages {
		e.Histogram(docEndpointAge.Name, docEndpointAge.Unit, docEndpointAge.Description, age, bounds, nil)
	}
}
