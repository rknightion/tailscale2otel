package app

import (
	"testing"

	"github.com/rknightion/tailscale2otel/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/internal/config"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

func TestIngestObserverDisabled(t *testing.T) {
	a := &App{cfg: &config.Config{}} // SelfObservability.Enabled == false
	if a.ingestObserver() != nil {
		t.Fatal("ingestObserver should be nil when self-observability is disabled")
	}
}

func TestIngestObserverEmits(t *testing.T) {
	rec := telemetrytest.New()
	cfg := &config.Config{}
	cfg.SelfObservability.Enabled = true
	a := &App{cfg: cfg, emitter: rec.Emitter()}

	obs := a.ingestObserver()
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
