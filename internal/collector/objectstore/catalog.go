package objectstore

import (
	"github.com/rknightion/tailscale2otel/v3/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
)

// Metric names emitted by this collector. Everything the flow records themselves
// produce is emitted by the shared flowlog.Processor; these describe the
// INGESTION, which is the part an operator cannot see any other way.
const (
	metricObjects                = "tailscale2otel.objectstore.objects"
	metricRecords                = "tailscale2otel.objectstore.records"
	metricBytes                  = "tailscale2otel.objectstore.bytes"
	metricDecompressedBytes      = "tailscale2otel.objectstore.decompressed.bytes"
	metricExpansionLimitFailures = "tailscale2otel.objectstore.expansion.limit_failures"
	metricSkipped                = "tailscale2otel.objectstore.skipped"
	metricBacklogObjects         = "tailscale2otel.objectstore.backlog"
	metricScanTruncated          = "tailscale2otel.objectstore.scan.truncated"
	metricGaps                   = "tailscale2otel.objectstore.gaps"
	metricGapOldestAge           = "tailscale2otel.objectstore.gap.oldest.age"
	metricGapHealthy             = "tailscale2otel.objectstore.gap.healthy"
)

// attrReason labels why an object or line was not ingested. It is the same
// bare "reason" key the stream receiver uses for the same purpose, so one query
// works across ingestion paths.
const (
	attrReason = "reason"
	attrLimit  = "limit"
)

// Reasons. These are a closed set defined here, not free text from a bucket.
const (
	reasonBudget          = "per_cycle_budget"
	reasonAlreadySeen     = "already_ingested"
	reasonUnparsedKey     = "unrecognized_key"
	reasonBeforeCursor    = "before_cursor"
	reasonFutureKey       = "future_timestamp"
	reasonDecodeError     = "decode_error"
	reasonSemanticInvalid = "semantic_invalid"
	reasonReadError       = "read_error"
	// reasonUndecodableObject counts whole objects that reached clean EOF but
	// produced no accepted record while at least one row failed — a framing
	// mismatch rather than individual corrupt records.
	reasonUndecodableObject = "undecodable_object"
)

var (
	docObjects = metricdoc.Metric{
		Name:        metricObjects,
		Instrument:  metricdoc.Counter,
		Unit:        semconv.UnitDimensionless,
		Description: "Objects successfully ingested from the flow-log export bucket.",
		Group:       "Object-store ingestion",
	}
	docRecords = metricdoc.Metric{
		Name:        metricRecords,
		Instrument:  metricdoc.Counter,
		Unit:        semconv.UnitDimensionless,
		Description: "Flow-log records decoded from ingested objects. Compare against the flow metrics to see what de-duplication removed.",
		Group:       "Object-store ingestion",
	}
	docBytes = metricdoc.Metric{
		Name:        metricBytes,
		Instrument:  metricdoc.Counter,
		Unit:        semconv.UnitBytes,
		Description: "Compressed object bytes actually read from the export bucket, including unsuccessful ingestion attempts.",
		Group:       "Object-store ingestion",
	}
	docDecompressedBytes = metricdoc.Metric{
		Name:        metricDecompressedBytes,
		Instrument:  metricdoc.Counter,
		Unit:        semconv.UnitBytes,
		Description: "Decompressed object bytes consumed by ingestion attempts, including attempts stopped by a configured expansion limit.",
		Group:       "Object-store ingestion",
	}
	docExpansionLimitFailures = metricdoc.Metric{
		Name:        metricExpansionLimitFailures,
		Instrument:  metricdoc.Counter,
		Unit:        semconv.UnitDimensionless,
		Description: "Object-store ingestion attempts stopped by a configured wire-byte, decompressed-byte, or record-count limit. The bounded limit attribute identifies the object or cycle limit that was breached.",
		Attributes:  []string{attrLimit},
		Group:       "Object-store ingestion",
	}
	docSkipped = metricdoc.Metric{
		Name:        metricSkipped,
		Instrument:  metricdoc.Counter,
		Unit:        semconv.UnitDimensionless,
		Description: "Objects or lines not ingested, by reason. A sustained non-zero `per_cycle_budget` means the per-cycle object cap is holding ingestion behind the bucket. `decode_error` and `semantic_invalid` count individual rows discarded from an object that still completed; `semantic_invalid` marks quarantined flow records, so inspect tailscale.network.data_quality for the bounded reason. `undecodable_object` counts whole objects that decoded no record at all while at least one row failed — the signature of an export whose framing is not newline-delimited records, so treat any non-zero value as a broken feed rather than as corrupt data; each one becomes a retried gap instead of being recorded as ingested. A non-zero `future_timestamp` means objects are named beyond the 5-minute clock-skew allowance and were skipped so they could not push the ingestion cursor past the wall clock; check the exporter's clock.",
		Attributes:  []string{attrReason},
		Group:       "Object-store ingestion",
	}
	docBacklog = metricdoc.Metric{
		Name:        metricBacklogObjects,
		Instrument:  metricdoc.Gauge,
		Unit:        semconv.UnitDimensionless,
		Description: "Objects listed but not yet ingested at the end of the last cycle. This is a lower bound when tailscale2otel.objectstore.scan.truncated is 1; zero means the examined listing ground is caught up, not necessarily the whole bucket.",
		Group:       "Object-store ingestion",
	}
	docScanTruncated = metricdoc.Metric{
		Name:        metricScanTruncated,
		Instrument:  metricdoc.Gauge,
		Unit:        semconv.UnitDimensionless,
		Description: "Whether unexamined object-listing ground remains after the last cycle. One means an S3 page was truncated or a listed object was not yet durably handled; zero together with a zero backlog means the current listing window is caught up.",
		Group:       "Object-store ingestion",
	}
	docGaps = metricdoc.Metric{
		Name:        metricGaps,
		Instrument:  metricdoc.Gauge,
		Unit:        semconv.UnitDimensionless,
		Description: "Failed object-store objects awaiting retry or operator acknowledgement. This count has no object-key attributes.",
		Group:       "Object-store ingestion",
	}
	docGapOldestAge = metricdoc.Metric{
		Name:        metricGapOldestAge,
		Instrument:  metricdoc.Gauge,
		Unit:        semconv.UnitSeconds,
		Description: "Age in seconds of the oldest unresolved object-store gap. Zero when no gaps remain.",
		Group:       "Object-store ingestion",
	}
	docGapHealthy = metricdoc.Metric{
		Name:        metricGapHealthy,
		Instrument:  metricdoc.Gauge,
		Unit:        semconv.UnitDimensionless,
		Description: "Whether object-store ingestion has no unresolved gaps. One is healthy; zero means at least one pending or quarantined object remains.",
		Group:       "Object-store ingestion",
	}
)

// Catalog returns this collector's metric descriptors.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{
		docObjects,
		docRecords,
		docBytes,
		docDecompressedBytes,
		docExpansionLimitFailures,
		docSkipped,
		docBacklog,
		docScanTruncated,
		docGaps,
		docGapOldestAge,
		docGapHealthy,
	}
}
