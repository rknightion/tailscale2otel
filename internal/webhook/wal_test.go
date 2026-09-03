package webhook

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/dedup"
	"github.com/rknightion/tailscale2otel/v5/internal/ingest"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

type durableAppendCall struct {
	body       []byte
	acceptedAt time.Time
}

func serveDurablePost(
	t *testing.T,
	ctx context.Context,
	handler http.Handler,
	body,
	signature string,
	headers map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body)).WithContext(ctx)
	if signature != "" {
		req.Header.Set(signatureHeader, signature)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)
	return rw
}

func assertNoAcceptedWebhookEffects(
	t *testing.T,
	s *Server,
	rec *telemetrytest.Recorder,
	cross *dedup.Set,
	ingestCalls,
	acceptedCalls int,
) {
	t.Helper()
	if ingestCalls != 0 {
		t.Errorf("OnIngest calls = %d, want 0", ingestCalls)
	}
	if acceptedCalls != 0 {
		t.Errorf("OnAccepted calls = %d, want 0", acceptedCalls)
	}
	if got := s.delivery.Len(); got != 0 {
		t.Errorf("delivery dedup entries = %d, want 0", got)
	}
	if cross != nil && cross.Len() != 0 {
		t.Errorf("cross-source dedup entries = %d, want 0", cross.Len())
	}
	s.typesMu.Lock()
	seenTypes := len(s.seenTypes)
	s.typesMu.Unlock()
	if seenTypes != 0 {
		t.Errorf("bounded event types = %d, want 0", seenTypes)
	}
	if got := len(rec.LogRecords()); got != 0 {
		t.Errorf("log records = %d, want 0", got)
	}
	for _, metric := range []string{MetricEvents, MetricDuplicates, MetricSchemaDrift} {
		if points := rec.MetricPoints(metric); len(points) != 0 {
			t.Errorf("%s points = %+v, want none", metric, points)
		}
	}
}

func TestHandler_DurableModeValidatesCompleteRequestBeforeAppend(t *testing.T) {
	now := time.Date(2026, 6, 2, 10, 6, 0, 0, time.UTC)
	valid := `[{"timestamp":"2026-06-02T10:00:00Z","version":1,"type":"nodeCreated","tailnet":"example.com","message":"ok","data":{"nodeID":"n1"}}]`
	invalidEvent := `[{"timestamp":"2026-06-02T10:00:00Z","version":1,"type":"nodeCreated","tailnet":"example.com","message":"ok"},{"timestamp":"2026-06-02T10:00:00Z","version":"1","type":"nodeDeleted","tailnet":"example.com"}]`
	tests := []struct {
		name      string
		body      string
		signature string
		tolerance time.Duration
	}{
		{name: "missing signature", body: valid},
		{name: "bad signature", body: valid, signature: "t=1780394760,v1=bad"},
		{name: "stale timestamp", body: valid, signature: signBody(testSecret, now.Add(-2*time.Minute), valid), tolerance: time.Minute},
		{name: "invalid outer body", body: `{"not":"an array"}`, signature: signBody(testSecret, now, `{"not":"an array"}`)},
		{name: "later invalid event", body: invalidEvent, signature: signBody(testSecret, now, invalidEvent)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := telemetrytest.New()
			appendCalls := 0
			ingestCalls := 0
			acceptedCalls := 0
			cross := dedup.New(8)
			s := New(Options{
				Listen:    "127.0.0.1:0",
				Path:      "/webhook",
				Secret:    testSecret,
				Tolerance: tt.tolerance,
				OnIngest: func(string, string, int, int) {
					ingestCalls++
				},
				OnAccepted: func(ingest.AcceptedEvent) {
					acceptedCalls++
				},
			}, rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)),
				WithClock(func() time.Time { return now }),
				WithDedup(cross),
				WithDurableAppend(func(context.Context, []byte, time.Time) error {
					appendCalls++
					return nil
				}),
			)

			rw := serveDurablePost(t, context.Background(), s.Handler(), tt.body, tt.signature, nil)
			if rw.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rw.Code, http.StatusUnauthorized)
			}
			if appendCalls != 0 {
				t.Fatalf("durable append calls = %d, want 0", appendCalls)
			}
			assertNoAcceptedWebhookEffects(t, s, rec, cross, ingestCalls, acceptedCalls)
		})
	}
}

func TestHandler_DurableAppendBlocksAcknowledgementAndReceivesExactBodyOnce(t *testing.T) {
	now := time.Date(2026, 6, 2, 10, 6, 30, 123, time.UTC)
	body := " \n" + twoEventBody + "\t"
	entered := make(chan durableAppendCall, 1)
	release := make(chan struct{})
	rec := telemetrytest.New()
	ingestCalls := 0
	acceptedCalls := 0
	s := New(Options{
		Listen: "127.0.0.1:0",
		Path:   "/webhook",
		Secret: testSecret,
		OnIngest: func(string, string, int, int) {
			ingestCalls++
		},
		OnAccepted: func(ingest.AcceptedEvent) {
			acceptedCalls++
		},
	}, rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithClock(func() time.Time { return now }),
		WithDurableAppend(func(_ context.Context, got []byte, acceptedAt time.Time) error {
			entered <- durableAppendCall{body: bytes.Clone(got), acceptedAt: acceptedAt}
			<-release
			return nil
		}),
	)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- serveDurablePost(t, context.Background(), s.Handler(), body, signBody(testSecret, now, body), nil)
	}()

	call := <-entered
	if string(call.body) != body {
		t.Fatalf("appended body = %q, want exact authenticated bytes %q", call.body, body)
	}
	if !call.acceptedAt.Equal(now) {
		t.Fatalf("append acceptedAt = %s, want %s", call.acceptedAt, now)
	}
	select {
	case <-done:
		t.Fatal("handler acknowledged before durable append returned")
	default:
	}
	assertNoAcceptedWebhookEffects(t, s, rec, nil, ingestCalls, acceptedCalls)

	close(release)
	rw := <-done
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rw.Code, http.StatusOK)
	}
	select {
	case extra := <-entered:
		t.Fatalf("durable append called more than once: %+v", extra)
	default:
	}
	assertNoAcceptedWebhookEffects(t, s, rec, nil, ingestCalls, acceptedCalls)
}

func TestHandler_DurableAppendFailureIsOpaqueAndHasNoAcceptedEffects(t *testing.T) {
	now := time.Date(2026, 6, 2, 10, 6, 30, 0, time.UTC)
	const (
		bodyMarker      = "private-body-marker"
		secretMarker    = "private-secret-marker"
		signatureMarker = "private-signature-marker"
		headerMarker    = "private-header-marker"
		errorMarker     = "private-error-marker"
	)
	body := `[{"timestamp":"2026-06-02T10:00:00Z","version":1,"type":"nodeCreated","tailnet":"example.com","message":"` + bodyMarker + `","data":{"nodeID":"n1"}}]`
	var logs bytes.Buffer
	rec := telemetrytest.New()
	ingestCalls := 0
	acceptedCalls := 0
	cross := dedup.New(8)
	appendCalls := 0
	s := New(Options{
		Listen: "127.0.0.1:0",
		Path:   "/webhook",
		Secret: secretMarker,
		OnIngest: func(string, string, int, int) {
			ingestCalls++
		},
		OnAccepted: func(ingest.AcceptedEvent) {
			acceptedCalls++
		},
	}, rec.Emitter(), slog.New(slog.NewTextHandler(&logs, nil)),
		WithClock(func() time.Time { return now }),
		WithDedup(cross),
		WithDurableAppend(func(context.Context, []byte, time.Time) error {
			appendCalls++
			return errors.New(errorMarker)
		}),
	)
	signature := signBody(secretMarker, now, body)
	rw := serveDurablePost(t, context.Background(), s.Handler(), body, signature, map[string]string{
		"X-Private":   headerMarker,
		"Traceparent": signatureMarker,
	})

	if rw.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rw.Code, http.StatusServiceUnavailable)
	}
	if got := rw.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
	if appendCalls != 1 {
		t.Fatalf("durable append calls = %d, want 1", appendCalls)
	}
	assertNoAcceptedWebhookEffects(t, s, rec, cross, ingestCalls, acceptedCalls)

	diagnostics := rw.Body.String() + logs.String()
	for _, marker := range []string{bodyMarker, secretMarker, signatureMarker, headerMarker, errorMarker, signature} {
		if strings.Contains(diagnostics, marker) {
			t.Errorf("opaque durability rejection leaked %q in response/logs: %q", marker, diagnostics)
		}
	}
	points := rec.MetricPoints(MetricRejected)
	if len(points) != 1 || points[0].Attrs[attrReason] != "wal_unavailable" {
		t.Fatalf("rejection points = %+v, want one bounded wal_unavailable reason", points)
	}
}

func TestHandler_DurableCancellationBoundary(t *testing.T) {
	now := time.Date(2026, 6, 2, 10, 6, 30, 0, time.UTC)
	body := twoEventBody
	signature := signBody(testSecret, now, body)

	t.Run("already canceled never appends", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		rec := telemetrytest.New()
		appendCalls := 0
		s := New(Options{Listen: "127.0.0.1:0", Path: "/webhook", Secret: testSecret},
			rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)),
			WithClock(func() time.Time { return now }),
			WithDurableAppend(func(context.Context, []byte, time.Time) error {
				appendCalls++
				return nil
			}),
		)

		rw := serveDurablePost(t, ctx, s.Handler(), body, signature, nil)
		if rw.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", rw.Code, http.StatusServiceUnavailable)
		}
		if rw.Header().Get("Retry-After") != "1" {
			t.Fatalf("Retry-After = %q, want 1", rw.Header().Get("Retry-After"))
		}
		if appendCalls != 0 {
			t.Fatalf("append calls = %d, want 0", appendCalls)
		}
		assertNoAcceptedWebhookEffects(t, s, rec, nil, 0, 0)
	})

	t.Run("successful append is acknowledged despite later cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		rec := telemetrytest.New()
		appendCalls := 0
		s := New(Options{Listen: "127.0.0.1:0", Path: "/webhook", Secret: testSecret},
			rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)),
			WithClock(func() time.Time { return now }),
			WithDurableAppend(func(gotCtx context.Context, _ []byte, _ time.Time) error {
				appendCalls++
				cancel()
				if !errors.Is(gotCtx.Err(), context.Canceled) {
					t.Errorf("durable append context error = %v, want request cancellation", gotCtx.Err())
				}
				return nil
			}),
		)

		rw := serveDurablePost(t, ctx, s.Handler(), body, signature, nil)
		if rw.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rw.Code, http.StatusOK)
		}
		if appendCalls != 1 {
			t.Fatalf("append calls = %d, want 1", appendCalls)
		}
		assertNoAcceptedWebhookEffects(t, s, rec, nil, 0, 0)
	})
}

func TestApplyDurable_ReplaysTrustedBodyWithPersistedAcceptanceTime(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	acceptedAt := time.Date(2026, 6, 2, 10, 6, 30, 123, time.UTC)
	body := `[` +
		`{"timestamp":"2026-06-02T10:00:00Z","version":1,"type":"nodeCreated","tailnet":"one.example.com","message":"one","data":{"nodeID":"n1"}},` +
		`{"timestamp":"2026-06-02T10:05:00Z","version":2,"type":"future","tailnet":"two.example.com","message":"two"}` +
		`]`
	var ingestRecords, ingestBytes int
	var accepted []ingest.AcceptedEvent
	rec := telemetrytest.New()
	s := New(Options{
		Listen:    "127.0.0.1:0",
		Path:      "/webhook",
		Secret:    testSecret,
		Tolerance: time.Nanosecond,
		OnIngest: func(_ string, _ string, records, bodyBytes int) {
			ingestRecords += records
			ingestBytes += bodyBytes
		},
		OnAccepted: func(event ingest.AcceptedEvent) {
			accepted = append(accepted, event)
		},
	}, rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)), WithClock(func() time.Time { return now }))

	if err := s.ApplyDurable(context.Background(), []byte(body), acceptedAt); err != nil {
		t.Fatalf("ApplyDurable: %v", err)
	}
	if ingestRecords != 2 || ingestBytes != len(body) {
		t.Fatalf("OnIngest records/bytes = %d/%d, want 2/%d", ingestRecords, ingestBytes, len(body))
	}
	if got := len(rec.LogRecords()); got != 2 {
		t.Fatalf("log records = %d, want 2", got)
	}
	var events, known, unknown float64
	for _, point := range rec.MetricPoints(MetricEvents) {
		events += point.Value
	}
	for _, point := range rec.MetricPoints(MetricSchemaDrift) {
		switch point.Attrs[attrSchemaStatus] {
		case "known":
			known += point.Value
		case "unknown":
			unknown += point.Value
		}
	}
	if events != 2 || known != 1 || unknown != 1 {
		t.Fatalf("event/schema counters = %v known=%v unknown=%v, want 2/1/1", events, known, unknown)
	}
	if len(accepted) != 2 {
		t.Fatalf("accepted events = %d, want 2", len(accepted))
	}
	for i, event := range accepted {
		if !event.AcceptedAt.Equal(acceptedAt) {
			t.Errorf("accepted event %d AcceptedAt = %s, want persisted %s", i, event.AcceptedAt, acceptedAt)
		}
	}
}

func TestApplyDurable_ValidatesWholeBatchBeforeEffectsAndHonorsCancellation(t *testing.T) {
	now := time.Date(2026, 6, 2, 10, 6, 30, 0, time.UTC)
	validThenInvalid := `[{"timestamp":"2026-06-02T10:00:00Z","version":1,"type":"nodeCreated","tailnet":"example.com"},{"version":"wrong"}]`
	for _, tt := range []struct {
		name string
		ctx  func() context.Context
		body string
	}{
		{name: "invalid later event", ctx: context.Background, body: validThenInvalid},
		{name: "canceled before apply", ctx: func() context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		}, body: twoEventBody},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := telemetrytest.New()
			ingestCalls := 0
			acceptedCalls := 0
			cross := dedup.New(8)
			s := New(Options{
				Listen: "127.0.0.1:0",
				Path:   "/webhook",
				OnIngest: func(string, string, int, int) {
					ingestCalls++
				},
				OnAccepted: func(ingest.AcceptedEvent) {
					acceptedCalls++
				},
			}, rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)), WithDedup(cross))

			if err := s.ApplyDurable(tt.ctx(), []byte(tt.body), now); err == nil {
				t.Fatal("ApplyDurable error = nil, want refusal")
			}
			assertNoAcceptedWebhookEffects(t, s, rec, cross, ingestCalls, acceptedCalls)
		})
	}
}

func TestApplyDurable_DeduplicatesCanonicalEventsAcrossJSONOrder(t *testing.T) {
	acceptedAt := time.Date(2026, 6, 2, 10, 6, 30, 0, time.UTC)
	first := `[{"version":1,"type":"nodeCreated","tailnet":"e.com","message":"one","data":{"nodeID":"n1","unknown":{"b":2,"a":1}}}]`
	second := `[{"data":{"unknown":{"a":1,"b":2},"nodeID":"n1"},"message":"one","tailnet":"e.com","type":"nodeCreated","version":1},{"version":1,"type":"nodeDeleted","tailnet":"e.com","message":"new","data":{"nodeID":"n2"}}]`
	var accepted []ingest.AcceptedEvent
	rec := telemetrytest.New()
	s := New(Options{
		Listen: "127.0.0.1:0",
		Path:   "/webhook",
		OnAccepted: func(event ingest.AcceptedEvent) {
			accepted = append(accepted, event)
		},
	}, rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	for _, body := range []string{first, second} {
		if err := s.ApplyDurable(context.Background(), []byte(body), acceptedAt); err != nil {
			t.Fatalf("ApplyDurable: %v", err)
		}
	}
	if got := len(rec.LogRecords()); got != 2 {
		t.Fatalf("logs = %d, want 2 (canonical duplicate suppressed; new event emitted)", got)
	}
	if len(accepted) != 2 {
		t.Fatalf("accepted events = %d, want 2 delivery-dedup survivors", len(accepted))
	}
	var duplicates float64
	for _, point := range rec.MetricPoints(MetricDuplicates) {
		duplicates += point.Value
	}
	if duplicates != 1 {
		t.Fatalf("duplicates = %v, want 1", duplicates)
	}
}

func TestHandler_NilDurableAppenderPreservesSynchronousBehavior(t *testing.T) {
	now := time.Date(2026, 6, 2, 10, 6, 30, 0, time.UTC)
	ingestCalls := 0
	acceptedCalls := 0
	rec := telemetrytest.New()
	s := New(Options{
		Listen: "127.0.0.1:0",
		Path:   "/webhook",
		Secret: testSecret,
		OnIngest: func(string, string, int, int) {
			ingestCalls++
		},
		OnAccepted: func(ingest.AcceptedEvent) {
			acceptedCalls++
		},
	}, rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithClock(func() time.Time { return now }),
		WithDurableAppend(nil),
	)

	rw := serveDurablePost(t, context.Background(), s.Handler(), twoEventBody, signBody(testSecret, now, twoEventBody), nil)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rw.Code, http.StatusOK)
	}
	if ingestCalls != 1 || acceptedCalls != 2 || len(rec.LogRecords()) != 2 {
		t.Fatalf("synchronous effects ingest/accepted/logs = %d/%d/%d, want 1/2/2",
			ingestCalls, acceptedCalls, len(rec.LogRecords()))
	}
}
