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
	metricRequests               = "tailscale2otel.objectstore.requests"
	metricRequestDuration        = "tailscale2otel.objectstore.request.duration"
	metricRetries                = "tailscale2otel.objectstore.retries"
	metricCursorAge              = "tailscale2otel.objectstore.cursor.age"
	metricDiscoveredNewestAge    = "tailscale2otel.objectstore.discovered.newest.age"
	metricPendingOldestAge       = "tailscale2otel.objectstore.pending.oldest.age"
)

// ageNothingDiscovered is what tailscale2otel.objectstore.discovered.newest.age
// reports when a cycle listed no object carrying a usable timestamp. An age can
// never be negative, so the value cannot collide with a real measurement, and it
// is deliberately NOT zero: zero is the freshest possible object, so an empty or
// misconfigured prefix would otherwise look like the healthiest possible feed.
// Emitting it (rather than omitting the gauge) keeps the series present, so an
// absent series still means only one thing — the collector is not running.
const ageNothingDiscovered = -1

// attrReason labels why an object or line was not ingested. It is the same
// bare "reason" key the stream receiver uses for the same purpose, so one query
// works across ingestion paths.
const (
	attrReason = "reason"
	attrLimit  = "limit"
	// attrOperation names which of the two provider calls a request metric
	// describes, and attrOutcome whether that call returned an error. Both are
	// CLOSED two-value sets defined immediately below, so the pair is four series
	// and can never carry an object key, identity, bucket, endpoint, URL, HTTP
	// status, or error text.
	attrOperation = "operation"
	attrOutcome   = "outcome"
)

// Operations. The whole provider seam is two calls, so this set is closed by the
// Backend interface itself.
const (
	operationList = "list"
	operationGet  = "get"
)

// Outcomes. Whether the provider call itself returned an error — nothing about
// what the call returned.
const (
	outcomeSuccess = "success"
	outcomeError   = "error"
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
	docRequests = metricdoc.Metric{
		Name:        metricRequests,
		Instrument:  metricdoc.Counter,
		Unit:        semconv.UnitDimensionless,
		Description: "Object-store provider calls, by operation and outcome. TRANSPORT health only, and exactly one data point per call: `error` means the LIST or GET call itself returned an error, never a decode, validation, framing, or per-object limit failure — those are counted by tailscale2otel.objectstore.skipped and the gap metrics. A failed GET is therefore counted once here and once on skipped, which measure different things; a body that fails mid-read counts as a SUCCESSFUL get, because the request succeeded and the read failure is already carried by skipped and the gaps. Both attributes are closed two-value sets, so this metric is at most four series and carries nothing derived from an object key, bucket, endpoint, or error text.",
		Attributes:  []string{attrOperation, attrOutcome},
		Group:       "Object-store ingestion",
	}
	docRequestDuration = metricdoc.Metric{
		Name:        metricRequestDuration,
		Instrument:  metricdoc.Histogram,
		Unit:        semconv.UnitSeconds,
		Description: "Wall-clock duration of object-store provider calls, by operation and outcome. It times the provider call itself: for `get` that is obtaining the object's reader, NOT streaming, decompressing, and decoding its body — that work is measured by the object, record, and byte counters. Attributed identically to tailscale2otel.objectstore.requests and bounded the same way, so the two divide by the same four series.",
		Attributes:  []string{attrOperation, attrOutcome},
		Group:       "Object-store ingestion",
	}
	docRetries = metricdoc.Metric{
		Name:        metricRetries,
		Instrument:  metricdoc.Counter,
		Unit:        semconv.UnitDimensionless,
		Description: "Object ingestion attempts that retried a previously failed object. The retry is OBJECT-level: an object that fails becomes a durable gap and is attempted again on a later cycle under a bounded backoff, and every one of those later attempts counts one — a first attempt on a newly listed object never does. Rising while tailscale2otel.objectstore.gaps does not fall means an object is failing repeatedly rather than recovering. Quarantined gaps are terminal until an operator intervenes, so they are never retried and never counted. Emitted every cycle, zero included, so a flat line reads as a healthy feed rather than as missing data.",
		Group:       "Object-store ingestion",
	}
	docCursorAge = metricdoc.Metric{
		Name:        metricCursorAge,
		Instrument:  metricdoc.Gauge,
		Unit:        semconv.UnitSeconds,
		Description: "Age in seconds of the ingestion cursor: the wall clock minus the timestamp this cycle leaves persisted, which is the lower bound of the next cycle's listing window. This is end-to-end ingestion lag, and in a healthy feed it settles near the exporter's write cadence plus one collection interval. It is never absent and never negative. A cold start with no persisted cursor reports the configured initial lookback rather than zero, because the cursor genuinely is that far behind; zero means the cursor sits at or ahead of the current instant, which is only reachable inside the fixed clock-skew allowance.",
		Group:       "Object-store ingestion",
	}
	docDiscoveredNewestAge = metricdoc.Metric{
		Name:        metricDiscoveredNewestAge,
		Instrument:  metricdoc.Gauge,
		Unit:        semconv.UnitSeconds,
		Description: "Age in seconds of the newest object the last cycle listed, measured from its key timestamp. This is how fresh the EXPORT's own writes are, independent of whether anything was ingested: objects skipped as already ingested still count, so a caught-up feed keeps reporting. -1 means the cycle listed no object with a usable timestamp — an empty or misconfigured prefix, or an exporter silent for longer than the listing window reaches — and is deliberately distinguishable from a fresh zero-second age, so alert on `> threshold or == -1`. Keys that do not parse (the unrecognized_key skip reason) and keys stamped beyond the clock-skew allowance (future_timestamp) are excluded, so a broken exporter clock cannot pin this at zero. Zero means the newest key is stamped at, or within the skew allowance ahead of, now.",
		Group:       "Object-store ingestion",
	}
	docPendingOldestAge = metricdoc.Metric{
		Name:        metricPendingOldestAge,
		Instrument:  metricdoc.Gauge,
		Unit:        semconv.UnitSeconds,
		Description: "Age in seconds of the oldest object listed but not yet ingested at the end of the last cycle, measured from its key timestamp. This is BACKLOG latency — how stale the next thing to be processed already is — over exactly the population tailscale2otel.objectstore.backlog counts, so zero here pairs with a zero backlog and means nothing is waiting. Like that backlog it is a lower bound while tailscale2otel.objectstore.scan.truncated is 1. It is NOT tailscale2otel.objectstore.gap.oldest.age: that one ages the oldest FAILED object awaiting retry or acknowledgement, and an object that fails leaves this population for that one, so the two report different objects on purpose. A healthy object deferred by the per-cycle object budget or by a cycle expansion limit stays counted here.",
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
		docRequests,
		docRequestDuration,
		docRetries,
		docCursorAge,
		docDiscoveredNewestAge,
		docPendingOldestAge,
	}
}
