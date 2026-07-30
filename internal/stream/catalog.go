package stream

import "github.com/rknightion/tailscale2otel/v4/internal/metricdoc"

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this receiver's own
// gateway counters. The stream receiver routes accepted records to the shared
// flowlog.Processor and audit.Processor, which emit their own metrics and log
// records (cataloged in their packages); the two metrics here count what the
// receiver accepts and rejects. The emit sites (stream.go) reference these
// descriptors so a description/unit cannot drift from what is documented;
// catalog_test.go asserts what the receiver emits matches these declarations.
const groupReceivers = "Receivers"

var (
	docStreamRecords = metricdoc.Metric{
		Name:        MetricRecords,
		Unit:        "{record}",
		Instrument:  metricdoc.Counter,
		Description: "Records accepted by the HEC stream receiver, by stream type (`flow`/`audit`).",
		Attributes:  []string{attrType},
		Group:       groupReceivers,
	}
	docStreamRejected = metricdoc.Metric{
		Name:        MetricRejected,
		Unit:        "{rejection}",
		Instrument:  metricdoc.Counter,
		Description: "Whole requests rejected by the stream receiver, by reason (`auth` = bad token; `auth_required` = network-reachable with no token; `cross_site` = browser-originated request to the untokened receiver; `too_large` = body over the byte cap; `too_many_records` = over the per-request record cap; `too_many_connections` = over the per-request flow-connection cap; `overloaded` = admission budget full; `unparsable` = nothing JSON-like; `malformed` = structurally corrupt batch; `decode_error` = a known record failed typed decoding; `semantic_invalid` = a decoded flow failed bounded semantic validation; `wal_unavailable` = durable append failed).",
		Attributes:  []string{attrReason},
		Group:       groupReceivers,
	}
	docStreamDecodeErrors = metricdoc.Metric{
		Name:        MetricDecodeErrors,
		Unit:        "{record}",
		Instrument:  metricdoc.Counter,
		Description: "Records that classified as a known type but failed to decode, by stream type (`flow`/`audit`).",
		Attributes:  []string{attrType},
		Group:       groupReceivers,
	}
	docStreamInflight = metricdoc.Metric{
		Name:        MetricInflight,
		Unit:        "{request}",
		Instrument:  metricdoc.UpDownCounter,
		Description: "In-flight HTTP requests currently being processed by the HEC receiver.",
		Group:       groupReceivers,
	}
	docStreamRequestDuration = metricdoc.Metric{
		Name:        MetricRequestDuration,
		Unit:        "s",
		Instrument:  metricdoc.Histogram,
		Description: "Wall-clock duration of HEC receiver HTTP request handling, in seconds.",
		Group:       groupReceivers,
	}
	docStreamSkipped = metricdoc.Metric{
		Name:        MetricSkipped,
		Unit:        "{record}",
		Instrument:  metricdoc.Counter,
		Description: "Records extracted from an otherwise-valid request body but never routed to a processor, by reason (`unclassified` = matched neither the flow nor audit shape; `unwrap_drop` = a non-object value, e.g. a scalar/null HEC \"event\", was dropped while unwrapping the envelope before classification).",
		Attributes:  []string{attrReason},
		Group:       groupReceivers,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{
		docStreamRecords,
		docStreamRejected,
		docStreamDecodeErrors,
		docStreamInflight,
		docStreamRequestDuration,
		docStreamSkipped,
	}
}

// LogCatalog returns the log events this package emits (none; flow/audit log
// records are emitted by the shared processors).
func LogCatalog() []metricdoc.LogEvent {
	return nil
}
