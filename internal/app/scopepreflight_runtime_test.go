package app

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/rknightion/tailscale2otel/v5/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/provider"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

func scopePreflightExpectation(rows []statusdata.CapabilityRow) (map[string]float64, map[string]struct{}) {
	want := make(map[string]float64)
	absent := make(map[string]struct{})
	for _, row := range rows {
		switch row.ScopeStatus {
		case statusdata.ScopeSatisfied:
			want[row.Capability] = 1
		case statusdata.ScopeInsufficient:
			want[row.Capability] = 0
		default:
			absent[row.Capability] = struct{}{}
		}
	}
	for capability := range want {
		delete(absent, capability)
	}
	return want, absent
}

func assertScopePreflightFixture(t *testing.T, rows []statusdata.CapabilityRow) {
	t.Helper()
	seen := make(map[string]bool)
	for _, row := range rows {
		seen[row.ScopeStatus] = true
	}
	for _, status := range []string{
		statusdata.ScopeSatisfied,
		statusdata.ScopeInsufficient,
		statusdata.ScopeUnknown,
		statusdata.ScopeNotApplicable,
	} {
		if !seen[status] {
			t.Errorf("fixture matrix has no %q scope row; expected all scope states", status)
		}
	}
}

func assertScopePreflightPoints(t *testing.T, rec *telemetrytest.Recorder, want map[string]float64, absent map[string]struct{}) {
	t.Helper()
	got := make(map[string]float64)
	for _, point := range rec.MetricPoints(MetricCapabilityScopeSatisfied) {
		capability := point.Attrs[semconv.AttrCapability]
		if capability == "" {
			t.Error("scope preflight point has no capability attribute")
			continue
		}
		if point.Value != 0 && point.Value != 1 {
			t.Errorf("capability %q = %v, want bounded 0 or 1", capability, point.Value)
		}
		if _, duplicate := got[capability]; duplicate {
			t.Errorf("capability %q emitted more than once", capability)
		}
		got[capability] = point.Value
	}
	if len(got) != len(want) {
		t.Errorf("scope preflight points = %v, want exactly %v", got, want)
	}
	for capability, value := range want {
		gotValue, ok := got[capability]
		if !ok {
			t.Errorf("capability %q is missing, want value %v (points=%v)", capability, value, got)
			continue
		}
		if gotValue != value {
			t.Errorf("capability %q = %v, want %v (points=%v)", capability, gotValue, value, got)
		}
	}
	for capability := range absent {
		if _, ok := got[capability]; ok {
			t.Errorf("capability %q has a scope preflight point despite an unknown or not-applicable scope", capability)
		}
	}
}

// TestRunActiveEmitsScopePreflight exercises the production heartbeat callback
// through newApp. It advances the fake heartbeat clock once after the initial
// emission, proving that each heartbeat recomputes the same matrix the admin
// status page exposes and keeps unknown/not-applicable rows absent.
func TestRunActiveEmitsScopePreflight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Default()
		cfg.Tailscale.Tailnet = "example.com"
		cfg.Tailscale.Auth.Method = "oauth"
		cfg.Tailscale.Auth.OAuth.Scopes = []string{"devices:core:read"}
		cfg.SelfObservability.Enabled = true
		cfg.Admin.Enabled = false
		cfg.VersionChecks.Self.Enabled = false
		cfg.VersionChecks.Devices.Enabled = false
		cfg.Scheduler.InitialStaggerWindow = 0
		for _, enabled := range []*bool{
			&cfg.Collectors.Devices.Enabled,
			&cfg.Collectors.Users.Enabled,
			&cfg.Collectors.Keys.Enabled,
			&cfg.Collectors.Settings.Enabled,
			&cfg.Collectors.Acl.Enabled,
			&cfg.Collectors.Dns.Enabled,
			&cfg.Collectors.Contacts.Enabled,
			&cfg.Collectors.Webhooks.Enabled,
			&cfg.Collectors.PostureIntegrations.Enabled,
			&cfg.Collectors.LogStream.Enabled,
			&cfg.Collectors.OAuthApps.Enabled,
			&cfg.Collectors.Services.Enabled,
			&cfg.Collectors.NodeMetrics.Enabled,
			&cfg.Collectors.Flowlogs.Enabled,
			&cfg.Collectors.Auditlogs.Enabled,
			&cfg.Collectors.K8sAudit.Enabled,
			&cfg.Collectors.PAM.Enabled,
		} {
			*enabled = false
		}

		rec := telemetrytest.New()
		a := newApp(cfg, "vtest", nil, rec.Emitter(), tracenoop.NewTracerProvider().Tracer("test"),
			func(context.Context) error { return nil },
			provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")), collector.NewMemoryStore(), NewAPIStats())

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- a.runActive(ctx, false) }()

		// runActive starts runHeartbeat, whose initial emission runs before it
		// blocks in its select. The callback is the production call site under test.
		synctest.Wait()
		rows := a.capabilityMatrix(a.primaryAPIState())
		assertScopePreflightFixture(t, rows)
		want, absent := scopePreflightExpectation(rows)
		assertScopePreflightPoints(t, rec, want, absent)

		// Advance exactly one heartbeat after the initial callback has quiesced. The
		// second assertion exercises the ticker path and confirms that the repeated
		// production callback preserves the matrix's values and absences.
		time.Sleep(heartbeatInterval)
		synctest.Wait()
		rows = a.capabilityMatrix(a.primaryAPIState())
		assertScopePreflightFixture(t, rows)
		want, absent = scopePreflightExpectation(rows)
		assertScopePreflightPoints(t, rec, want, absent)

		cancel()
		synctest.Wait()
		if err := <-done; err != nil {
			t.Fatalf("runActive() = %v, want nil on cancellation", err)
		}
	})
}
