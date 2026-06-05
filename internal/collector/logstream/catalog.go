package logstream

import (
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/semconv"
)

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this package's metric
// and log-event documentation; the emit sites reference these descriptors so a
// description/unit cannot drift, and catalog_test.go asserts the emission matches.
const groupLogStreaming = "Log streaming"

var typeAttr = []string{attrType}

var (
	docConfigured = metricdoc.Metric{
		Name:        metricConfigured,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "`1` if a log stream is configured for this log type, else `0`.",
		Attributes:  typeAttr,
		Group:       groupLogStreaming,
	}
	docBytesSent = metricdoc.Metric{
		Name:        metricBytesSent,
		Unit:        semconv.UnitBytes,
		Instrument:  metricdoc.Counter,
		Description: "Bytes delivered to the log-stream sink (emitted as the delta of Tailscale's cumulative counter).",
		Attributes:  typeAttr,
		Group:       groupLogStreaming,
	}
	docEntriesSent = metricdoc.Metric{
		Name:        metricEntriesSent,
		Unit:        semconv.UnitEvents,
		Instrument:  metricdoc.Counter,
		Description: "Log entries delivered to the sink.",
		Attributes:  typeAttr,
		Group:       groupLogStreaming,
	}
	docRequests = metricdoc.Metric{
		Name:        metricRequests,
		Unit:        semconv.UnitRequests,
		Instrument:  metricdoc.Counter,
		Description: "Total delivery requests to the sink.",
		Attributes:  typeAttr,
		Group:       groupLogStreaming,
	}
	docRequestsFailed = metricdoc.Metric{
		Name:        metricRequestsFailed,
		Unit:        semconv.UnitRequests,
		Instrument:  metricdoc.Counter,
		Description: "Failed delivery requests to the sink (alert on a sustained rate).",
		Attributes:  typeAttr,
		Group:       groupLogStreaming,
	}
	docSpoofedEntries = metricdoc.Metric{
		Name:        metricSpoofedEntries,
		Unit:        semconv.UnitEvents,
		Instrument:  metricdoc.Counter,
		Description: "Log entries rejected as spoofed.",
		Attributes:  typeAttr,
		Group:       groupLogStreaming,
	}
	docMaxBodyRequests = metricdoc.Metric{
		Name:        metricMaxBodyRequests,
		Unit:        semconv.UnitRequests,
		Instrument:  metricdoc.Counter,
		Description: "Delivery requests that hit the maximum body size (a SIEM backpressure signal).",
		Attributes:  typeAttr,
		Group:       groupLogStreaming,
	}
	docLastActivity = metricdoc.Metric{
		Name:        metricLastActivity,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Gauge,
		Description: "Unix timestamp of the most recent delivery activity (alert on staleness).",
		Attributes:  typeAttr,
		Group:       groupLogStreaming,
	}
	docError = metricdoc.Metric{
		Name:        metricError,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "`1` if the last delivery reported an error, else `0`. The error text is on the `tailscale.logstream.error` LOG event, never a label.",
		Attributes:  typeAttr,
		Group:       groupLogStreaming,
	}

	docErrorLog = metricdoc.LogEvent{
		Name:        metricError,
		Severity:    "ERROR",
		Description: "Emitted when a log stream's last delivery reported an error; the error text is the log body.",
		Attributes:  typeAttr,
		Group:       groupLogStreaming,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{
		docConfigured, docBytesSent, docEntriesSent, docRequests, docRequestsFailed,
		docSpoofedEntries, docMaxBodyRequests, docLastActivity, docError,
	}
}

// LogCatalog returns the log events this package emits.
func LogCatalog() []metricdoc.LogEvent { return []metricdoc.LogEvent{docErrorLog} }
