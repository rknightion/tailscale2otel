package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/config"
)

// A persistent flow store that never opened used to be one ERROR line at
// startup and nothing else: flowsEnabled() went false, flowStoreInfo()
// short-circuited on Enabled, and the resulting page was byte-identical to one
// where flows were simply switched off. An operator looking at the status page
// had no way to tell "you asked for flow history and are not getting it" from
// "you did not ask for flow history".
func TestFlowStoreOpenFailureIsVisibleOnTheStatusSurface(t *testing.T) {
	cfg := config.Default()
	cfg.Flows.Store.Directory = "/var/lib/tailscale2otel/flows"
	a := &App{
		cfg: cfg,
		runtimes: []*tailnetRuntime{
			{name: "acme.example", flowStoreErr: errors.New("legacy database cannot prove its tailnet identity")},
		},
	}

	info := a.flowStoreInfo()
	if len(info.Failures) != 1 {
		t.Fatalf("Failures = %d, want 1 — a store that failed to open must be reported, not silently absent", len(info.Failures))
	}
	if info.Failures[0].Tailnet != "acme.example" {
		t.Errorf("Failures[0].Tailnet = %q, want %q", info.Failures[0].Tailnet, "acme.example")
	}
	if !strings.Contains(info.Failures[0].Error, "cannot prove its tailnet identity") {
		t.Errorf("Failures[0].Error = %q, want the underlying refusal", info.Failures[0].Error)
	}
	if info.Enabled {
		t.Errorf("Enabled = true with no store open, want false")
	}
}

func TestFlowStoreOpenFailureDegradesReportedHealth(t *testing.T) {
	cfg := config.Default()
	cfg.Flows.Store.Directory = "/var/lib/tailscale2otel/flows"
	a := &App{
		cfg: cfg,
		runtimes: []*tailnetRuntime{
			{name: "acme.example", flowStoreErr: errors.New("disk on fire")},
		},
	}

	reasons := flowStoreHealthReasons(a.flowStoreInfo())
	if len(reasons) != 1 {
		t.Fatalf("health reasons = %v, want exactly one", reasons)
	}
	if !strings.Contains(reasons[0], "acme.example") || !strings.Contains(reasons[0], "disk on fire") {
		t.Errorf("reason %q names neither the tailnet nor the cause", reasons[0])
	}

	state, all := deriveHealth(nil, reasons)
	if state != healthDegraded {
		t.Errorf("health = %q, want %q — a requested flow store that never opened is a degraded service", state, healthDegraded)
	}
	if len(all) == 0 {
		t.Error("degraded health carried no reasons")
	}
}

// A healthy deployment must stay clean: no flow store configured, or one that
// opened fine, produces no failures and no health reasons.
func TestFlowStoreHealthySurfaceReportsNothing(t *testing.T) {
	a := &App{cfg: config.Default()}
	info := a.flowStoreInfo()
	if len(info.Failures) != 0 {
		t.Errorf("Failures = %v, want none", info.Failures)
	}
	if got := flowStoreHealthReasons(info); len(got) != 0 {
		t.Errorf("health reasons = %v, want none", got)
	}
}

// The failure must NOT gate readiness. The exporter keeps working when the
// admin flow view is off, and pulling every pod out of rotation for it would
// turn a partial fault into an outage — the same asymmetry the delivery and
// degraded-collector paths already keep.
func TestFlowStoreOpenFailureDoesNotGateReadiness(t *testing.T) {
	cfg := config.Default()
	cfg.Flows.Store.Directory = "/var/lib/tailscale2otel/flows"
	a := &App{
		cfg:        cfg,
		readyState: newComponentHealth(),
		runtimes: []*tailnetRuntime{
			{name: "acme.example", flowStoreErr: errors.New("disk on fire")},
		},
	}
	if got := a.componentFailureReasons(); len(got) != 0 {
		t.Errorf("component failures = %v, want none: a failed flow store must not make the pod unready", got)
	}
}
