package app

import (
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v4/internal/ingest"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
)

var ingestAgeBucketsSeconds = []float64{0, 1, 5, 10, 30, 60, 300, 900, 3600, 21600, 86400}

// ingestObserver returns the closure the ingestion paths (poll flow/audit, stream,
// webhook) call to record tailscale2otel.ingest.{records,bytes}. It returns nil
// when self-observability is disabled, mirroring apiObserver — so a nil hook is
// the off switch and the receiver/collector packages stay agnostic. records>0
// emits ingest.records{source,signal}; bytes>0 emits ingest.bytes{source}; a call
// may carry either or both.
func ingestObserver(e telemetry.Emitter, selfObs bool) func(source, signal string, records, bytes int) {
	if !selfObs {
		return nil
	}
	return func(source, signal string, records, bytes int) {
		if records > 0 {
			e.Counter(appcatalog.DocIngestRecords.Name, appcatalog.DocIngestRecords.Unit, appcatalog.DocIngestRecords.Description,
				float64(records), telemetry.Attrs{
					semconv.AttrIngestSource: source,
					semconv.AttrIngestSignal: signal,
				})
		}
		if bytes > 0 {
			e.Counter(appcatalog.DocIngestBytes.Name, appcatalog.DocIngestBytes.Unit, appcatalog.DocIngestBytes.Description,
				float64(bytes), telemetry.Attrs{semconv.AttrIngestSource: source})
		}
	}
}

// acceptedEventObserver records the age and greatest timestamp of records that
// survived source-level validation/de-duplication and were handed to a
// processor. Its state is per runtime, preventing cross-tailnet freshness from
// being merged.
func acceptedEventObserver(e telemetry.Emitter, selfObs bool) ingest.AcceptedObserver {
	if !selfObs {
		return nil
	}
	var mu sync.Mutex
	last := make(map[[2]string]time.Time)
	return func(event ingest.AcceptedEvent) {
		if event.EventTime.IsZero() {
			return
		}
		source := boundedIngestSource(event.Source)
		signal := boundedIngestSignal(event.Signal)
		acceptedAt := event.AcceptedAt
		if acceptedAt.IsZero() {
			acceptedAt = time.Now()
		}
		attrs := telemetry.Attrs{
			semconv.AttrIngestSource: source,
			semconv.AttrIngestSignal: signal,
		}

		age := acceptedAt.Sub(event.EventTime).Seconds()
		if age < 0 {
			age = 0
			e.Counter(appcatalog.DocIngestTimestampSkew.Name, appcatalog.DocIngestTimestampSkew.Unit,
				appcatalog.DocIngestTimestampSkew.Description, 1, attrs)
		}
		e.Histogram(appcatalog.DocIngestEventAge.Name, appcatalog.DocIngestEventAge.Unit,
			appcatalog.DocIngestEventAge.Description, age, ingestAgeBucketsSeconds, attrs)

		if !event.CaptureTime.IsZero() {
			delay := event.CaptureTime.Sub(event.EventTime).Seconds()
			if delay < 0 {
				delay = 0
				e.Counter(appcatalog.DocIngestTimestampSkew.Name, appcatalog.DocIngestTimestampSkew.Unit,
					appcatalog.DocIngestTimestampSkew.Description, 1, attrs)
			}
			e.Histogram(appcatalog.DocIngestCaptureDelay.Name, appcatalog.DocIngestCaptureDelay.Unit,
				appcatalog.DocIngestCaptureDelay.Description, delay, ingestAgeBucketsSeconds, attrs)
		}

		key := [2]string{source, signal}
		mu.Lock()
		if event.EventTime.After(last[key]) {
			last[key] = event.EventTime
		}
		latest := last[key]
		mu.Unlock()
		e.Gauge(appcatalog.DocIngestLastEventTimestamp.Name, appcatalog.DocIngestLastEventTimestamp.Unit,
			appcatalog.DocIngestLastEventTimestamp.Description,
			float64(latest.UnixNano())/float64(time.Second), attrs)
	}
}

func boundedIngestSource(source string) string {
	switch source {
	case semconv.IngestSourcePoll, semconv.IngestSourceStream,
		semconv.IngestSourceWebhook, semconv.IngestSourceObjectStore:
		return source
	default:
		return "other"
	}
}

func boundedIngestSignal(signal string) string {
	switch signal {
	case semconv.IngestSignalFlow, semconv.IngestSignalAudit, semconv.IngestSignalWebhook:
		return signal
	default:
		return "other"
	}
}
