package objectstore

import (
	"github.com/rknightion/tailscale2otel/v3/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
)

// Metric names emitted by this collector. Everything the flow records themselves
// produce is emitted by the shared flowlog.Processor; these describe the
// INGESTION, which is the part an operator cannot see any other way.
const (
	metricObjects        = "tailscale2otel.objectstore.objects"
	metricRecords        = "tailscale2otel.objectstore.records"
	metricBytes          = "tailscale2otel.objectstore.bytes"
	metricSkipped        = "tailscale2otel.objectstore.skipped"
	metricBacklogObjects = "tailscale2otel.objectstore.backlog"
)

// attrReason labels why an object or line was not ingested. It is the same
// bare "reason" key the stream receiver uses for the same purpose, so one query
// works across ingestion paths.
const attrReason = "reason"

// Reasons. These are a closed set defined here, not free text from a bucket.
const (
	reasonBudget       = "per_cycle_budget"
	reasonAlreadySeen  = "already_ingested"
	reasonUnparsedKey  = "unrecognized_key"
	reasonBeforeCursor = "before_cursor"
	reasonDecodeError  = "decode_error"
	reasonReadError    = "read_error"
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
		Description: "Compressed bytes fetched from the flow-log export bucket, as reported by the object listing.",
		Group:       "Object-store ingestion",
	}
	docSkipped = metricdoc.Metric{
		Name:        metricSkipped,
		Instrument:  metricdoc.Counter,
		Unit:        semconv.UnitDimensionless,
		Description: "Objects or lines not ingested, by reason. A sustained non-zero `per_cycle_budget` means the per-cycle object cap is holding ingestion behind the bucket.",
		Attributes:  []string{attrReason},
		Group:       "Object-store ingestion",
	}
	docBacklog = metricdoc.Metric{
		Name:        metricBacklogObjects,
		Instrument:  metricdoc.Gauge,
		Unit:        semconv.UnitDimensionless,
		Description: "Objects listed but not yet ingested at the end of the last cycle. Zero means ingestion is caught up with the bucket.",
		Group:       "Object-store ingestion",
	}
)

// Catalog returns this collector's metric descriptors.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{docObjects, docRecords, docBytes, docSkipped, docBacklog}
}
