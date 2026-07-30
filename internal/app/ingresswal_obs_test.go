package app

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v4/internal/ingresswal"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

func TestEmitIngressWALHealthUsesProcessGlobalAttributeFreeGauges(t *testing.T) {
	rec := telemetrytest.New()
	wal := &coordinatorWAL{}
	wal.pending = []ingresswal.Envelope{
		{Body: []byte("one")},
		{Body: []byte("three")},
	}
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{
		testIngressRoute("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook),
	})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}

	emitIngressWALHealth(rec.Emitter(), coordinator)

	for name, want := range map[string]float64{
		appcatalog.MetricIngressWALPendingEntries:    2,
		appcatalog.MetricIngressWALPendingSize:       8,
		appcatalog.MetricIngressWALOrphanStages:      0,
		appcatalog.MetricIngressWALOrphanSize:        0,
		appcatalog.MetricIngressWALCompletionMarkers: 0,
	} {
		points := rec.MetricPoints(name)
		if len(points) != 1 {
			t.Fatalf("%s points = %d, want 1", name, len(points))
		}
		if points[0].Value != want {
			t.Errorf("%s value = %v, want %v", name, points[0].Value, want)
		}
		if len(points[0].Attrs) != 0 {
			t.Errorf("%s attrs = %v, want none", name, points[0].Attrs)
		}
	}
}
