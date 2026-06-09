package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func scrape(t *testing.T, ps *ProviderSet) string {
	t.Helper()
	srv := httptest.NewServer(promhttp.HandlerFor(ps.PromGatherers(), promhttp.HandlerOpts{}))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("scrape status = %d, body=%s", resp.StatusCode, body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

func TestPrometheusMultiTailnetExposition(t *testing.T) {
	ctx := context.Background()
	ps, err := NewProviderSet(ctx, Options{ServiceName: "tailscale2otel", PrometheusEnabled: true, Protocol: "stdout", StdoutWriter: io.Discard},
		[]PerTailnetOptions{{Name: "alpha", InstanceID: "i/alpha"}, {Name: "beta", InstanceID: "i/beta"}})
	if err != nil {
		t.Fatalf("NewProviderSet: %v", err)
	}
	defer func() { _ = ps.Shutdown(ctx) }()

	// tailscale.devices.count is a unit-"1" gauge -> tailscale_devices_count_ratio.
	ps.Tailnet("alpha").Emitter().Gauge("tailscale.devices.count", "1", "devices", 3, nil)
	ps.Tailnet("beta").Emitter().Gauge("tailscale.devices.count", "1", "devices", 7, nil)
	ps.Process().Emitter().Gauge("tailscale2otel.up", "1", "up", 1, nil)

	body := scrape(t, ps)

	// Assert the base name appears (do NOT hardcode the unit suffix: the OTEL
	// Prometheus exporter's unit-"1" handling may differ from Mimir's _ratio rule;
	// match a prefix instead). Each tailnet's series must be present and carry its
	// own tailscale_tailnet label — a per-data-point attribute (roadmap item L),
	// not a resource-promoted constant label, and it must appear exactly once.
	hasAlpha, hasBeta := false, false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "tailscale_devices_count") {
			if strings.Count(line, "tailscale_tailnet=") > 1 {
				t.Errorf("duplicate tailscale_tailnet label on series: %q", line)
			}
			if strings.Contains(line, `tailscale_tailnet="alpha"`) {
				hasAlpha = true
			}
			if strings.Contains(line, `tailscale_tailnet="beta"`) {
				hasBeta = true
			}
		}
	}
	if !hasAlpha || !hasBeta {
		t.Errorf("missing per-tailnet devices_count series (alpha=%v beta=%v); body:\n%s", hasAlpha, hasBeta, body)
	}
	if strings.Contains(body, "otel_scope_name") {
		t.Errorf("otel_scope_name leaked (WithoutScopeInfo not applied); body:\n%s", body)
	}
	if !strings.Contains(body, "target_info") {
		t.Errorf("target_info missing; body:\n%s", body)
	}
	// Process-global series must NOT carry a tailnet label.
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "tailscale2otel_up") && strings.Contains(line, "tailscale_tailnet=") {
			t.Errorf("process-global up carries tailnet label: %q", line)
		}
	}
}

func TestPrometheusSingleTailnetLabel(t *testing.T) {
	ctx := context.Background()
	ps, err := NewProviderSet(ctx, Options{ServiceName: "tailscale2otel", PrometheusEnabled: true, Protocol: "stdout", StdoutWriter: io.Discard},
		[]PerTailnetOptions{{Name: "solo", InstanceID: "i"}})
	if err != nil {
		t.Fatalf("NewProviderSet: %v", err)
	}
	defer func() { _ = ps.Shutdown(ctx) }()
	ps.Tailnet("solo").Emitter().Gauge("tailscale.devices.count", "1", "devices", 5, nil)
	body := scrape(t, ps)
	if !strings.Contains(body, `tailscale_tailnet="solo"`) {
		t.Errorf("single-tailnet missing tailnet label (data-point attr, item L); body:\n%s", body)
	}
}
