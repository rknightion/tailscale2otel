package app

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
)

// The page labeled a field "last export" and computed it from the freshest
// COLLECTOR success, so a completely broken OTLP pipeline still read as recent
// delivery. Exporter outcomes are now the source (#317).

func deliveryApp(t *testing.T, states []telemetry.DeliveryState) *App {
	t.Helper()
	return &App{
		cfg:      config.Default(),
		logger:   slog.New(slog.DiscardHandler),
		delivery: func() []telemetry.DeliveryState { return states },
	}
}

func TestDeliverySignalsCarryPerSignalState(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	a := deliveryApp(t, []telemetry.DeliveryState{
		{Signal: telemetry.SignalMetrics, Exports: 10, LastSuccessAt: now, LastDurationSeconds: 0.25},
		{Signal: telemetry.SignalLogs, Exports: 10, Failures: 4, ConsecutiveFailures: 4,
			LastFailureAt: now, LastErrorClass: "unauthenticated"},
		{Signal: telemetry.SignalTraces},
	})

	rows := a.deliverySignals()
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want one per signal", len(rows))
	}
	byName := map[string]statusdata.DeliverySignal{}
	for _, r := range rows {
		byName[r.Signal] = r
	}

	m := byName[telemetry.SignalMetrics]
	if m.LastSuccessAt == "" || m.LastDurationMs != 250 {
		t.Errorf("metrics = %+v, want a success time and a 250ms duration", m)
	}
	if m.Failing {
		t.Error("a signal with no failures is marked failing")
	}

	l := byName[telemetry.SignalLogs]
	if !l.Failing {
		t.Error("four consecutive failures is not a blip and must be marked failing")
	}
	if l.LastErrorClass != "unauthenticated" {
		t.Errorf("logs error class = %q, want the classified value", l.LastErrorClass)
	}
	// The signals are independent: a healthy metric pipeline beside a broken log
	// pipeline is exactly the state the collector proxy could not express.
	if m.Failing == l.Failing {
		t.Error("the two signals report the same state despite opposite inputs")
	}

	if tr := byName[telemetry.SignalTraces]; tr.Exports != 0 || tr.Failing {
		t.Errorf("traces = %+v, want an idle row rather than an omitted one", tr)
	}
}

// Sustained export failure has to reach the health verdict, or the page says
// healthy while nothing is being delivered.
func TestSustainedExportFailureDegradesHealth(t *testing.T) {
	healthy := []statusdata.DeliverySignal{{Signal: "metrics", Exports: 10}}
	if got := deliveryHealthReasons(healthy); len(got) != 0 {
		t.Fatalf("a working pipeline produced health reasons %v", got)
	}

	broken := []statusdata.DeliverySignal{
		{Signal: "metrics", Exports: 10},
		{Signal: "logs", Exports: 10, Failures: 5, ConsecutiveFailures: 5, Failing: true, LastErrorClass: "unavailable"},
	}
	reasons := deliveryHealthReasons(broken)
	if len(reasons) != 1 {
		t.Fatalf("got %d reasons, want exactly the failing signal: %v", len(reasons), reasons)
	}
	if !strings.Contains(reasons[0], "logs") || !strings.Contains(reasons[0], "unavailable") {
		t.Errorf("reason = %q, want the signal and its error class", reasons[0])
	}

	collectors := []statusdata.CollectorStatus{ranOK("devices")}
	health, _ := deriveHealth(collectors, reasons)
	if health != healthDegraded {
		t.Errorf("health = %q with a failing export pipeline, want %q", health, healthDegraded)
	}
}

// A backend outage must not pull every replica out of rotation — that turns one
// vendor's bad afternoon into a cascading outage. The asymmetry is deliberate
// and has to stay deliberate.
func TestExportFailureDoesNotGateReadiness(t *testing.T) {
	a := deliveryApp(t, []telemetry.DeliveryState{
		{Signal: telemetry.SignalMetrics, Exports: 9, Failures: 9, ConsecutiveFailures: 9},
	})
	a.readyState = newComponentHealth()

	if got := a.componentFailureReasons(); len(got) != 0 {
		t.Fatalf("export failures leaked into the readiness source: %v", got)
	}
	ready, reason := readinessVerdict([]statusdata.CollectorStatus{ranOK("devices")}, a.componentFailureReasons())
	if !ready {
		t.Errorf("readiness = false (%q) because OTLP export is failing", reason)
	}
}

// No provider set wired (the App-less seams) must not panic or invent rows.
func TestDeliverySignalsWithNoProvider(t *testing.T) {
	a := &App{cfg: config.Default(), logger: slog.New(slog.DiscardHandler)}
	if got := a.deliverySignals(); got != nil {
		t.Errorf("deliverySignals() = %v with no provider set, want nil", got)
	}
}
