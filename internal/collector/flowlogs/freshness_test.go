package flowlogs

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/dedup"
	"github.com/rknightion/tailscale2otel/v5/internal/enrich"
	"github.com/rknightion/tailscale2otel/v5/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v5/internal/ingest"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

func TestCollectWindow_AcceptedObserverOnlySeesValidIntraSourceAcceptedRecords(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	from, to := now.Add(-time.Minute), now
	flow := oneTCPResponse().Logs[0]

	t.Run("accepted after processor handoff", func(t *testing.T) {
		rec := telemetrytest.New()
		var got []ingest.AcceptedEvent
		c := New(&fakeAPI{resp: flowlog.NetworkResponse{Logs: []flowlog.FlowLog{flow}}}, newProcessor(), 0, 0, nil, nil,
			WithAcceptedObserver(func(event ingest.AcceptedEvent) {
				if len(rec.MetricPoints(flowlog.MetricIO)) == 0 {
					t.Fatal("observer ran before processor handoff")
				}
				got = append(got, event)
			}),
			withClock(func() time.Time { return now }))

		if _, err := c.CollectWindow(context.Background(), from, to, rec.Emitter()); err != nil {
			t.Fatalf("CollectWindow() error = %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("accepted events = %d, want 1", len(got))
		}
		want := ingest.AcceptedEvent{
			Source:      semconv.IngestSourcePoll,
			Signal:      semconv.IngestSignalFlow,
			EventTime:   flowlog.EventTimestamp(flow),
			CaptureTime: flowlog.CaptureTimestamp(flow),
			AcceptedAt:  now,
		}
		if got[0] != want {
			t.Fatalf("accepted event = %+v, want %+v", got[0], want)
		}
	})

	t.Run("exact intra-source replay", func(t *testing.T) {
		var got []ingest.AcceptedEvent
		c := New(&fakeAPI{resp: flowlog.NetworkResponse{Logs: []flowlog.FlowLog{flow}}}, newProcessor(), 0, 0, nil, nil,
			WithAcceptedObserver(func(event ingest.AcceptedEvent) { got = append(got, event) }),
			withClock(func() time.Time { return now }))
		rec := telemetrytest.New()
		for range 2 {
			if _, err := c.CollectWindow(context.Background(), from, to, rec.Emitter()); err != nil {
				t.Fatalf("CollectWindow() error = %v", err)
			}
		}
		if len(got) != 1 {
			t.Fatalf("accepted events = %d, want 1 after exact replay", len(got))
		}
	})

	t.Run("semantically invalid", func(t *testing.T) {
		invalid := flow
		invalid.VirtualTraffic = append([]flowlog.ConnectionCounts(nil), flow.VirtualTraffic...)
		invalid.VirtualTraffic[0].TxBytes = -1
		var got []ingest.AcceptedEvent
		c := New(&fakeAPI{resp: flowlog.NetworkResponse{Logs: []flowlog.FlowLog{invalid}}}, newProcessor(), 0, 0, nil, nil,
			WithAcceptedObserver(func(event ingest.AcceptedEvent) { got = append(got, event) }),
			withClock(func() time.Time { return now }))
		if _, err := c.CollectWindow(context.Background(), from, to, telemetrytest.New().Emitter()); err != nil {
			t.Fatalf("CollectWindow() error = %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("accepted events = %d, want 0 for invalid record", len(got))
		}
	})

	t.Run("cross-source processor suppression", func(t *testing.T) {
		rec := telemetrytest.New()
		proc := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{Dedup: dedup.New(0)})
		proc.Process(flow, rec.Emitter())
		before := len(rec.MetricPoints(flowlog.MetricIO))
		var got []ingest.AcceptedEvent
		c := New(&fakeAPI{resp: flowlog.NetworkResponse{Logs: []flowlog.FlowLog{flow}}}, proc, 0, 0, nil, nil,
			WithAcceptedObserver(func(event ingest.AcceptedEvent) { got = append(got, event) }),
			withClock(func() time.Time { return now }))
		if _, err := c.CollectWindow(context.Background(), from, to, rec.Emitter()); err != nil {
			t.Fatalf("CollectWindow() error = %v", err)
		}
		if after := len(rec.MetricPoints(flowlog.MetricIO)); after != before {
			t.Fatalf("processor emitted %d io points after cross-source duplicate, want %d", after, before)
		}
		if len(got) != 1 {
			t.Fatalf("accepted events = %d, want 1 despite downstream cross-source suppression", len(got))
		}
	})
}
