package webhook

import (
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/semconv"
)

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this receiver's metric
// and log-event documentation. The emit sites (webhook.go) reference these
// descriptors so a description/unit cannot drift from what is documented; the
// doc generator (tools/metricscatalog, via internal/catalog) renders them into
// docs/metrics.md, and catalog_test.go asserts what the receiver emits matches
// these declarations.
//
// The per-event log record's EventName is computed at runtime as
// eventNamePrefix + ev.Type (e.g. "tailscale.webhook.nodeCreated"), so it cannot
// be statically enumerated; LogCatalog() declares the "tailscale.webhook.<type>"
// pattern for documentation, and catalog_test.go verifies emitted log names by
// prefix.
const groupReceivers = "Receivers"

// eventNameDoc is the documentation placeholder for the computed per-event log
// record name (eventNamePrefix + the Tailscale event type).
const eventNameDoc = eventNamePrefix + "<type>"

var (
	docWebhookEvents = metricdoc.Metric{
		Name:        MetricEvents,
		Unit:        semconv.UnitEvents,
		Instrument:  metricdoc.Counter,
		Description: "Webhook events accepted, by Tailscale event type.",
		Attributes:  []string{attrType},
		Group:       groupReceivers,
	}
	docWebhookRejected = metricdoc.Metric{
		Name:        MetricRejected,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Counter,
		Description: "Webhook deliveries rejected (e.g. bad HMAC), by reason.",
		Attributes:  []string{attrReason},
		Group:       groupReceivers,
	}
	docWebhookInflight = metricdoc.Metric{
		Name:        "tailscale.webhook.inflight",
		Unit:        semconv.UnitRequests,
		Instrument:  metricdoc.UpDownCounter,
		Description: "In-flight HTTP requests currently being processed by the webhook receiver.",
		Attributes:  nil,
		Group:       groupReceivers,
	}
	docWebhookRequestDuration = metricdoc.Metric{
		Name:        "tailscale.webhook.request.duration",
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Histogram,
		Description: "Wall-clock duration of webhook receiver HTTP request handling, in seconds.",
		Attributes:  nil,
		Group:       groupReceivers,
	}

	docWebhookLog = metricdoc.LogEvent{
		Name:        eventNameDoc,
		Severity:    "INFO / WARN by type",
		Description: "Per webhook event; `<type>` is the Tailscale event type. Emitted at **WARN** for attention-worthy types (node key expiry, needs-approval/authorization/signature, deletions), otherwise INFO. The client-misconfig health events `exitNodeIPForwardingNotEnabled`/`subnetIPForwardingNotEnabled` are INFO and surfaced via the `NodeIPForwardingMisconfigured` alert.",
		Attributes:  []string{attrType, semconv.AttrTailnet},
		Group:       groupReceivers,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{docWebhookEvents, docWebhookRejected, docWebhookInflight, docWebhookRequestDuration}
}

// LogCatalog returns the log events this package emits, for the doc generator.
func LogCatalog() []metricdoc.LogEvent {
	return []metricdoc.LogEvent{docWebhookLog}
}
