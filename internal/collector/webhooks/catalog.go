package webhooks

import (
	"github.com/rknightion/tailscale2otel/v3/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
)

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this package's metric
// documentation; the emit site (webhooks.go) references these descriptors so a
// description/unit cannot drift, and catalog_test.go asserts the emission matches.
const groupWebhooks = "Webhooks"

var eventAttr = []string{attrEvent}

var (
	docEndpointsCount = metricdoc.Metric{
		Name:        metricEndpointsCount,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of configured webhook endpoints (a **count**, despite `_ratio`). Emitted as `0` when the tailnet exposes no webhook surface at all (HTTP 404); a rejected or under-scoped credential emits nothing here — see `tailscale2otel.api.availability`.",
		Group:       groupWebhooks,
	}
	docEndpointSubs = metricdoc.Metric{
		Name:        metricEndpointSubs,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of event categories a webhook endpoint is subscribed to (a **count**); one series per endpoint. **Gated** by `cardinality.per_entity.webhook`. The endpoint URL/secret/creator are never emitted.",
		Attributes:  []string{attrEndpointID, attrEndpointProvider},
		Group:       groupWebhooks,
	}
	docEventSubscriptions = metricdoc.Metric{
		Name:       metricEventSubscriptions,
		Unit:       semconv.UnitDimensionless,
		Instrument: metricdoc.Gauge,
		Description: "Number of webhook endpoints subscribed to each documented event category (a **count**, despite `_ratio`). " +
			"The full 18-value subscription vocabulary is emitted every tick, **zero-seeded**, plus `other` for any " +
			"future value upstream adds — so a category nobody listens for reads as an explicit `0` rather than a " +
			"missing series, and the label stays bounded at 19 values. An endpoint listing the same category twice " +
			"counts once. Alert on `== 0` for the categories your incident response depends on.",
		Attributes: eventAttr,
		Group:      groupWebhooks,
	}
	docDesiredCovered = metricdoc.Metric{
		Name:       metricDesiredCovered,
		Unit:       semconv.UnitDimensionless,
		Instrument: metricdoc.Gauge,
		Description: "`1` if an event category listed in `collectors.webhooks.desired_events` has at least one " +
			"subscribed endpoint, else `0` (a **flag**, despite `_ratio`). Emitted only for configured desired " +
			"categories, and not at all when the list is empty (no expectation). Unrecognized entries are excluded " +
			"here and counted by `tailscale.webhook_endpoints.desired_unrecognized` instead.",
		Attributes: eventAttr,
		Group:      groupWebhooks,
	}
	docDesiredUnrecognized = metricdoc.Metric{
		Name:       metricDesiredUnrecognize,
		Unit:       semconv.UnitDimensionless,
		Instrument: metricdoc.Gauge,
		Description: "Number of `collectors.webhooks.desired_events` entries that are not documented subscription " +
			"categories (a **count**, despite `_ratio`) — almost always a typo. Kept separate from coverage because " +
			"a misspelled category would otherwise read as permanently uncovered with nothing naming the cause. " +
			"Emitted only when `desired_events` is non-empty; the offending strings are on the " +
			"`tailscale.webhook_endpoints.event_mismatch` log event, never a label.",
		Group: groupWebhooks,
	}
	docEndpointAge = metricdoc.Metric{
		Name:       metricEndpointAge,
		Unit:       semconv.UnitSeconds,
		Instrument: metricdoc.Histogram,
		Description: "Age distribution of configured webhook endpoints, in seconds since creation. Fleet-level: no " +
			"per-endpoint attributes. Endpoints whose `created` timestamp is absent are omitted rather than " +
			"recorded as age `0`, and nothing is emitted when no endpoint reports one.",
		Group: groupWebhooks,
	}

	docMismatchLog = metricdoc.LogEvent{
		Name:     logEventMismatch,
		Severity: "WARN",
		Description: "Emitted per `collectors.webhooks.desired_events` entry that is either uncovered (no endpoint " +
			"subscribes to it) or unrecognized (not a documented category). The body names the reason; the " +
			"`tailscale.webhook.event` attribute carries the bounded category (`other` for an unrecognized entry). " +
			"No endpoint URL, creator identity or secret can appear here.",
		Attributes: eventAttr,
		Group:      groupWebhooks,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{
		docEndpointsCount, docEndpointSubs,
		docEventSubscriptions, docDesiredCovered, docDesiredUnrecognized,
		docEndpointAge,
	}
}

// LogCatalog returns the log events this package emits.
func LogCatalog() []metricdoc.LogEvent { return []metricdoc.LogEvent{docMismatchLog} }
