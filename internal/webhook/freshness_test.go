package webhook

import (
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/audit"
	"github.com/rknightion/tailscale2otel/v3/internal/dedup"
	"github.com/rknightion/tailscale2otel/v3/internal/ingest"
	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

func TestHandler_OnAcceptedReportsDeliveryDedupSurvivingEvents(t *testing.T) {
	now := time.Date(2026, 6, 2, 10, 6, 30, 0, time.UTC)
	body := twoEventBody
	var mu sync.Mutex
	var got []ingest.AcceptedEvent

	rec := telemetrytest.New()
	s := New(Options{
		Listen: "127.0.0.1:0",
		Path:   "/webhook",
		Secret: testSecret,
		OnAccepted: func(event ingest.AcceptedEvent) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, event)
		},
	}, rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)), WithClock(func() time.Time { return now }))

	resp := doPost(t, s.Handler(), "/webhook", body, signBody(testSecret, now, body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("accepted events = %d, want 2: %+v", len(got), got)
	}
	wantTimes := []time.Time{
		time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 2, 10, 5, 0, 0, time.UTC),
	}
	for i, event := range got {
		if event.Source != semconv.IngestSourceWebhook || event.Signal != semconv.IngestSignalWebhook {
			t.Errorf("event %d source/signal = %q/%q, want %q/%q", i, event.Source, event.Signal, semconv.IngestSourceWebhook, semconv.IngestSignalWebhook)
		}
		if !event.EventTime.Equal(wantTimes[i]) {
			t.Errorf("event %d event time = %s, want %s", i, event.EventTime, wantTimes[i])
		}
		if !event.CaptureTime.IsZero() {
			t.Errorf("event %d capture time = %s, want zero", i, event.CaptureTime)
		}
		if !event.AcceptedAt.Equal(now) {
			t.Errorf("event %d accepted at = %s, want %s", i, event.AcceptedAt, now)
		}
	}
}

func TestHandler_OnAcceptedExcludesRejectedAndDeliveryDuplicateEvents(t *testing.T) {
	now := time.Date(2026, 6, 2, 10, 6, 30, 0, time.UTC)
	body := `[{"timestamp":"2026-06-02T10:00:00Z","version":1,"type":"nodeCreated","tailnet":"example.com","message":"Node created","data":{"nodeID":"n1"}}]`
	var mu sync.Mutex
	var got []ingest.AcceptedEvent
	rec := telemetrytest.New()
	s := New(Options{
		Listen: "127.0.0.1:0",
		Path:   "/webhook",
		Secret: testSecret,
		OnAccepted: func(event ingest.AcceptedEvent) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, event)
		},
	}, rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)), WithClock(func() time.Time { return now }))

	if resp := doPost(t, s.Handler(), "/webhook", body, "bad"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad signature status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	if resp := doPost(t, s.Handler(), "/webhook", `[{"timestamp":false}]`, signBody(testSecret, now, `[{"timestamp":false}]`)); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid schema status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	for range 2 {
		if resp := doPost(t, s.Handler(), "/webhook", body, signBody(testSecret, now, body)); resp.StatusCode != http.StatusOK {
			t.Fatalf("valid post status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("accepted events = %d, want 1 after rejected and duplicate deliveries: %+v", len(got), got)
	}
}

func TestHandler_OnAcceptedSurvivesCrossSourceDedup(t *testing.T) {
	now := time.Date(2026, 6, 2, 10, 6, 30, 0, time.UTC)
	body := `[{"timestamp":"2026-06-02T10:00:00Z","version":1,"type":"nodeCreated","tailnet":"example.com","message":"Node created","data":{"nodeID":"n1"}}]`
	set := dedup.New(0)
	audit.NewProcessor(audit.WithCrossDedup(set)).Process(audit.Event{
		EventTime: time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC),
		Action:    "CREATE",
		Target:    audit.Target{ID: "n1", Type: "NODE"},
	}, telemetrytest.New().Emitter())

	var got []ingest.AcceptedEvent
	var crossDedupHitsAtAccepted uint64
	rec := telemetrytest.New()
	s := New(Options{
		Listen: "127.0.0.1:0",
		Path:   "/webhook",
		Secret: testSecret,
		OnAccepted: func(event ingest.AcceptedEvent) {
			got = append(got, event)
			crossDedupHitsAtAccepted = set.Hits()
		},
	}, rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)), WithClock(func() time.Time { return now }), WithDedup(set))

	if resp := doPost(t, s.Handler(), "/webhook", body, signBody(testSecret, now, body)); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if len(rec.LogRecords()) != 0 {
		t.Fatalf("log records = %d, want 0 because cross-source dedup suppresses telemetry", len(rec.LogRecords()))
	}
	if len(got) != 1 {
		t.Fatalf("accepted events = %d, want 1 despite cross-source dedup", len(got))
	}
	if crossDedupHitsAtAccepted != 1 {
		t.Fatalf("cross-source dedup hits at OnAccepted = %d, want 1 (dedup attempt must precede freshness observation)", crossDedupHitsAtAccepted)
	}
}
