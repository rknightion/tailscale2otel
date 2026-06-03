package stream

import "github.com/rknightion/tailscale2otel/internal/metricdoc"

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
		Description: "Records rejected by the stream receiver, by reason (`auth`/`unparsable`/`too_large`).",
		Attributes:  []string{attrReason},
		Group:       groupReceivers,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{docStreamRecords, docStreamRejected}
}

// LogCatalog returns the log events this package emits (none; flow/audit log
// records are emitted by the shared processors).
func LogCatalog() []metricdoc.LogEvent {
	return nil
}
