package app

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v3/internal/ingest"
	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

func TestIngestObserverDisabled(t *testing.T) {
	if ingestObserver(telemetrytest.New().Emitter(), false) != nil {
		t.Fatal("ingestObserver should be nil when self-observability is disabled")
	}
}

func TestAcceptedEventObserverDisabled(t *testing.T) {
	if acceptedEventObserver(telemetrytest.New().Emitter(), false) != nil {
		t.Fatal("acceptedEventObserver should be nil when self-observability is disabled")
	}
}

func TestAcceptedEventObserverEmitsFreshnessWithoutRegressingLastTimestamp(t *testing.T) {
	rec := telemetrytest.New()
	acceptedAt := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	obs := acceptedEventObserver(rec.Emitter(), true)
	if obs == nil {
		t.Fatal("acceptedEventObserver nil when enabled")
	}

	latest := acceptedAt.Add(-10 * time.Minute)
	obs(ingest.AcceptedEvent{
		Source:      semconv.IngestSourceStream,
		Signal:      semconv.IngestSignalFlow,
		EventTime:   latest,
		CaptureTime: latest.Add(2 * time.Minute),
		AcceptedAt:  acceptedAt,
	})
	obs(ingest.AcceptedEvent{
		Source:     semconv.IngestSourceStream,
		Signal:     semconv.IngestSignalFlow,
		EventTime:  latest.Add(-time.Hour),
		AcceptedAt: acceptedAt,
	})

	attrs := map[string]string{
		semconv.AttrIngestSource: semconv.IngestSourceStream,
		semconv.AttrIngestSignal: semconv.IngestSignalFlow,
	}
	ages := rec.MetricPoints(appcatalog.MetricIngestEventAge)
	if len(ages) != 1 || ages[0].Count != 2 || ages[0].Value != 4800 {
		t.Fatalf("event ages = %+v, want count=2 sum=4800s", ages)
	}
	for _, point := range ages {
		for key, want := range attrs {
			if point.Attrs[key] != want {
				t.Fatalf("event age attrs = %v, want %v", point.Attrs, attrs)
			}
		}
	}
	capture := rec.MetricPoints(appcatalog.MetricIngestCaptureDelay)
	if len(capture) != 1 || capture[0].Count != 1 || capture[0].Value != 120 {
		t.Fatalf("capture delay = %+v, want one 120s point", capture)
	}
	last := rec.MetricPoints(appcatalog.MetricIngestLastEventTimestamp)
	if len(last) != 1 {
		t.Fatalf("last-event points = %d, want 1", len(last))
	}
	wantLatest := float64(latest.UnixNano()) / float64(time.Second)
	if last[0].Value != wantLatest {
		t.Fatalf("last-event value = %v; want monotonic %v", last[0].Value, wantLatest)
	}
}

func TestAcceptedEventObserverClampsFutureAgeAndCountsSkew(t *testing.T) {
	rec := telemetrytest.New()
	acceptedAt := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	obs := acceptedEventObserver(rec.Emitter(), true)
	obs(ingest.AcceptedEvent{
		Source:     semconv.IngestSourceWebhook,
		Signal:     semconv.IngestSignalWebhook,
		EventTime:  acceptedAt.Add(time.Minute),
		AcceptedAt: acceptedAt,
	})

	ages := rec.MetricPoints(appcatalog.MetricIngestEventAge)
	if len(ages) != 1 || ages[0].Value != 0 {
		t.Fatalf("future event age = %+v, want one clamped zero point", ages)
	}
	skew := rec.MetricPoints(appcatalog.MetricIngestTimestampSkew)
	if len(skew) != 1 || skew[0].Value != 1 {
		t.Fatalf("timestamp skew = %+v, want one counter point", skew)
	}
}

func TestIngestObserverEmits(t *testing.T) {
	rec := telemetrytest.New()

	obs := ingestObserver(rec.Emitter(), true)
	if obs == nil {
		t.Fatal("ingestObserver nil when enabled")
	}
	obs(semconv.IngestSourceStream, semconv.IngestSignalFlow, 5, 0)       // records only
	obs(semconv.IngestSourceWebhook, semconv.IngestSignalWebhook, 2, 128) // records + bytes

	recs := rec.MetricPoints(appcatalog.MetricIngestRecords)
	if len(recs) != 2 {
		t.Fatalf("ingest.records points = %d, want 2", len(recs))
	}
	bytes := rec.MetricPoints(appcatalog.MetricIngestBytes)
	if len(bytes) != 1 {
		t.Fatalf("ingest.bytes points = %d, want 1 (records-only call must not emit bytes)", len(bytes))
	}
	if bytes[0].Value != 128 || bytes[0].Attrs[semconv.AttrIngestSource] != semconv.IngestSourceWebhook {
		t.Errorf("ingest.bytes = %v attrs=%v", bytes[0].Value, bytes[0].Attrs)
	}
}
